param([ValidateSet('auto','cuda','vulkan','cpu')][string]$Backend = 'auto')
# Portable Clipboard Translator setup. Downloads only from official PFN Hugging Face
# and official ggml-org GitHub release assets. Windows 10/11 x64.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$root = $PSScriptRoot
$runtime = Join-Path $root 'runtime'
$models = Join-Path $root 'models'
New-Item -ItemType Directory -Force -Path $runtime, $models | Out-Null

function Download-File([string]$url, [string]$dest) {
    Write-Host "Downloading: $url"
    $partial = "$dest.partial"
    if (Test-Path $partial) { Remove-Item $partial -Force }
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $partial -Headers @{ 'User-Agent' = 'PortableClipboardTranslator/0.3' } -MaximumRedirection 10
        if ((Get-Item $partial).Length -eq 0) { throw "Download empty: $url" }
        Move-Item -Force -Path $partial -Destination $dest
    } catch {
        Remove-Item $partial -Force -ErrorAction SilentlyContinue
        throw
    }
}

function Assert-Hash($asset, [string]$file) {
    if ($null -ne $asset -and $asset.PSObject.Properties.Name -contains 'digest' -and $asset.digest -match '^sha256:([0-9a-fA-F]{64})$') {
        $expected = $Matches[1]
        $actual = (Get-FileHash -Algorithm SHA256 -Path $file).Hash
        if ($actual -ne $expected) { throw "SHA256 checksum mismatch for $($asset.name)" }
    }
}

function Install-Binary($asset, [string]$subdir, [bool]$onlyDlls) {
    $target = Join-Path $runtime $subdir
    New-Item -ItemType Directory -Force -Path $target | Out-Null
    if (-not $onlyDlls -and (Test-Path (Join-Path $target 'llama-server.exe'))) {
        Write-Host "Already installed: $subdir"
        return
    }
    $tmp = Join-Path $env:TEMP ('PortableTranslator-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    try {
        $zip = Join-Path $tmp 'llama.zip'
        Download-File $asset.browser_download_url $zip
        Assert-Hash $asset $zip
        $unpacked = Join-Path $tmp 'unpacked'
        Expand-Archive -Path $zip -DestinationPath $unpacked
        if ($onlyDlls) {
            $files = @(Get-ChildItem -Path $unpacked -Recurse -File -Filter '*.dll')
            if ($files.Count -eq 0) { throw "CUDA DLL package contains no DLL files: $($asset.name)" }
        } else {
            $server = Get-ChildItem -Path $unpacked -Recurse -File -Filter 'llama-server.exe' | Select-Object -First 1
            if ($null -eq $server) { throw "llama-server.exe not found in $($asset.name)" }
            $files = @(Get-ChildItem -Path $server.DirectoryName -File)
        }
        foreach ($file in $files) { Copy-Item -Force -LiteralPath $file.FullName -Destination (Join-Path $target $file.Name) }
        if (-not $onlyDlls -and -not (Test-Path (Join-Path $target 'llama-server.exe'))) { throw 'The server executable was not installed.' }
    } finally {
        Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
    }
}

$modelFile = Join-Path $models 'plamo-3-translate-2b-Q4_K_M.gguf'
$templateFile = Join-Path $models 'chat_template.jinja'
$hf = 'https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF/resolve/main/'
if (-not (Test-Path $modelFile)) { Download-File ($hf + 'plamo-3-translate-2b-Q4_K_M.gguf') $modelFile }
if ((Get-Item $modelFile).Length -lt 1GB) { throw 'Model appears incomplete (<1GB). Remove damaged GGUF and run Setup.cmd again.' }
if (-not (Test-Path $templateFile)) { Download-File ($hf + 'chat_template.jinja') $templateFile }
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

# /releases/latest points at stable tags that may NOT contain Windows binaries.
# Use recent official releases, select one with a matching Windows CPU asset and
# the requested GPU asset. Do not construct or guess download filenames.
Write-Host 'Discovering official llama.cpp Windows builds...'
$releases = @(Invoke-RestMethod -Uri 'https://api.github.com/repos/ggml-org/llama.cpp/releases?per_page=30' -Headers @{ 'User-Agent' = 'PortableClipboardTranslator/0.3'; 'Accept' = 'application/vnd.github+json' })
$choice = $null
$cpuAsset = $null
$gpuAsset = $null
$cudaDllAsset = $null
foreach ($release in $releases) {
    if ($null -eq $release.assets) { continue }
    $cpu = $release.assets | Where-Object { $_.name -match '^llama-.*-bin-win-cpu-x64\.zip$' } | Select-Object -First 1
    if ($null -eq $cpu) { continue }
    $gpu = $null
    $dll = $null
    if ($Backend -eq 'cuda') {
        $gpu = $release.assets | Where-Object { $_.name -match '^llama-.*-bin-win-cuda-12\.4-x64\.zip$' } | Select-Object -First 1
        $dll = $release.assets | Where-Object { $_.name -match '^cudart-llama.*-bin-win-cuda-12\.4-x64\.zip$' } | Select-Object -First 1
        if ($null -eq $gpu -or $null -eq $dll) { continue }
    } elseif ($Backend -eq 'vulkan') {
        $gpu = $release.assets | Where-Object { $_.name -match '^llama-.*-bin-win-vulkan-x64\.zip$' } | Select-Object -First 1
        if ($null -eq $gpu) { continue }
    }
    $choice, $cpuAsset, $gpuAsset, $cudaDllAsset = $release, $cpu, $gpu, $dll
    break
}
if ($null -eq $cpuAsset) {
    throw 'No matching official llama.cpp build found in 30 recent GitHub releases. See https://github.com/ggml-org/llama.cpp/releases'
}
Write-Host "Selected llama.cpp release: $($choice.tag_name)"
Install-Binary $cpuAsset 'cpu' $false
$installedBackend = $Backend
if ($Backend -ne 'cpu') {
    try {
        Install-Binary $gpuAsset $Backend $false
        if ($Backend -eq 'cuda') { Install-Binary $cudaDllAsset $Backend $true }
    } catch {
        Write-Warning "Could not install ${Backend}: $($_.Exception.Message)"
        Write-Warning 'Using CPU fallback instead. Run Setup.cmd later to retry GPU installation.'
        $installedBackend = 'cpu'
    }
}

# Modify only relevant properties; retain user's other settings/hotkey.
$configFile = Join-Path $root 'config.json'
$config = Get-Content -Raw -Encoding UTF8 $configFile | ConvertFrom-Json
$config.server_file = "runtime/$installedBackend/llama-server.exe"
$config.fallback_server_file = 'runtime/cpu/llama-server.exe'
$config.gpu_layers = if ($installedBackend -eq 'cpu') { 0 } else { 99 }
$json = $config | ConvertTo-Json -Depth 8
[System.IO.File]::WriteAllText($configFile, $json, (New-Object System.Text.UTF8Encoding($false)))
Write-Host ''
Write-Host "Setup finished. Primary backend: $installedBackend; CPU fallback installed."
Write-Host "Now run Start.cmd. The full folder can be copied to another Windows x64 PC."
Write-Host 'A different GPU or driver on the destination may trigger CPU fallback.'
Write-Host 'PLaMo terms: https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF'
