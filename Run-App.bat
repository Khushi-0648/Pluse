@echo off
setlocal
title Pulse
echo ===================================================
echo     Starting Pulse...
echo ===================================================
cd /d "%~dp0"

rem Strip Mark-of-the-Web if extracted from an internet-downloaded zip
powershell -NoProfile -ExecutionPolicy Bypass -Command "Get-ChildItem -LiteralPath '%~dp0' -Recurse | Unblock-File -ErrorAction SilentlyContinue" 2>nul

if not exist "whatsapp-auto.exe" (
    echo [ERROR] whatsapp-auto.exe was not found in:
    echo "%~dp0"
    echo.
    pause
    exit /b 1
)
"%~dp0whatsapp-auto.exe" app
set RC=%errorlevel%
if not %RC%==0 (
    echo.
    echo ===================================================
    echo [ERROR] Application stopped with error code %RC%.
    echo Read the message above. If it says the port is in use, Pulse may already be running.
    echo ===================================================
)
echo.
echo Application stopped.
pause
