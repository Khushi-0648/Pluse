@echo off
setlocal
title Unblock Pulse
echo ===================================================
echo     Unblock Pulse files for Windows
echo ===================================================
cd /d "%~dp0"
set "PULSE_DIR=%~dp0"
if not exist "whatsapp-auto.exe" (
    echo [ERROR] whatsapp-auto.exe was not found in:
    echo "%~dp0"
    echo If you opened this file inside the zip, unzip the whole Pulse folder first
    echo ^(right-click the zip, Extract All^), then run it from the unzipped folder.
    echo.
    pause
    exit /b 1
)
echo SHA-256 of whatsapp-auto.exe:
powershell -NoProfile -Command "(Get-FileHash -LiteralPath (Join-Path $env:PULSE_DIR 'whatsapp-auto.exe') -Algorithm SHA256).Hash.ToLower()"
echo.
echo Compare it with the checksum published on the page you downloaded Pulse from.
echo Only continue if both match exactly.
echo.
choice /c YN /m "Do the checksums match"
if errorlevel 2 (
    echo Nothing was changed. Download Pulse again from the official page.
    pause
    exit /b 1
)
powershell -NoProfile -Command "Get-ChildItem -LiteralPath $env:PULSE_DIR -Recurse | Unblock-File; Write-Host 'All Pulse files unblocked.' -ForegroundColor Green"
echo.
echo Smart App Control ^(Windows 11^) blocks unsigned programs completely, and the
echo unblocking above does not change that. A signed release of Pulse runs with it on.
echo For an unsigned copy, Run-App.bat explains the choice when you start Pulse.
echo.
pause
