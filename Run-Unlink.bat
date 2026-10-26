@echo off
setlocal
title Pulse - disconnect WhatsApp
echo ===================================================
echo     Removing the WhatsApp Business API access token from
echo     this computer (messages and contacts are kept)...
echo ===================================================
cd /d "%~dp0"
if not exist "whatsapp-auto.exe" (
    echo [ERROR] whatsapp-auto.exe was not found in:
    echo "%~dp0"
    echo If you opened this file inside the zip, unzip the whole Pulse folder first
    echo ^(right-click the zip, Extract All^), then run it from the unzipped folder.
    echo.
    pause
    exit /b 1
)
"%~dp0whatsapp-auto.exe" disconnect
echo.
pause
