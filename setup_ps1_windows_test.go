//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests run the functions of setup.ps1 under Windows PowerShell 5.1 (the
// functions are loaded from the script's AST, so the script's download and
// config code is exercised without running the network part) and run the whole
// script once in a folder where everything is already installed. Nothing is
// downloaded: Download-File is replaced by a fake that copies local zips.

const setupHarnessPrelude = `
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$ast = [System.Management.Automation.Language.Parser]::ParseFile($env:CT_SETUP_PS1, [ref]$null, [ref]$null)
$funcs = $ast.FindAll({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] }, $false)
foreach ($f in $funcs) { . ([scriptblock]::Create($f.Extent.Text)) }
$work = $env:CT_WORK
function Assert-That($cond, [string]$msg) { if (-not $cond) { throw "FAILED: $msg" } }
function Assert-Throws([scriptblock]$sb, [string]$pattern) {
    $threw = $false
    try { & $sb } catch {
        $threw = $true
        if ($_.Exception.Message -notmatch $pattern) { throw "wrong error: $($_.Exception.Message)" }
    }
    if (-not $threw) { throw "expected an error matching $pattern" }
}
function New-File([string]$path, [string]$content = 'x') {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $path) | Out-Null
    Set-Content -LiteralPath $path -Value $content
}
function New-Zip([string]$zip, [hashtable]$files) {
    $src = Join-Path $work ([guid]::NewGuid().ToString('N'))
    foreach ($k in $files.Keys) { New-File (Join-Path $src $k) $files[$k] }
    Compress-Archive -Path (Join-Path $src '*') -DestinationPath $zip
}
`

func powershellExe(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell is not available")
	}
	return p
}

// psEnv is the environment without PSModulePath: when go test runs from
// PowerShell 7 it would point Windows PowerShell 5.1 at modules it cannot load
// (Get-FileHash and friends go missing).
func psEnv() []string {
	return cleanPowerShellEnv(os.Environ())
}

func runSetupHarness(t *testing.T, body string) {
	t.Helper()
	exe := powershellExe(t)
	script, err := filepath.Abs("setup.ps1")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	file := filepath.Join(work, "harness.ps1")
	if err := os.WriteFile(file, []byte("\xef\xbb\xbf"+setupHarnessPrelude+body+"\n'HARNESS-OK'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", file)
	cmd.Env = append(psEnv(), "CT_SETUP_PS1="+script, "CT_WORK="+work, "TEMP="+work, "TMP="+work)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "HARNESS-OK") {
		t.Fatalf("PowerShell test failed: %v\n%s", err, out)
	}
}

func TestSetupParsesOnBothPowerShells(t *testing.T) {
	check := `$e = $null; [void][System.Management.Automation.Language.Parser]::ParseFile($env:CT_TARGET_PS1, [ref]$null, [ref]$e); if ($e) { $e | Out-String; exit 1 }`
	for _, scriptName := range []string{"setup.ps1", "install.ps1"} {
		script, err := filepath.Abs(scriptName)
		if err != nil {
			t.Fatal(err)
		}
		ran := 0
		for _, name := range []string{"powershell.exe", "pwsh.exe"} {
			exe, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			ran++
			cmd := exec.Command(exe, "-NoProfile", "-Command", check)
			cmd.Env = append(psEnv(), "CT_TARGET_PS1="+script)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s cannot parse %s: %v\n%s", name, scriptName, err, out)
			}
		}
		if ran == 0 {
			t.Skip("no PowerShell available")
		}
	}
}

