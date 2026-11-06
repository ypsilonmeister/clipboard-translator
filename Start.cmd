@echo off
cd /d "%~dp0"
if exist STOP del /q STOP
start "ClipboardTranslator" /min "%~dp0ClipboardTranslator.exe"
