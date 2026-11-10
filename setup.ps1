param(
    [ValidateSet('auto','cuda','vulkan','cpu')][string]$Backend = 'auto',
    # Which PLaMo 3 Translate size to install. Without -Model the model named in config.json
    # is kept (2b on a fresh install); with it, config.json is switched to that model.
    [ValidateSet('0.8b','2b','8b')][string]$Model = '2b'
)
# Portable Clipboard Translator setup. Downloads only from official PFN Hugging Face
# and official ggml-org GitHub release assets. Windows 10/11 x64.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
# Only an explicit -Backend cuda/vulkan/cpu may rewrite the engine settings of an existing setup.
$explicitBackend = $PSBoundParameters.ContainsKey('Backend') -and $PSBoundParameters['Backend'] -ne 'auto'
$explicitModel = $PSBoundParameters.ContainsKey('Model')
$root = $PSScriptRoot
$runtime = Join-Path $root 'runtime'
$models = Join-Path $root 'models'

# The models setup can install. Each file is pinned to one Hugging Face commit and one
# SHA-256 (the model and the chat template), so a moved "main" or a corrupted download cannot
# slip through. To update a model: bump its Revision, Sha256 and TemplateSha256 together.
# MinBytes only guards files that are already on disk (they may come from an older revision).
# Only Q4_K_M is offered; the 31B model is not (about 20 GB).
function Get-ModelCatalog {
    return [ordered]@{
        '0.8b' = @{ Repo = 'plamo-3-translate-0.8b-GGUF'; File = 'plamo-3-translate-0.8b-Q4_K_M.gguf'; Size = '0.6 GB'; MinBytes = 400MB
            Revision = '0c4af2b003fdd94b6868aa955414c1c86ee96997'
            Sha256 = '73a1d942d8ab1e654d83898d997b8b4808c32ba669b378df612230989d18f079'
            TemplateSha256 = '084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e' }
        '2b' = @{ Repo = 'plamo-3-translate-2b-GGUF'; File = 'plamo-3-translate-2b-Q4_K_M.gguf'; Size = '1.8 GB'; MinBytes = 1GB
            Revision = '228a27cc0bb4f111219963848d50a3127c16ca73'
            Sha256 = '1ffd9073afcc70e35ccc942428aa2b73d7aa2b9ea779bf504b775369e0276c13'
            TemplateSha256 = 'ad7eb87b161c6792be070d777a9f9da50c9e57a77e1fa5cca3a24b91b6bdb9e9' }
        '8b' = @{ Repo = 'plamo-3-translate-8b-GGUF'; File = 'plamo-3-translate-8b-Q4_K_M.gguf'; Size = '5.3 GB'; MinBytes = 4GB
            Revision = '407c15bc44f6968e6734bc047d92f766c163bbec'
            Sha256 = 'e021fb83dcda393a4f866291caa53380eebbe2f25be4c7585fb19e8486828bbf'
            TemplateSha256 = '084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e' }
    }
}

# The catalog key to install: the one asked for with -Model, else the model config.json
# already names (so an update never swaps a chosen model for another), else 2b.
function Resolve-ModelKey($catalog, $config, [bool]$explicit, [string]$requested) {
    if ($explicit) { return $requested.ToLowerInvariant() }
    $configured = Get-ConfigValue $config 'model_file'
    if ($configured -is [string] -and $configured) {
        $leaf = ($configured -split '[\\/]')[-1]
        foreach ($key in $catalog.Keys) {
            if ($catalog[$key].File -ieq $leaf) { return $key }
        }
    }
    return '2b'
}

function Test-CustomTemplate([string]$path) {
    if ([string]::IsNullOrWhiteSpace($path)) { return $false }
    $norm = ($path -replace '\\', '/') -replace '^\./', ''
    $leaf = Split-Path -Leaf $norm
    $dir = Split-Path -Parent $norm
    if ($dir -ne '' -and $dir -ne 'models' -and $dir -ne '.') { return $true }
    if ($leaf -ieq 'chat_template.jinja') { return $false }
    if ($leaf -match '^chat_template\.(0\.8b|2b|8b)\.jinja$') { return $false }
    return $true
}

