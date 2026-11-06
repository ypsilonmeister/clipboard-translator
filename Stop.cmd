@echo off
cd /d "%~dp0"
if not exist STOP type nul > STOP
rem The app detects STOP and terminates its llama-server child.
