@echo off
cd /d "%~dp0"
setlocal
rem A PowerShell 7 module path breaks Windows PowerShell 5.1 (Get-FileHash goes missing).
set "PSModulePath="
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0setup.ps1" -Backend auto
if errorlevel 1 (
  echo.
  echo Setup failed. See the messages above.
  pause
  exit /b 1
)
echo.
echo Setup complete.
pause