func TestSetupBackendCompleteness(t *testing.T) {
	runSetupHarness(t, `
$r = Join-Path $work 'runtime'
Assert-That (-not (Test-BackendComplete 'cpu' (Join-Path $r 'cpu'))) 'a missing folder is not complete'
New-File (Join-Path $r 'cpu\llama-server.exe')
Assert-That (Test-BackendComplete 'cpu' (Join-Path $r 'cpu')) 'cpu needs only the exe'
New-File (Join-Path $r 'vulkan\llama-server.exe')
Assert-That (Test-BackendComplete 'vulkan' (Join-Path $r 'vulkan')) 'vulkan needs only the exe'
New-File (Join-Path $r 'cuda\llama-server.exe')
Assert-That (-not (Test-BackendComplete 'cuda' (Join-Path $r 'cuda'))) 'cuda without DLLs is the broken state of issue 20'
New-File (Join-Path $r 'cuda\cudart64_12.dll')
Assert-That (-not (Test-BackendComplete 'cuda' (Join-Path $r 'cuda'))) 'cuda needs cublas too'
New-File (Join-Path $r 'cuda\cublas64_12.dll')
Assert-That (Test-BackendComplete 'cuda' (Join-Path $r 'cuda')) 'cuda with exe and both DLLs is complete'
Remove-Item (Join-Path $r 'cuda\llama-server.exe')
Assert-That (-not (Test-BackendComplete 'cuda' (Join-Path $r 'cuda'))) 'DLLs alone are not complete'
`)
}

func TestSetupInstallBackendIsAllOrNothing(t *testing.T) {
	runSetupHarness(t, `
$runtimeDir = Join-Path $work 'runtime'
$srvZip = Join-Path $work 'srv.zip'
$dllZip = Join-Path $work 'dll.zip'
$emptyZip = Join-Path $work 'empty.zip'
New-Zip $srvZip @{ 'llama-server.exe' = 'new-server'; 'ggml-cuda.dll' = 'g' }
New-Zip $dllZip @{ 'cudart64_12.dll' = 'a'; 'cublas64_12.dll' = 'b'; 'cublasLt64_12.dll' = 'c' }
New-Zip $emptyZip @{ 'readme.txt' = 'no dlls here' }
function New-TestAsset([string]$name, [string]$zip, [string]$digest = 'auto') {
    $o = [ordered]@{ name = $name; browser_download_url = "https://example.invalid/$name" }
    if ($digest -eq 'auto') { $o.digest = 'sha256:' + (Get-FileSha256 $zip).ToLower() }
    elseif ($digest) { $o.digest = $digest }
    $global:zipFor[$o.browser_download_url] = $zip
    [pscustomobject]$o
}
$global:zipFor = @{}
$global:fail = @()
$global:downloads = @()
function Download-File([string]$url, [string]$dest, [string]$sha256 = '') {
    $global:downloads += $url
    if ($global:fail -contains $url) { throw 'simulated network failure' }
    Copy-Item -LiteralPath $global:zipFor[$url] -Destination $dest
}
$srv = New-TestAsset 'srv.zip' $srvZip
$dll = New-TestAsset 'dll.zip' $dllZip
$cuda = Join-Path $runtimeDir 'cuda'
function Assert-NoStaging { Assert-That (@(Get-ChildItem -LiteralPath $runtimeDir -Force -Filter '.staging-*' -ErrorAction SilentlyContinue).Count -eq 0) 'a staging folder was left behind' }

# 1. A clean install is complete and leaves nothing else behind.
Install-Backend $runtimeDir 'cuda' $srv $dll
Assert-That (Test-BackendComplete 'cuda' $cuda) 'cuda install is complete'
Assert-That (Test-Path (Join-Path $cuda 'ggml-cuda.dll')) 'the server package files are installed'
Assert-NoStaging

# 2. A failed CUDA DLL download must not leave llama-server.exe without its DLLs.
Remove-Item -Recurse -Force $cuda
$global:fail = @($dll.browser_download_url)
Assert-Throws { Install-Backend $runtimeDir 'cuda' $srv $dll } 'simulated network failure'
Assert-That (-not (Test-Path $cuda)) 'no cuda folder after a failed DLL download'
Assert-NoStaging

# 3. The broken state of an older setup.ps1 (exe without DLLs) is removed on failure and replaced on success.
New-File (Join-Path $cuda 'llama-server.exe') 'old-server'
Assert-Throws { Install-Backend $runtimeDir 'cuda' $srv $dll } 'simulated network failure'
Assert-That (-not (Test-Path $cuda)) 'the incomplete leftover must be removed when the retry fails'
New-File (Join-Path $cuda 'llama-server.exe') 'old-server'
$global:fail = @()
Install-Backend $runtimeDir 'cuda' $srv $dll
Assert-That (Test-BackendComplete 'cuda' $cuda) 'the retry completes cuda'
Assert-That ((Get-Content -Raw (Join-Path $cuda 'llama-server.exe')) -match 'new-server') 'the old exe was replaced by the one of the same release'

# 4. A complete backend survives a failed attempt (nothing is deleted that works).
$global:fail = @($srv.browser_download_url)
Assert-Throws { Install-Backend $runtimeDir 'cuda' $srv $dll } 'simulated network failure'
Assert-That (Test-BackendComplete 'cuda' $cuda) 'a complete backend must stay'
$global:fail = @()

# 5. A package without any DLL is rejected for cuda.
$emptyDll = New-TestAsset 'emptydll.zip' $emptyZip
Remove-Item -Recurse -Force $cuda
Assert-Throws { Install-Backend $runtimeDir 'cuda' $srv $emptyDll } 'no DLL files'
Assert-That (-not (Test-Path $cuda)) 'no cuda folder after an empty DLL package'
Assert-NoStaging

# 6. Bad or missing digests are refused and nothing is installed.
$wrong = New-TestAsset 'wrong.zip' $srvZip ('sha256:' + ('0' * 64))
Assert-Throws { Install-Backend $runtimeDir 'vulkan' $wrong $null } 'checksum mismatch'
Assert-That (-not (Test-Path (Join-Path $runtimeDir 'vulkan'))) 'nothing installed after a checksum mismatch'
$nodigest = New-TestAsset 'nodigest.zip' $srvZip ''
Assert-Throws { Install-Backend $runtimeDir 'vulkan' $nodigest $null } 'No SHA256 digest'
Assert-That (-not (Test-Path (Join-Path $runtimeDir 'vulkan'))) 'nothing installed without a digest'
Assert-NoStaging

# 7. Plain backends install too.
Install-Backend $runtimeDir 'cpu' $srv $null
Assert-That (Test-BackendComplete 'cpu' (Join-Path $runtimeDir 'cpu')) 'cpu installs'
`)
}