function Get-ModelKeyFromEntry($entry) {
    if ($entry -is [System.Collections.IDictionary]) {
        if ($entry.Contains('Key')) { return $entry['Key'] }
        if ($entry.Contains('File') -and $entry['File'] -match 'plamo-3-translate-(0\.8b|2b|8b)-') { return $Matches[1] }
    } elseif ($null -ne $entry.PSObject.Properties['File']) {
        if ($entry.File -match 'plamo-3-translate-(0\.8b|2b|8b)-') { return $Matches[1] }
    }
    return '2b'
}

# Only an explicit -Model rewrites model_file. Returns $true when the config changed.
function Update-ModelConfig($config, $entry, [bool]$explicit) {
    if (-not $explicit) { return $false }
    $file = if ($entry -is [System.Collections.IDictionary]) { $entry['File'] } else { $entry.File }
    $changed = Set-ConfigValue $config 'model_file' ('models/' + $file)
    $key = Get-ModelKeyFromEntry $entry
    $curTpl = Get-ConfigValue $config 'template_file'
    if (-not (Test-CustomTemplate $curTpl)) {
        $wantTpl = 'models/chat_template.' + $key + '.jinja'
        if (Set-ConfigValue $config 'template_file' $wantTpl) { $changed = $true }
    }
    return $changed
}

function Download-File([string]$url, [string]$dest, [string]$sha256 = '') {
    Write-Host "Downloading: $url"
    $partial = "$dest.partial"
    if (Test-Path $partial) { Remove-Item $partial -Force }
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $partial -Headers @{ 'User-Agent' = 'ClipboardTranslatorSetup' } -MaximumRedirection 10
        if ((Get-Item $partial).Length -eq 0) { throw "Download empty: $url" }
        # Verify before the file gets its final name: a bad file left at $dest would be skipped on every later run.
        if ($sha256) { Assert-FileSha256 $partial $sha256 (Split-Path -Leaf $dest) }
        Move-Item -Force -Path $partial -Destination $dest
    } catch {
        Remove-Item $partial -Force -ErrorAction SilentlyContinue
        throw
    }
}

function Get-FileSha256([string]$file) {
    $fullPath = if (Test-Path -LiteralPath $file) { (Convert-Path -LiteralPath $file) } else { $file }
    $hasher = [System.Security.Cryptography.SHA256]::Create()
    $stream = [System.IO.File]::OpenRead($fullPath)
    try {
        $hashBytes = $hasher.ComputeHash($stream)
        return [System.BitConverter]::ToString($hashBytes).Replace('-', '')
    } finally {
        $stream.Dispose()
        $hasher.Dispose()
    }
}

function Assert-FileSha256([string]$file, [string]$expected, [string]$label) {
    $actual = Get-FileSha256 $file
    if ($actual -ne $expected) { throw "SHA256 checksum mismatch for $label (expected $expected, got $actual)" }
}

# The SHA-256 GitHub publishes for a release asset ("digest": "sha256:..."), or $null.
function Get-AssetSha256($asset) {
    if ($null -ne $asset -and $asset.PSObject.Properties.Name -contains 'digest' -and "$($asset.digest)" -match '^sha256:([0-9a-fA-F]{64})$') { return $Matches[1] }
    return $null
}

# Fails closed: an asset without a pinned or published SHA-256 is never installed.
function Assert-Hash($asset, [string]$file) {
    $expected = if ($null -ne $asset.PSObject.Properties['sha256'] -and $asset.sha256) { $asset.sha256 } else { Get-AssetSha256 $asset }
    if ($null -eq $expected) { throw "No SHA256 digest is published for $($asset.name); refusing to install an unverified download." }
    Assert-FileSha256 $file $expected $asset.name
}

