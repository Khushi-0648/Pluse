@echo off
setlocal
title Pulse
echo ===================================================
echo     Starting Pulse...
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
if exist "%~dp0scripts\Check-SmartAppControl.bat" (
    call "%~dp0scripts\Check-SmartAppControl.bat"
    if errorlevel 1 (
        echo.
        pause
        exit /b 1
    )
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
