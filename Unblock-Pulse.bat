@echo off
setlocal
title Unblock Pulse
echo ===================================================
echo     Unblocking Pulse files for Windows...
echo ===================================================
cd /d "%~dp0"

powershell -NoProfile -ExecutionPolicy Bypass -Command "Get-ChildItem -LiteralPath '%~dp0' -Recurse | Unblock-File -ErrorAction SilentlyContinue; Write-Host 'All Pulse files unblocked successfully!' -ForegroundColor Green"
echo.
echo ===================================================
echo NOTE FOR WINDOWS 11 SMART APP CONTROL:
echo If Smart App Control is strictly ON on this machine:
echo   1. Open Windows Security from the Start menu.
echo   2. Click 'App & browser control' > 'Smart App Control'.
echo   3. Set it to 'Evaluation' or 'Off'.
echo ===================================================
echo.
pause