func TestSetupAssertHashFailsClosed(t *testing.T) {
	runSetupHarness(t, `
$file = Join-Path $work 'a.bin'
Set-Content -LiteralPath $file -Value 'hello'
$good = Get-FileSha256 $file
Assert-Throws { Assert-Hash ([pscustomobject]@{ name = 'a' }) $file } 'No SHA256 digest'
Assert-Throws { Assert-Hash ([pscustomobject]@{ name = 'a'; digest = $null }) $file } 'No SHA256 digest'
Assert-Throws { Assert-Hash ([pscustomobject]@{ name = 'a'; digest = '' }) $file } 'No SHA256 digest'
Assert-Throws { Assert-Hash ([pscustomobject]@{ name = 'a'; digest = 'md5:abc' }) $file } 'No SHA256 digest'
Assert-Throws { Assert-Hash ([pscustomobject]@{ name = 'a'; digest = 'sha256:abc' }) $file } 'No SHA256 digest'
Assert-Throws { Assert-Hash ([pscustomobject]@{ name = 'a'; digest = ('sha256:' + ('0' * 64)) }) $file } 'checksum mismatch'
Assert-Hash ([pscustomobject]@{ name = 'a'; digest = ('sha256:' + $good.ToLower()) }) $file
Assert-Hash ([pscustomobject]@{ name = 'a'; digest = ('sha256:' + $good.ToUpper()) }) $file
Assert-Hash ([pscustomobject]@{ name = 'a'; sha256 = $good.ToLower() }) $file
Assert-Throws { Assert-Hash ([pscustomobject]@{ name = 'a'; sha256 = ('1' * 64) }) $file } 'checksum mismatch'
Assert-FileSha256 $file $good.ToLower() 'a'
Assert-Throws { Assert-FileSha256 $file ('1' * 64) 'a' } 'checksum mismatch'
`)
}

