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

    if (-not [Environment]::Is64BitOperatingSystem) { throw 'Clipboard Translator needs 64-bit Windows 10 or 11.' }
    if ([string]::IsNullOrWhiteSpace($InstallDir)) { throw 'Install directory cannot be empty.' }

    $repo = 'ypsilonmeister/clipboard-translator'
    $exeName = 'ClipboardTranslator.exe'
    $headers = @{ 'User-Agent' = 'ClipboardTranslatorInstaller'; 'Accept' = 'application/vnd.github+json' }
    $uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardTranslator'
    $runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
    $shortcut = Join-Path ([Environment]::GetFolderPath('Programs')) 'Clipboard Translator.lnk'
    $fullPath = [IO.Path]::GetFullPath($InstallDir)
    $dirRoot = [IO.Path]::GetPathRoot($fullPath)
    if ($fullPath -ieq $dirRoot -or $InstallDir.TrimEnd('\') -ieq $dirRoot.TrimEnd('\') -or $fullPath.TrimEnd('\').Length -lt 4) {
        throw "Refusing to use drive root or unsafe directory: $InstallDir"
    }

    $InstallDir = $fullPath.TrimEnd('\')
    $exe = Join-Path $InstallDir $exeName

    function Say([string]$m) { Write-Host $m }
    function Step([string]$m) { Write-Host "==> $m" -ForegroundColor Cyan }

    $criticalDirs = @(
        [Environment]::GetFolderPath('Windows'),
        [Environment]::GetFolderPath('System'),
        [Environment]::GetFolderPath('ProgramFiles'),
        [Environment]::GetFolderPath('ProgramFilesX86'),
        [Environment]::GetFolderPath('UserProfile'),
        [Environment]::GetFolderPath('Desktop')
    ) | Where-Object { $_ } | ForEach-Object { [IO.Path]::GetFullPath($_).TrimEnd('\') }
    if (@($criticalDirs | Where-Object { $InstallDir -ieq $_ }).Count -gt 0) {
        throw "Refusing to use system directory: $InstallDir"
    }

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
            Say "This removes Clipboard Translator from $InstallDir, including the downloaded model"
            Say 'and llama.cpp, the Start menu shortcut and the "Start with Windows" entry.'
            if ((Read-Host 'Continue? (y/N)') -notmatch '^[yY]') { Say 'Cancelled.'; return }
        }
        Stop-App

        # The Start menu entry, the Run value and the Apps & features entry are
        # per user, not per folder: only remove the ones that belong to this folder.
        $run = Get-ItemProperty -Path $runKey -Name 'ClipboardTranslator' -ErrorAction SilentlyContinue
        if ($run -and $run.ClipboardTranslator.IndexOf($exe, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
            Remove-ItemProperty -Path $runKey -Name 'ClipboardTranslator' -ErrorAction SilentlyContinue
        }
        if (Test-Path -LiteralPath $shortcut) {
            try {
                $target = (New-Object -ComObject WScript.Shell).CreateShortcut($shortcut).TargetPath
                if ($target -ieq $exe) { Remove-Item -LiteralPath $shortcut -Force -ErrorAction SilentlyContinue }
            } catch { }
        }
        $reg = Get-ItemProperty -Path $uninstallKey -ErrorAction SilentlyContinue
        if ($reg -and ($reg.PSObject.Properties.Name -contains 'InstallLocation') -and ([string]$reg.InstallLocation).TrimEnd('\') -ieq $InstallDir) {
            Remove-Item -Path $uninstallKey -Recurse -Force -ErrorAction SilentlyContinue
        }
        Set-Location -Path $env:TEMP   # this script may be running from the folder being removed

        # Only files this app owns are removed, never the folder as a whole.
        $failed = New-Object System.Collections.Generic.List[string]
        function Remove-Owned([string]$path, [bool]$recurse = $false) {
            if (-not (Test-Path -LiteralPath $path)) { return }
            try {
                if ($recurse) { Remove-Item -LiteralPath $path -Recurse -Force -ErrorAction Stop }
                else { Remove-Item -LiteralPath $path -Force -ErrorAction Stop }
            } catch { $failed.Add($path) }
        }
        function Remove-IfEmpty([string]$path) {
            if (-not (Test-Path -LiteralPath $path)) { return }
            if (@(Get-ChildItem -LiteralPath $path -Force -ErrorAction SilentlyContinue).Count -eq 0) { Remove-Owned $path }
        }

        # Files the app creates while it runs.
        $owned = @($exeName, 'translator.log', 'server.log', 'STOP', '.installed', 'install.ps1')
        $manifest = Join-Path $InstallDir '.installed'
        if (Test-Path -LiteralPath $manifest -PathType Leaf) {
            # The installer recorded exactly what it unpacked. Entries are plain file
            # names; anything else (paths, "..") is ignored so this file cannot point
            # the removal outside the install folder.
            foreach ($line in @(Get-Content -LiteralPath $manifest -Encoding UTF8 -ErrorAction SilentlyContinue)) {
                $name = ([string]$line).Trim()
                if ($name -and $name -ne '.' -and $name -ne '..' -and $name.IndexOfAny([IO.Path]::GetInvalidFileNameChars()) -lt 0) { $owned += $name }
            }
            if ($owned -contains 'config.json') { $owned += 'config.json.bak' }
        } else {
            # Installed before the manifest existed: the fixed file set of the release zip.
            $owned += @('Setup.cmd', 'Start.cmd', 'Stop.cmd', 'setup.ps1', 'config.json', 'config.json.bak', 'README.txt', 'LICENSE')
        }
        foreach ($name in ($owned | Select-Object -Unique)) {
            $p = Join-Path $InstallDir $name
            if (Test-Path -LiteralPath $p -PathType Leaf) { Remove-Owned $p }
        }

        # setup.ps1 downloads exactly these into models\ (one model; the size is its -Model choice)
        # and one folder per backend into runtime\.
        $modelsDir = Join-Path $InstallDir 'models'
        foreach ($name in @('plamo-3-translate-0.8b-Q4_K_M.gguf', 'plamo-3-translate-2b-Q4_K_M.gguf', 'plamo-3-translate-8b-Q4_K_M.gguf', 'chat_template.jinja')) {
            Remove-Owned (Join-Path $modelsDir $name)
            Remove-Owned (Join-Path $modelsDir ($name + '.partial'))
        }
        Remove-IfEmpty $modelsDir
        $runtimeDir = Join-Path $InstallDir 'runtime'
        foreach ($backend in @('cpu', 'cuda', 'vulkan')) { Remove-Owned (Join-Path $runtimeDir $backend) $true }
        Remove-IfEmpty $runtimeDir

        # Remove the folder itself only when nothing else is left in it.
        Remove-IfEmpty $InstallDir
        if ($failed.Count -gt 0) {
            Write-Warning "Some files could not be removed (in use?). Delete them manually:`n  $($failed -join "`n  ")"
        } elseif (Test-Path -LiteralPath $InstallDir) {
            Say "Clipboard Translator has been removed. Other files in $InstallDir were left alone."
        } else {
            Say 'Clipboard Translator has been removed.'
        }
        return
    }

    # Never install into a folder that already holds other things: uninstall would
    # have to tell our files from theirs, and the zip overwrites same-named files.
    if ((Test-Path -LiteralPath $InstallDir -PathType Container) -and
        -not (Test-Path -LiteralPath $exe) -and -not (Test-Path -LiteralPath (Join-Path $InstallDir '.installed')) -and
        @(Get-ChildItem -LiteralPath $InstallDir -Force -ErrorAction SilentlyContinue).Count -gt 0) {
        throw "$InstallDir is not empty and is not a Clipboard Translator folder. Use an empty or new folder, for example $(Join-Path $InstallDir 'ClipboardTranslator')."
    }

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
            $installedFiles = @()
            $oldManifest = Join-Path $InstallDir '.installed'
            if (Test-Path -LiteralPath $oldManifest) { $installedFiles += @(Get-Content -LiteralPath $oldManifest -Encoding UTF8 -ErrorAction SilentlyContinue) }
            foreach ($f in Get-ChildItem -LiteralPath $unpacked -File) {
                $dest = Join-Path $InstallDir $f.Name
                $installedFiles += $f.Name
                if ($f.Name -eq 'config.json' -and (Test-Path -LiteralPath $dest)) { continue }   # keep the user's settings
                Copy-Item -LiteralPath $f.FullName -Destination $dest -Force
            }
            Set-Content -LiteralPath (Join-Path $InstallDir '.installed') -Value @($installedFiles | Where-Object { $_ } | Select-Object -Unique) -Encoding UTF8
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
