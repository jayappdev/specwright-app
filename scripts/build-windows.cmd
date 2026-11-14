@echo off
rem Runs build-windows.ps1 from cmd or a double-click, with the same options
rem (e.g. build-windows.cmd -Installer). ExecutionPolicy Bypass applies to
rem this one PowerShell process only; no system setting is changed.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-windows.ps1" %*
set code=%errorlevel%
if not "%code%"=="0" echo Build failed (exit %code%).
rem opened by a double-click: keep the window open to read the result
echo %cmdcmdline% | find /i "/c" >nul && pause
exit /b %code%