func TestSetupGetLlamaRelease(t *testing.T) {
	runSetupHarness(t, `
$llama = Get-LlamaRelease
Assert-That ($null -ne $llama -and $llama.Tag -match '^b[0-9]+$') 'pinned release has a tag like b12032'
$assets = $llama.Assets
Assert-That ($null -ne $assets) 'pinned release has assets'
foreach ($key in @('cpu', 'cuda', 'cudart', 'vulkan')) {
    $a = $assets[$key]
    Assert-That ($null -ne $a) "$key asset exists"
    Assert-That ($a.File -match '\.zip$') "$key asset is a zip"
    Assert-That ($a.Sha256 -match '^[0-9a-fA-F]{64}$') "$key sha256 is 64 hex characters"
}
Assert-That ($assets['cpu'].File -match '^llama-.*-bin-win-cpu-x64\.zip$') 'cpu asset filename pattern'
Assert-That ($assets['cuda'].File -match '^llama-.*-bin-win-cuda-12\.4-x64\.zip$') 'cuda asset filename pattern'
Assert-That ($assets['cudart'].File -match '^cudart-llama.*-bin-win-cuda-12\.4-x64\.zip$') 'cudart asset filename pattern'
Assert-That ($assets['vulkan'].File -match '^llama-.*-bin-win-vulkan-x64\.zip$') 'vulkan asset filename pattern'
`)
}

func TestSetupConfigPreservesUserEngine(t *testing.T) {
	runSetupHarness(t, `
New-File (Join-Path $work 'runtime\cpu\llama-server.exe')
New-File (Join-Path $work 'runtime\cuda\llama-server.exe')
function New-TestConfig([string]$server, $layers) {
    [pscustomobject]@{ hotkey = 'Ctrl+Alt+E'; gpu_layers = $layers; server_file = $server; fallback_server_file = 'runtime/cpu/llama-server.exe'; extra_unknown = 'keep' }
}
$cpu = 'runtime/cpu/llama-server.exe'
$cudaFile = 'runtime/cuda/llama-server.exe'

# Update (as scoop update does): the user switched to CPU in the tray, or limited the GPU layers.
$c = New-TestConfig $cpu 7
$changed = Update-BackendConfig $c $work 'cuda' $false $true $true
Assert-That (-not $changed) 'nothing to change on an update'
Assert-That ($c.server_file -eq $cpu -and $c.gpu_layers -eq 7) 'the engine the user chose on the tray must survive'
$c = New-TestConfig $cudaFile 24
$changed = Update-BackendConfig $c $work 'cuda' $false $true $true
Assert-That ((-not $changed) -and $c.server_file -eq $cudaFile -and $c.gpu_layers -eq 24) 'a custom gpu_layers must survive'

# Fresh install: the shipped config points at the CPU build, which did not exist at the start.
$c = New-TestConfig $cpu 0
$changed = Update-BackendConfig $c $work 'vulkan' $false $false $false
Assert-That ($changed -and $c.server_file -eq 'runtime/vulkan/llama-server.exe' -and $c.gpu_layers -eq 99) 'a fresh GPU install must switch to the GPU'
Assert-That ($c.fallback_server_file -eq $cpu -and $c.extra_unknown -eq 'keep') 'fallback is set and unknown keys survive'
$c = New-TestConfig $cpu 99
$changed = Update-BackendConfig $c $work 'cpu' $false $false $false
Assert-That ($c.server_file -eq $cpu -and $c.gpu_layers -eq 0) 'a fresh CPU install uses 0 layers'

# Retry after a failed GPU install: the GPU build was incomplete at the start and is complete now.
$c = New-TestConfig $cpu 0
$changed = Update-BackendConfig $c $work 'cuda' $false $true $false
Assert-That ($changed -and $c.server_file -eq $cudaFile -and $c.gpu_layers -eq 99) 'a newly completed GPU build is switched to'

# Explicit -Backend reconfigures.
$c = New-TestConfig $cudaFile 24
$changed = Update-BackendConfig $c $work 'cpu' $true $true $true
Assert-That ($changed -and $c.server_file -eq $cpu -and $c.gpu_layers -eq 0) 'an explicit backend rewrites the settings'

# The configured server is gone (for example removed after a failed install): fall back to what is installed.
$c = New-TestConfig 'runtime/vulkan/llama-server.exe' 99
$changed = Update-BackendConfig $c $work 'cpu' $false $true $true
Assert-That ($changed -and $c.server_file -eq $cpu -and $c.gpu_layers -eq 0) 'a missing configured server is replaced'

# Paths outside the folder are never trusted.
$abs = (Join-Path $work 'runtime\cuda\llama-server.exe')
foreach ($bad in @($abs, '..\runtime\cuda\llama-server.exe', '')) {
    $c = New-TestConfig $bad 5
    $changed = Update-BackendConfig $c $work 'cpu' $false $true $true
    Assert-That ($changed -and $c.server_file -eq $cpu) "unsafe server_file '$bad' is replaced"
}

# A custom (non-standard) existing server file is kept, and missing keys are filled in.
New-File (Join-Path $work 'runtime\custom\llama-server.exe')
$c = [pscustomobject]@{ server_file = 'runtime/custom/llama-server.exe' }
$changed = Update-BackendConfig $c $work 'cpu' $false $true $true
Assert-That ($changed -and $c.server_file -eq 'runtime/custom/llama-server.exe') 'a custom server file stays'
Assert-That ($c.fallback_server_file -eq $cpu -and $c.gpu_layers -eq 99) 'missing fallback and gpu_layers are filled in'

# A bare exe has no config.json: everything is created.
$c = [pscustomobject]@{}
$changed = Update-BackendConfig $c $work 'cpu' $false $false $false
Assert-That ($changed -and $c.server_file -eq $cpu -and $c.gpu_layers -eq 0 -and $c.fallback_server_file -eq $cpu) 'an empty config is filled in'
`)
}