# A backend is complete when its llama-server.exe is there and, for CUDA, so are the CUDA runtime DLLs it loads.
function Test-BackendComplete([string]$name, [string]$dir) {
    if (-not (Test-Path -LiteralPath (Join-Path $dir 'llama-server.exe') -PathType Leaf)) { return $false }
    if ($name -eq 'cuda') {
        foreach ($pattern in @('cudart64_*.dll', 'cublas64_*.dll')) {
            if (@(Get-ChildItem -LiteralPath $dir -File -Filter $pattern -ErrorAction SilentlyContinue).Count -eq 0) { return $false }
        }
    }
    return $true
}

# Downloads one release zip, checks its SHA-256 and copies the wanted files flat into $stage.
function Add-AssetFiles($asset, [string]$tmp, [string]$stage, [bool]$onlyDlls) {
    $id = [guid]::NewGuid().ToString('N')
    $zip = Join-Path $tmp "$id.zip"
    $url = if ($null -ne $asset.PSObject.Properties['url'] -and $asset.url) { $asset.url } else { $asset.browser_download_url }
    Download-File $url $zip
    Assert-Hash $asset $zip
    $unpacked = Join-Path $tmp $id
    Expand-Archive -Path $zip -DestinationPath $unpacked
    if ($onlyDlls) {
        $files = @(Get-ChildItem -Path $unpacked -Recurse -File -Filter '*.dll')
        if ($files.Count -eq 0) { throw "CUDA DLL package contains no DLL files: $($asset.name)" }
    } else {
        $server = Get-ChildItem -Path $unpacked -Recurse -File -Filter 'llama-server.exe' | Select-Object -First 1
        if ($null -eq $server) { throw "llama-server.exe not found in $($asset.name)" }
        $files = @(Get-ChildItem -Path $server.DirectoryName -File)
    }
    foreach ($file in $files) { Copy-Item -Force -LiteralPath $file.FullName -Destination (Join-Path $stage $file.Name) }
}

