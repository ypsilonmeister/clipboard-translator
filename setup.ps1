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
            Revision = '232fbd7916519edecddfdae0beae7e76fe70453f'
            Sha256 = 'f36d0813dd95573a852ab5d76e374c543632d9e292929e922bcdbf7e53b494e0'
            TemplateSha256 = '18dcc169548d794897084a04cd150a5a633717f1eb6e62de4a0e0973b377e02a' }
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

# Only an explicit -Model rewrites model_file. Returns $true when the config changed.
function Update-ModelConfig($config, $entry, [bool]$explicit) {
    if (-not $explicit) { return $false }
    return (Set-ConfigValue $config 'model_file' ('models/' + $entry.File))
}

function Download-File([string]$url, [string]$dest, [string]$sha256 = '') {
    Write-Host "Downloading: $url"
    $partial = "$dest.partial"
    if (Test-Path $partial) { Remove-Item $partial -Force }
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $partial -Headers @{ 'User-Agent' = 'PortableClipboardTranslator/0.3.2' } -MaximumRedirection 10
        if ((Get-Item $partial).Length -eq 0) { throw "Download empty: $url" }
        # Verify before the file gets its final name: a bad file left at $dest would be skipped on every later run.
        if ($sha256) { Assert-FileSha256 $partial $sha256 (Split-Path -Leaf $dest) }
        Move-Item -Force -Path $partial -Destination $dest
    } catch {
        Remove-Item $partial -Force -ErrorAction SilentlyContinue
        throw
    }
}

function Assert-FileSha256([string]$file, [string]$expected, [string]$label) {
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $file).Hash
    if ($actual -ne $expected) { throw "SHA256 checksum mismatch for $label (expected $expected, got $actual)" }
}

# The SHA-256 GitHub publishes for a release asset ("digest": "sha256:..."), or $null.
function Get-AssetSha256($asset) {
    if ($null -ne $asset -and $asset.PSObject.Properties.Name -contains 'digest' -and "$($asset.digest)" -match '^sha256:([0-9a-fA-F]{64})$') { return $Matches[1] }
    return $null
}

# Fails closed: an asset without a published SHA-256 is never installed.
function Assert-Hash($asset, [string]$file) {
    $expected = Get-AssetSha256 $asset
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
    Download-File $asset.browser_download_url $zip
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

# /releases/latest points at stable tags that may NOT contain Windows binaries.
# Use recent official releases, select one with a matching Windows CPU asset (when
# $needCpu) and the requested GPU asset. Do not construct or guess download filenames.
# Only releases whose assets publish a SHA-256 digest qualify; returns $null if there is none.
function Find-LlamaRelease($releases, [string]$backend, [bool]$needCpu) {
    foreach ($release in $releases) {
        if ($null -eq $release.assets) { continue }
        $cpu = $null
        $gpu = $null
        $dll = $null
        if ($needCpu) {
            $cpu = $release.assets | Where-Object { $_.name -match '^llama-.*-bin-win-cpu-x64\.zip$' -and $null -ne (Get-AssetSha256 $_) } | Select-Object -First 1
            if ($null -eq $cpu) { continue }
        }
        if ($backend -eq 'cuda') {
            $gpu = $release.assets | Where-Object { $_.name -match '^llama-.*-bin-win-cuda-12\.4-x64\.zip$' -and $null -ne (Get-AssetSha256 $_) } | Select-Object -First 1
            $dll = $release.assets | Where-Object { $_.name -match '^cudart-llama.*-bin-win-cuda-12\.4-x64\.zip$' -and $null -ne (Get-AssetSha256 $_) } | Select-Object -First 1
            if ($null -eq $gpu -or $null -eq $dll) { continue }
        } elseif ($backend -eq 'vulkan') {
            $gpu = $release.assets | Where-Object { $_.name -match '^llama-.*-bin-win-vulkan-x64\.zip$' -and $null -ne (Get-AssetSha256 $_) } | Select-Object -First 1
            if ($null -eq $gpu) { continue }
        }
        return [pscustomobject]@{ Release = $release; Cpu = $cpu; Gpu = $gpu; CudaDll = $dll }
    }
    return $null
}

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
$templateFile = Join-Path $models 'chat_template.jinja'
$hf = "https://huggingface.co/pfnet/$($modelInfo.Repo)/resolve/$($modelInfo.Revision)/"
if (-not (Test-Path $modelFile)) { Download-File ($hf + $modelInfo.File) $modelFile $modelInfo.Sha256 }
if ((Get-Item $modelFile).Length -lt $modelInfo.MinBytes) { throw "Model appears incomplete (smaller than expected for the $modelKey model). Remove the damaged GGUF and run Setup.cmd again." }
# The template is shared by all sizes (the app only passes source_lang / target_lang, which
# every revision handles alike), so one that is already in models\ is kept.
if (-not (Test-Path $templateFile)) { Download-File ($hf + 'chat_template.jinja') $templateFile $modelInfo.TemplateSha256 }
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
    # Nothing to download: skip the GitHub query so an offline or rate-limited run still succeeds.
    Write-Host "llama.cpp is already installed (cpu$(if ($Backend -ne 'cpu') { ", $Backend" }))."
} else {
    Write-Host 'Discovering official llama.cpp Windows builds...'
    $releases = @(Invoke-RestMethod -Uri 'https://api.github.com/repos/ggml-org/llama.cpp/releases?per_page=30' -Headers @{ 'User-Agent' = 'PortableClipboardTranslator/0.3.2'; 'Accept' = 'application/vnd.github+json' })
    $found = Find-LlamaRelease $releases $Backend $needCpu
    if ($null -eq $found) {
        throw 'No matching official llama.cpp build (with a published SHA256 digest) found in 30 recent GitHub releases. See https://github.com/ggml-org/llama.cpp/releases'
    }
    Write-Host "Selected llama.cpp release: $($found.Release.tag_name)"
    if ($needCpu) { Install-Backend $runtime 'cpu' $found.Cpu $null }
    if ($needGpu) {
        try {
            Install-Backend $runtime $Backend $found.Gpu $found.CudaDll
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