func TestSetupModelChoice(t *testing.T) {
	runSetupHarness(t, `
$catalog = Get-ModelCatalog
Assert-That (@($catalog.Keys) -join ',' -eq '0.8b,2b,8b') 'the catalog offers 0.8b, 2b and 8b in that order'
foreach ($k in $catalog.Keys) {
    $m = $catalog[$k]
    Assert-That ($m.File -eq "plamo-3-translate-$k-Q4_K_M.gguf" -and $m.Repo -eq "plamo-3-translate-$k-GGUF") "names of $k"
    Assert-That ($m.Revision -match '^[0-9a-f]{40}$' -and $m.Sha256 -match '^[0-9a-f]{64}$' -and $m.TemplateSha256 -match '^[0-9a-f]{64}$') "$k is pinned"
}

# Nothing asked, nothing configured: the default.
Assert-That ((Resolve-ModelKey $catalog ([pscustomobject]@{}) $false '2b') -eq '2b') 'a fresh install gets 2b'
# An update (no -Model) keeps the model config.json names, with either slash and any case.
foreach ($v in @('models/plamo-3-translate-8b-Q4_K_M.gguf', 'models\PLAMO-3-translate-8b-q4_k_m.gguf')) {
    Assert-That ((Resolve-ModelKey $catalog ([pscustomobject]@{ model_file = $v }) $false '2b') -eq '8b') "an update keeps $v"
}
# A file that is not in the catalog does not decide anything.
Assert-That ((Resolve-ModelKey $catalog ([pscustomobject]@{ model_file = 'models/my.gguf' }) $false '2b') -eq '2b') 'an unknown model_file falls back to 2b'
Assert-That ((Resolve-ModelKey $catalog ([pscustomobject]@{ model_file = 5 }) $false '2b') -eq '2b') 'a non-string model_file is ignored'
# -Model wins over the config.
Assert-That ((Resolve-ModelKey $catalog ([pscustomobject]@{ model_file = 'models/plamo-3-translate-8b-Q4_K_M.gguf' }) $true '0.8B') -eq '0.8b') '-Model overrides the config'

# Only an explicit -Model rewrites model_file.
$c = [pscustomobject]@{ model_file = 'models/plamo-3-translate-8b-Q4_K_M.gguf'; keep = 1 }
Assert-That (-not (Update-ModelConfig $c $catalog['2b'] $false)) 'no -Model, no config change'
Assert-That ($c.model_file -eq 'models/plamo-3-translate-8b-Q4_K_M.gguf') 'model_file untouched without -Model'
Assert-That (Update-ModelConfig $c $catalog['2b'] $true) '-Model changes model_file'
Assert-That ($c.model_file -eq 'models/plamo-3-translate-2b-Q4_K_M.gguf' -and $c.keep -eq 1) 'model_file switched, other keys kept'
Assert-That (-not (Update-ModelConfig $c $catalog['2b'] $true)) 'asking for the model already set changes nothing'
$empty = [pscustomobject]@{}
Assert-That (Update-ModelConfig $empty $catalog['0.8b'] $true) 'a config without model_file gets one'
Assert-That ($empty.model_file -eq 'models/plamo-3-translate-0.8b-Q4_K_M.gguf') 'model_file added'
`)
}

