<#
.SYNOPSIS
  Installs, updates or removes Clipboard Translator for the current user.

.DESCRIPTION
  Install or update (no admin rights needed):
    irm https://raw.githubusercontent.com/ypsilonmeister/clipboard-translator/main/install.ps1 | iex

  Remove:
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/ypsilonmeister/clipboard-translator/main/install.ps1))) -Uninstall

  What it does: downloads the latest GitHub release zip and checks its SHA-256,
  installs it to %LOCALAPPDATA%\ClipboardTranslator, runs the bundled setup.ps1
  (PLaMo model and llama.cpp, only when missing), adds a Start menu shortcut and an
  "Apps & features" entry, and starts the app. An existing config.json is kept.

  Through `| iex` no parameters can be passed, so the same options are read from
  environment variables: CT_VERSION, CT_INSTALL_DIR, CT_SKIP_SETUP=1, CT_NO_LAUNCH=1.
#>
param(
    [string]$Version = $env:CT_VERSION,
    [string]$InstallDir = $(if ($env:CT_INSTALL_DIR) { $env:CT_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'ClipboardTranslator' }),
    [switch]$SkipSetup,
    [switch]$NoLaunch,
    [switch]$Force,
    [switch]$Uninstall,
    [switch]$Yes
)

function Invoke-ClipboardTranslatorInstaller {
    param($Version, $InstallDir, [bool]$SkipSetup, [bool]$NoLaunch, [bool]$Force, [bool]$Uninstall, [bool]$Yes)

    Set-StrictMode -Version Latest
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = 'ypsilonmeister/clipboard-translator'
    $exeName = 'ClipboardTranslator.exe'
    $headers = @{ 'User-Agent' = 'ClipboardTranslatorInstaller'; 'Accept' = 'application/vnd.github+json' }
    $uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardTranslator'
    $runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
    $shortcut = Join-Path ([Environment]::GetFolderPath('Programs')) 'Clipboard Translator.lnk'
    $InstallDir = [IO.Path]::GetFullPath($InstallDir).TrimEnd('\')
    $exe = Join-Path $InstallDir $exeName

    function Say([string]$m) { Write-Host $m }
    function Step([string]$m) { Write-Host "==> $m" -ForegroundColor Cyan }

    # Asks the running app to quit through its STOP file (which also ends its
    # llama-server child), and only force-kills what is left.
    function Stop-App {
        $mine = {
            param($p)
            try { $p.Path -and $p.Path.StartsWith($InstallDir + '\', [StringComparison]::OrdinalIgnoreCase) } catch { $false }
        }
        $procs = @(Get-Process -Name 'ClipboardTranslator' -ErrorAction SilentlyContinue | Where-Object { & $mine $_ })
        if ($procs.Count -gt 0) {
            Step 'Stopping the running app'
            New-Item -ItemType File -Force -Path (Join-Path $InstallDir 'STOP') | Out-Null
            foreach ($p in $procs) { [void]$p.WaitForExit(15000) }
            Get-Process -Name 'ClipboardTranslator' -ErrorAction SilentlyContinue | Where-Object { & $mine $_ } | Stop-Process -Force -ErrorAction SilentlyContinue
        }
        Get-Process -Name 'llama-server' -ErrorAction SilentlyContinue | Where-Object { & $mine $_ } | Stop-Process -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath (Join-Path $InstallDir 'STOP') -Force -ErrorAction SilentlyContinue
    }

    if ($Uninstall) {
        if (-not (Test-Path -LiteralPath $exe)) { throw "Clipboard Translator is not installed at $InstallDir" }
        if (-not $Yes) {
            Say "This removes $InstallDir, including the downloaded model and llama.cpp,"
            Say 'the Start menu shortcut and the "Start with Windows" entry.'
            if ((Read-Host 'Continue? (y/N)') -notmatch '^[yY]') { Say 'Cancelled.'; return }
        }
        Stop-App
        $run = Get-ItemProperty -Path $runKey -Name 'ClipboardTranslator' -ErrorAction SilentlyContinue
        if ($run -and $run.ClipboardTranslator -like "*$exeName*" -and $run.ClipboardTranslator -like "*$InstallDir*") {
            Remove-ItemProperty -Path $runKey -Name 'ClipboardTranslator' -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $shortcut -Force -ErrorAction SilentlyContinue
        Remove-Item -Path $uninstallKey -Recurse -Force -ErrorAction SilentlyContinue
        Set-Location -Path $env:TEMP   # this script may be running from the folder being removed
        Remove-Item -LiteralPath $InstallDir -Recurse -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $InstallDir) {
            Write-Warning "Some files in $InstallDir could not be removed (in use?). Delete the folder manually."
        } else {
            Say 'Clipboard Translator has been removed.'
        }
        return
    }

    if (-not [Environment]::Is64BitOperatingSystem) { throw 'Clipboard Translator needs 64-bit Windows 10 or 11.' }
    if ($InstallDir.Length -lt 4) { throw "Refusing to install into $InstallDir" }

    Step 'Looking up the release'
    $tag = if ($Version) { 'v' + ([string]$Version).TrimStart('v') } else { $null }
    $uri = if ($tag) { "https://api.github.com/repos/$repo/releases/tags/$tag" } else { "https://api.github.com/repos/$repo/releases/latest" }
    $release = Invoke-RestMethod -Uri $uri -Headers $headers
    $asset = @($release.assets | Where-Object { $_.name -match '^Portable-Clipboard-Translator-Windows-x64-.*\.zip$' }) | Select-Object -First 1
    if (-not $asset) { throw "Release $($release.tag_name) has no portable Windows zip." }
    $newVersion = ([string]$release.tag_name).TrimStart('v')

    $installedVersion = $null
    if (Test-Path -LiteralPath $exe) {
        $installedVersion = (Get-Item -LiteralPath $exe).VersionInfo.ProductVersion
    }
    $upToDate = ($installedVersion -eq $newVersion) -and -not $Force

    if ($upToDate) {
        Say "Version $newVersion is already installed in $InstallDir (use -Force to reinstall)."
    } else {
        if ($installedVersion) { Say "Updating $installedVersion -> $newVersion" } else { Say "Installing $newVersion" }
        $tmp = Join-Path $env:TEMP ('ClipboardTranslator-' + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Force -Path $tmp | Out-Null
        try {
            Step "Downloading $($asset.name)"
            $zip = Join-Path $tmp 'app.zip'
            Invoke-WebRequest -UseBasicParsing -Uri $asset.browser_download_url -OutFile $zip -Headers @{ 'User-Agent' = 'ClipboardTranslatorInstaller' } -MaximumRedirection 10

            $expected = $null
            if ($asset.PSObject.Properties.Name -contains 'digest' -and $asset.digest -match '^sha256:([0-9a-fA-F]{64})$') { $expected = $Matches[1] }
            elseif ($release.body -match 'SHA-256[^`]*`([0-9a-fA-F]{64})`') { $expected = $Matches[1] }
            if (-not $expected) { throw "No SHA-256 published for $($asset.name); refusing to install an unverified download." }
            $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash
            if ($actual -ne $expected) { throw "SHA-256 mismatch for $($asset.name) (expected $expected, got $actual)." }
            Say "SHA-256 verified: $actual"

            $unpacked = Join-Path $tmp 'unpacked'
            Expand-Archive -LiteralPath $zip -DestinationPath $unpacked
            if (-not (Test-Path -LiteralPath (Join-Path $unpacked $exeName))) { throw "$exeName is missing from the zip." }

            Stop-App
            Step "Installing to $InstallDir"
            New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
            foreach ($f in Get-ChildItem -LiteralPath $unpacked -File) {
                $dest = Join-Path $InstallDir $f.Name
                if ($f.Name -eq 'config.json' -and (Test-Path -LiteralPath $dest)) { continue }   # keep the user's settings
                Copy-Item -LiteralPath $f.FullName -Destination $dest -Force
            }
        } finally {
            Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
        }
    }

    $model = @(Get-ChildItem -LiteralPath (Join-Path $InstallDir 'models') -Filter '*.gguf' -ErrorAction SilentlyContinue)
    $runtimeReady = Test-Path -LiteralPath (Join-Path $InstallDir 'runtime\cpu\llama-server.exe')
    if ($SkipSetup) {
        Say 'Skipping model and llama.cpp download (-SkipSetup). Run setup.ps1 in the install folder later.'
    } elseif ($model.Count -gt 0 -and $runtimeReady -and -not $Force) {
        Say 'Model and llama.cpp are already installed.'
    } else {
        Step 'Downloading the PLaMo model (over 1 GB) and llama.cpp - this can take a while'
        $setup = Join-Path $InstallDir 'setup.ps1'
        $p = Start-Process -FilePath 'powershell.exe' -WorkingDirectory $InstallDir -Wait -PassThru -NoNewWindow `
            -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$setup`"", '-Backend', 'auto')
        if ($p.ExitCode -ne 0) { throw "setup.ps1 failed (exit code $($p.ExitCode)). Run the installer again to retry." }
    }

    Step 'Registering the app'
    $wsh = New-Object -ComObject WScript.Shell
    $lnk = $wsh.CreateShortcut($shortcut)
    $lnk.TargetPath = $exe
    $lnk.WorkingDirectory = $InstallDir
    $lnk.IconLocation = "$exe,0"
    $lnk.Description = 'Clipboard Translator'
    $lnk.Save()

    # Keep a copy of this installer next to the app so "Apps & features" can uninstall offline.
    $selfCopy = Join-Path $InstallDir 'install.ps1'
    if ($PSCommandPath -and ($PSCommandPath -eq $selfCopy)) {
        # already running from there
    } elseif ($PSCommandPath -and (Test-Path -LiteralPath $PSCommandPath)) {
        Copy-Item -LiteralPath $PSCommandPath -Destination $selfCopy -Force
    } else {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "https://raw.githubusercontent.com/$repo/main/install.ps1" -OutFile $selfCopy -Headers @{ 'User-Agent' = 'ClipboardTranslatorInstaller' }
        } catch {
            Write-Warning "Could not save install.ps1 for uninstalling: $($_.Exception.Message)"
        }
    }
    if (Test-Path -LiteralPath $selfCopy) {
        $sizeKb = [int]((Get-ChildItem -LiteralPath $InstallDir -Recurse -File | Measure-Object Length -Sum).Sum / 1KB)
        New-Item -Path $uninstallKey -Force | Out-Null
        $entries = @{
            DisplayName     = 'Clipboard Translator'
            DisplayVersion  = $newVersion
            Publisher       = 'ypsilonmeister'
            InstallLocation = $InstallDir
            DisplayIcon     = $exe
            URLInfoAbout    = "https://github.com/$repo"
            UninstallString = "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$selfCopy`" -Uninstall -Yes -InstallDir `"$InstallDir`""
        }
        foreach ($k in $entries.Keys) { New-ItemProperty -Path $uninstallKey -Name $k -Value $entries[$k] -PropertyType String -Force | Out-Null }
        New-ItemProperty -Path $uninstallKey -Name 'EstimatedSize' -Value $sizeKb -PropertyType DWord -Force | Out-Null
        New-ItemProperty -Path $uninstallKey -Name 'NoModify' -Value 1 -PropertyType DWord -Force | Out-Null
        New-ItemProperty -Path $uninstallKey -Name 'NoRepair' -Value 1 -PropertyType DWord -Force | Out-Null
    }

    $hotkey = 'Ctrl+Alt+E'
    try { $hotkey = (Get-Content -LiteralPath (Join-Path $InstallDir 'config.json') -Raw -Encoding UTF8 | ConvertFrom-Json).hotkey } catch { }

    if (-not $NoLaunch) {
        Step 'Starting Clipboard Translator'
        Remove-Item -LiteralPath (Join-Path $InstallDir 'STOP') -Force -ErrorAction SilentlyContinue
        Start-Process -FilePath $exe -WorkingDirectory $InstallDir
    }

    Say ''
    Say "Done: Clipboard Translator $newVersion is installed in $InstallDir"
    Say "Look for its icon in the system tray (it may be under the ^ arrow). Select text and press $hotkey,"
    Say 'then paste the translation with Ctrl+V. Right-click the tray icon for "Start with Windows" and "Quit".'
    Say 'Update: run the install command again.  Remove: Settings > Apps, or run the installer with -Uninstall.'
}

Invoke-ClipboardTranslatorInstaller -Version $Version -InstallDir $InstallDir `
    -SkipSetup ([bool]($SkipSetup -or $env:CT_SKIP_SETUP)) -NoLaunch ([bool]($NoLaunch -or $env:CT_NO_LAUNCH)) `
    -Force ([bool]$Force) -Uninstall ([bool]$Uninstall) -Yes ([bool]$Yes)