# Installs runtime\<name> all or nothing: everything (including the CUDA DLLs) is downloaded
# and unpacked into a staging folder first and only then renamed into place. A failure
# leaves no half-installed backend that the tray menu would offer but could not start.
# $dllAsset is the extra CUDA runtime package (cuda only), otherwise $null.
function Install-Backend([string]$runtimeDir, [string]$name, $serverAsset, $dllAsset) {
    $target = Join-Path $runtimeDir $name
    New-Item -ItemType Directory -Force -Path $runtimeDir | Out-Null
    # Inside runtime\ (not %TEMP%) so the final rename stays on one volume.
    $stage = Join-Path $runtimeDir ('.staging-' + $name + '-' + [guid]::NewGuid().ToString('N'))
    $tmp = Join-Path $env:TEMP ('PortableTranslator-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $stage, $tmp | Out-Null
    try {
        Add-AssetFiles $serverAsset $tmp $stage $false
        if ($null -ne $dllAsset) { Add-AssetFiles $dllAsset $tmp $stage $true }
        if (-not (Test-BackendComplete $name $stage)) { throw "The $name package is incomplete (llama-server.exe or the CUDA runtime DLLs are missing)." }
        # Only an incomplete backend gets here (a complete one is skipped), so replacing it loses nothing.
        if (Test-Path -LiteralPath $target) { Remove-Item -Recurse -Force -LiteralPath $target }
        Move-Item -LiteralPath $stage -Destination $target
    } catch {
        # Do not leave an incomplete backend of an earlier run on disk: availableBackends in the app only checks for llama-server.exe.
        if ((Test-Path -LiteralPath $target) -and -not (Test-BackendComplete $name $target)) { Remove-Item -Recurse -Force -LiteralPath $target -ErrorAction SilentlyContinue }
        throw
    } finally {
        Remove-Item -Recurse -Force -LiteralPath $stage, $tmp -ErrorAction SilentlyContinue
    }
}

# --- llama.cpp pinned release (managed by tools/bump_llama_pin.py) ---
function Get-LlamaRelease {
    return [ordered]@{
        Tag = 'b12032'
        Assets = [ordered]@{
            'cpu' = [ordered]@{
                File   = 'llama-b12032-bin-win-cpu-x64.zip'
                Sha256 = '5296f5d18bb7d422c7c26f4bb9bc796d9d4f41e1312a8c296cff475bdc60d0d4'
            }
            'cuda' = [ordered]@{
                File   = 'llama-b12032-bin-win-cuda-12.4-x64.zip'
                Sha256 = '36c7e24ad048cceabf00c1b49d0f0411b954c6a410a7ff4300af8721a94fd00a'
            }
            'cudart' = [ordered]@{
                File   = 'cudart-llama-bin-win-cuda-12.4-x64.zip'
                Sha256 = '8c79a9b226de4b3cacfd1f83d24f962d0773be79f1e7b75c6af4ded7e32ae1d6'
            }
            'vulkan' = [ordered]@{
                File   = 'llama-b12032-bin-win-vulkan-x64.zip'
                Sha256 = '19a8bbf269fa683ae7bc04f886c9c2a93e0655c2606f37d2b60069bf9e8bcc32'
            }
        }
    }
}
# --- end llama.cpp pinned release ---

# --- config.json -----------------------------------------------------------------------

function Get-ConfigValue($config, [string]$name) {
    $p = $config.PSObject.Properties[$name]
    if ($p) { return $p.Value }
    return $null
}

# Sets a value; returns $true when the config changed.
function Set-ConfigValue($config, [string]$name, $value) {
    $p = $config.PSObject.Properties[$name]
    if ($p) {
        if ($p.Value -eq $value) { return $false }
        $config.$name = $value
        return $true
    }
    $config | Add-Member -NotePropertyName $name -NotePropertyValue $value
    return $true
}

# True when the config names a llama-server file inside the app folder that exists.
function Test-ConfiguredServer($config, [string]$appRoot, [string]$key) {
    $v = Get-ConfigValue $config $key
    if ($v -isnot [string] -or [string]::IsNullOrWhiteSpace($v)) { return $false }
    if ([IO.Path]::IsPathRooted($v) -or (@($v -split '[\\/]') -contains '..')) { return $false }
    return (Test-Path -LiteralPath (Join-Path $appRoot $v) -PathType Leaf)
}

# Decides what setup writes to config.json. The engine the user already runs (server_file,
# gpu_layers) is left alone on re-runs and updates (Scoop runs this on every update),
# unless one of these holds:
#   - -Backend was given explicitly,
#   - the configured server_file did not exist when setup started (fresh install; the
#     shipped config.json points at the CPU build) or does not exist any more,
#   - $backend was not complete when setup started and is now (a GPU build that failed
#     earlier was installed on a retry, or the CPU fallback was).
# Returns $true when the config changed.
function Update-BackendConfig($config, [string]$appRoot, [string]$backend, [bool]$explicit, [bool]$serverPresentAtStart, [bool]$backendCompleteAtStart) {
    $changed = $false
    $write = $explicit -or -not $serverPresentAtStart -or -not (Test-ConfiguredServer $config $appRoot 'server_file') -or -not $backendCompleteAtStart
    if ($write) {
        if (Set-ConfigValue $config 'server_file' "runtime/$backend/llama-server.exe") { $changed = $true }
        if (Set-ConfigValue $config 'gpu_layers' $(if ($backend -eq 'cpu') { 0 } else { 99 })) { $changed = $true }
        if (Set-ConfigValue $config 'fallback_server_file' 'runtime/cpu/llama-server.exe') { $changed = $true }
        return $changed
    }
    if (-not (Test-ConfiguredServer $config $appRoot 'fallback_server_file')) {
        if (Set-ConfigValue $config 'fallback_server_file' 'runtime/cpu/llama-server.exe') { $changed = $true }
    }
    if ($null -eq (Get-ConfigValue $config 'gpu_layers')) {
        $isCpu = ([string](Get-ConfigValue $config 'server_file')) -match '^runtime[\\/]cpu[\\/]'
        if (Set-ConfigValue $config 'gpu_layers' $(if ($isCpu) { 0 } else { 99 })) { $changed = $true }
    }
    return $changed
}

# --- main ------------------------------------------------------------------------------
# Dot-sourcing is not supported; the tests load the functions above from this file's AST.

New-Item -ItemType Directory -Force -Path $runtime, $models | Out-Null

# Read config.json first: a damaged file should fail now, not after a 1.8 GB download.
# A bare exe (for example from winget) has no config.json yet; the app fills in defaults for the rest.
$configFile = Join-Path $root 'config.json'
$config = if (Test-Path -LiteralPath $configFile) { Get-Content -Raw -Encoding UTF8 $configFile | ConvertFrom-Json } else { [pscustomobject]@{} }
$serverPresentAtStart = Test-ConfiguredServer $config $root 'server_file'
$completeAtStart = @{}
foreach ($b in @('cpu', 'cuda', 'vulkan')) { $completeAtStart[$b] = Test-BackendComplete $b (Join-Path $runtime $b) }

# Files already on disk are only length-checked: they may come from an older model revision,
# and re-downloading 1.8 GB on every update would be worse. New downloads must match the pinned SHA-256.
$catalog = Get-ModelCatalog
$modelKey = Resolve-ModelKey $catalog $config $explicitModel $Model
$modelInfo = $catalog[$modelKey]
Write-Host "Model: PLaMo 3 Translate $($modelKey.ToUpperInvariant()) ($($modelInfo.Size))"
$modelFile = Join-Path $models $modelInfo.File
$templateFile = Join-Path $models "chat_template.$modelKey.jinja"
$legacyTemplate = Join-Path $models 'chat_template.jinja'
$hf = "https://huggingface.co/pfnet/$($modelInfo.Repo)/resolve/$($modelInfo.Revision)/"
if (-not (Test-Path $modelFile)) { Download-File ($hf + $modelInfo.File) $modelFile $modelInfo.Sha256 }
if ((Get-Item $modelFile).Length -lt $modelInfo.MinBytes) { throw "Model appears incomplete (smaller than expected for the $modelKey model). Remove the damaged GGUF and run Setup.cmd again." }
$needTemplate = $true
if (Test-Path $templateFile) {
    if ((Get-FileSha256 $templateFile) -eq $modelInfo.TemplateSha256) {
        $needTemplate = $false
    }
} elseif (Test-Path $legacyTemplate) {
    if ((Get-FileSha256 $legacyTemplate) -eq $modelInfo.TemplateSha256) {
        Copy-Item -LiteralPath $legacyTemplate -Destination $templateFile
        $needTemplate = $false
    }
}
if ($needTemplate) {
    Download-File ($hf + 'chat_template.jinja') $templateFile $modelInfo.TemplateSha256
}
if ((Get-Item $templateFile).Length -lt 100) { throw 'Chat template download appears incomplete.' }

if ($Backend -eq 'auto') {
    $names = @()
    try { $names = @(Get-CimInstance Win32_VideoController -ErrorAction Stop | ForEach-Object { $_.Name }) }
    catch { Write-Warning 'GPU detection failed, using CPU. Run setup.ps1 -Backend cuda/vulkan to select manually.' }
    $names = @($names | Where-Object { $_ })
    Write-Host "Detected display devices: $($names -join '; ')"
    if (@($names | Where-Object { $_ -match 'NVIDIA|GeForce|RTX|Quadro' }).Count -gt 0) { $Backend = 'cuda' }
    elseif (@($names | Where-Object { $_ -match 'AMD|Radeon|Intel|Arc' }).Count -gt 0) { $Backend = 'vulkan' }
    else { $Backend = 'cpu' }
}
Write-Host "Requested backend: $Backend"

$needCpu = -not $completeAtStart['cpu']
$needGpu = ($Backend -ne 'cpu') -and -not $completeAtStart[$Backend]
$installedBackend = $Backend
if (-not $needCpu -and -not $needGpu) {
    # Nothing to download: skip downloading so an offline run still succeeds.
    Write-Host "llama.cpp is already installed (cpu$(if ($Backend -ne 'cpu') { ", $Backend" }))."
} else {
    $llama = Get-LlamaRelease
    $tag = $llama.Tag
    Write-Host "Installing pinned llama.cpp release: $tag"
    $baseUrl = "https://github.com/ggml-org/llama.cpp/releases/download/$tag"
    function New-PinnedAsset($entry) {
        return [pscustomobject]@{
            name   = $entry.File
            url    = "$baseUrl/$($entry.File)"
            sha256 = $entry.Sha256
        }
    }
    if ($needCpu) {
        Install-Backend $runtime 'cpu' (New-PinnedAsset $llama.Assets['cpu']) $null
    }
    if ($needGpu) {
        $gpuAsset = New-PinnedAsset $llama.Assets[$Backend]
        $dllAsset = if ($Backend -eq 'cuda') { New-PinnedAsset $llama.Assets['cudart'] } else { $null }
        try {
            Install-Backend $runtime $Backend $gpuAsset $dllAsset
        } catch {
            Write-Warning "Could not install ${Backend}: $($_.Exception.Message)"
            Write-Warning 'Using CPU fallback instead. Run Setup.cmd later to retry GPU installation.'
            $installedBackend = 'cpu'
        }
    }
}

# Modify only relevant properties; retain user's other settings/hotkey.
$changed = Update-BackendConfig $config $root $installedBackend $explicitBackend $serverPresentAtStart $completeAtStart[$installedBackend]
if (Update-ModelConfig $config $modelInfo $explicitModel) { $changed = $true }
if (-not (Test-Path -LiteralPath $configFile)) {
    if ($null -eq (Get-ConfigValue $config 'model_file')) { [void](Set-ConfigValue $config 'model_file' ('models/' + $modelInfo['File'])) }
    if ($null -eq (Get-ConfigValue $config 'template_file')) { [void](Set-ConfigValue $config 'template_file' ('models/chat_template.' + $modelKey + '.jinja')) }
}
if ($changed -or -not (Test-Path -LiteralPath $configFile)) {
    $json = $config | ConvertTo-Json -Depth 8
    [System.IO.File]::WriteAllText($configFile, $json, (New-Object System.Text.UTF8Encoding($false)))
} else {
    Write-Host 'Kept your existing engine settings (server_file, gpu_layers) in config.json.'
}
# llama.cpp's Windows builds import vcruntime140_1.dll. Without the Visual C++ runtime llama-server
# exits at once (0xC0000135) and prints nothing, so say so now, while the user is looking at this window.
if (-not (Test-Path -LiteralPath (Join-Path ([Environment]::GetFolderPath('System')) 'vcruntime140_1.dll'))) {
    Write-Warning 'The Visual C++ Redistributable (x64) looks missing or too old; llama-server will not start without it.'
    Write-Warning 'Install it with:  winget install --id Microsoft.VCRedist.2015+.x64 -e   (or https://aka.ms/vs/17/release/vc_redist.x64.exe)'
}
Write-Host ''
Write-Host "Setup finished. Engine in config.json: $(Get-ConfigValue $config 'server_file'); CPU fallback installed."
Write-Host "Now start ClipboardTranslator.exe (or Start.cmd). The full folder can be copied to another Windows x64 PC."
Write-Host 'A different GPU or driver on the destination may trigger CPU fallback.'
Write-Host "PLaMo terms: https://huggingface.co/pfnet/$($modelInfo.Repo)"