// TestSetupWholeScriptWhenInstalled runs the real script in a folder where the
// model and all backends are already installed: it must not need the network
// (no GitHub query) and must leave the user's engine settings alone.
func TestSetupWholeScriptWhenInstalled(t *testing.T) {
	exe := powershellExe(t)
	script, err := os.ReadFile("setup.ps1")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("setup.ps1", string(script))
	write("models/chat_template.jinja", strings.Repeat("x", 200))
	for _, b := range []string{"cpu", "cuda", "vulkan"} {
		write("runtime/"+b+"/llama-server.exe", "exe")
	}
	write("runtime/cuda/cudart64_12.dll", "dll")
	write("runtime/cuda/cublas64_12.dll", "dll")
	// A model just over 1 GB; NTFS does not write the zeros.
	f, err := os.Create(filepath.Join(dir, "models", "plamo-3-translate-2b-Q4_K_M.gguf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1<<30 + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	const userConfig = `{
  "hotkey": "Ctrl+Alt+E",
  "gpu_layers": 7,
  "server_file": "runtime/cpu/llama-server.exe",
  "fallback_server_file": "runtime/cpu/llama-server.exe",
  "custom_key": "keep me"
}`
	configPath := filepath.Join(dir, "config.json")
	run := func(backend string, extra ...string) string {
		t.Helper()
		args := append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(dir, "setup.ps1"), "-Backend", backend}, extra...)
		cmd := exec.Command(exe, args...)
		cmd.Env = psEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("setup.ps1 -Backend %s failed: %v\n%s", backend, err, out)
		}
		if strings.Contains(string(out), "Discovering") || strings.Contains(string(out), "Downloading") {
			t.Fatalf("setup.ps1 -Backend %s went to the network although everything is installed:\n%s", backend, out)
		}
		return string(out)
	}

	// auto: whatever GPU the test machine has, every backend is installed, so nothing is needed.
	if err := os.WriteFile(configPath, []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	run("auto")
	if got, _ := os.ReadFile(configPath); string(got) != userConfig {
		t.Fatalf("-Backend auto rewrote the user's config:\n%s", got)
	}

	// An explicit backend is an explicit reconfiguration.
	run("cuda")
	raw, _ := os.ReadFile(configPath)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("config.json is not valid JSON after setup: %v\n%s", err, raw)
	}
	if got["server_file"] != "runtime/cuda/llama-server.exe" || got["gpu_layers"] != float64(99) || got["custom_key"] != "keep me" {
		t.Errorf("config after -Backend cuda is wrong:\n%s", raw)
	}
	if _, has := got["model_file"]; has {
		t.Errorf("model_file was written without -Model:\n%s", raw)
	}

	// -Model switches model_file to a model that is already on disk, offline.
	f08, err := os.Create(filepath.Join(dir, "models", "plamo-3-translate-0.8b-Q4_K_M.gguf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f08.Truncate(400<<20 + 1); err != nil {
		f08.Close()
		t.Fatal(err)
	}
	f08.Close()
	out := run("auto", "-Model", "0.8b")
	if !strings.Contains(out, "PLaMo 3 Translate 0.8B") {
		t.Errorf("setup does not say which model it uses:\n%s", out)
	}
	raw, _ = os.ReadFile(configPath)
	got = map[string]any{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("config.json is not valid JSON after -Model: %v\n%s", err, raw)
	}
	if got["model_file"] != "models/plamo-3-translate-0.8b-Q4_K_M.gguf" || got["custom_key"] != "keep me" || got["server_file"] != "runtime/cuda/llama-server.exe" {
		t.Errorf("config after -Model 0.8b is wrong:\n%s", raw)
	}

	// A later plain run (an update) keeps the chosen model and does not rewrite the file.
	before, _ := os.ReadFile(configPath)
	out = run("auto")
	if !strings.Contains(out, "0.8B") {
		t.Errorf("an update must keep using the configured model:\n%s", out)
	}
	if after, _ := os.ReadFile(configPath); string(after) != string(before) {
		t.Errorf("a plain run rewrote the config:\n%s", after)
	}

	// A model that is too small for its size is refused, not used.
	if err := os.Truncate(filepath.Join(dir, "models", "plamo-3-translate-0.8b-Q4_K_M.gguf"), 1<<20); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(dir, "setup.ps1"), "-Backend", "auto", "-Model", "0.8b")
	cmd.Env = psEnv()
	if o, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(o), "incomplete") {
		t.Errorf("a truncated model must be rejected: %v\n%s", err, o)
	}
}

func TestGetFileSha256OnBothPowerShells(t *testing.T) {
	dir := t.TempDir()
	emptyPath := filepath.Join(dir, "empty.bin")
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	largeData := make([]byte, 1500000)
	for i := range largeData {
		largeData[i] = byte((i*31 + 7) % 256)
	}
	largePath := filepath.Join(dir, "large.bin")
	if err := os.WriteFile(largePath, largeData, 0o644); err != nil {
		t.Fatal(err)
	}

	emptySum := sha256.Sum256(nil)
	wantEmpty := strings.ToUpper(hex.EncodeToString(emptySum[:]))
	largeSum := sha256.Sum256(largeData)
	wantLarge := strings.ToUpper(hex.EncodeToString(largeSum[:]))

	psScript := `
$ast = [System.Management.Automation.Language.Parser]::ParseFile($env:CT_TARGET_PS1, [ref]$null, [ref]$null)
$func = $ast.Find({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Get-FileSha256' }, $true)
if ($null -eq $func) { throw "Get-FileSha256 not found in $env:CT_TARGET_PS1" }
. ([scriptblock]::Create($func.Extent.Text))
Write-Output (Get-FileSha256 $env:CT_FILE_EMPTY)
Write-Output (Get-FileSha256 $env:CT_FILE_LARGE)
`
	scripts := []string{"setup.ps1", "install.ps1"}
	shells := []string{"powershell.exe", "pwsh.exe"}

	for _, scriptName := range scripts {
		scriptPath, err := filepath.Abs(scriptName)
		if err != nil {
			t.Fatal(err)
		}
		for _, sh := range shells {
			exe, err := exec.LookPath(sh)
			if err != nil {
				continue
			}
			t.Run(scriptName+"/"+sh, func(t *testing.T) {
				cmd := exec.Command(exe, "-NoProfile", "-Command", psScript)
				cmd.Env = append(psEnv(),
					"CT_TARGET_PS1="+scriptPath,
					"CT_FILE_EMPTY="+emptyPath,
					"CT_FILE_LARGE="+largePath,
				)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s failed: %v\n%s", sh, err, out)
				}
				lines := strings.Fields(string(out))
				if len(lines) < 2 {
					t.Fatalf("expected at least 2 output lines, got:\n%s", out)
				}
				if lines[0] != wantEmpty {
					t.Errorf("empty file: got %q, want %q", lines[0], wantEmpty)
				}
				if lines[1] != wantLarge {
					t.Errorf("large file: got %q, want %q", lines[1], wantLarge)
				}
			})
		}
	}

	// Verify Get-FileSha256 survives even when powershell.exe runs under pwsh's contaminated PSModulePath.
	if pwshExe, err := exec.LookPath("pwsh.exe"); err == nil {
		if winPSExe, err := exec.LookPath("powershell.exe"); err == nil {
			t.Run("powershell.exe with pwsh contaminated PSModulePath", func(t *testing.T) {
				// Get pwsh's module path
				cmdPwsh := exec.Command(pwshExe, "-NoProfile", "-Command", "Write-Output $env:PSModulePath")
				outPwsh, err := cmdPwsh.Output()
				if err != nil {
					t.Fatalf("getting pwsh PSModulePath: %v", err)
				}
				pwshModPath := strings.TrimSpace(string(outPwsh))

				setupPath, err := filepath.Abs("setup.ps1")
				if err != nil {
					t.Fatal(err)
				}

				cmd := exec.Command(winPSExe, "-NoProfile", "-Command", psScript)
				cmd.Env = append(cleanPowerShellEnv(os.Environ()),
					"PSModulePath="+pwshModPath,
					"CT_TARGET_PS1="+setupPath,
					"CT_FILE_EMPTY="+emptyPath,
					"CT_FILE_LARGE="+largePath,
				)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("Get-FileSha256 failed under contaminated PSModulePath: %v\n%s", err, out)
				}
				lines := strings.Fields(string(out))
				if len(lines) < 2 {
					t.Fatalf("expected at least 2 output lines, got:\n%s", out)
				}
				if lines[0] != wantEmpty || lines[1] != wantLarge {
					t.Errorf("got %v, want [%s %s]", lines, wantEmpty, wantLarge)
				}
			})

			t.Run("Get-CleanWindowsPowerShellModulePath from pwsh", func(t *testing.T) {
				installPath, err := filepath.Abs("install.ps1")
				if err != nil {
					t.Fatal(err)
				}
				testScript := `
$ast = [System.Management.Automation.Language.Parser]::ParseFile($env:CT_TARGET_PS1, [ref]$null, [ref]$null)
$func = $ast.Find({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Get-CleanWindowsPowerShellModulePath' }, $true)
if ($null -eq $func) { throw 'Get-CleanWindowsPowerShellModulePath not found' }
. ([scriptblock]::Create($func.Extent.Text))
$clean = Get-CleanWindowsPowerShellModulePath
Write-Output "CLEAN_PATH=$clean"
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = 'powershell.exe'
$psi.Arguments = '-NoProfile -Command "(Get-Command Get-FileHash).Name"'
$psi.UseShellExecute = $false
$psi.RedirectStandardOutput = $true
$psi.EnvironmentVariables['PSModulePath'] = $clean
$p = [System.Diagnostics.Process]::Start($psi)
$out = $p.StandardOutput.ReadToEnd().Trim()
$p.WaitForExit()
Write-Output "CHILD_RESULT=$out"
`
				cmd := exec.Command(pwshExe, "-NoProfile", "-Command", testScript)
				cmd.Env = append(os.Environ(), "CT_TARGET_PS1="+installPath)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("clean module path test failed: %v\n%s", err, out)
				}
				outStr := string(out)
				if !strings.Contains(outStr, "CHILD_RESULT=Get-FileHash") {
					t.Errorf("child powershell.exe did not find Get-FileHash with clean PSModulePath:\n%s", outStr)
				}
				if strings.Contains(outStr, "PowerShell\\7") {
					t.Errorf("clean PSModulePath must not contain PowerShell 7:\n%s", outStr)
				}
			})
		}
	}
}
