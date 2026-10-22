@echo off
setlocal
cd /d "%~dp0"
title Update Pulse

set "NEW=%~dp0whatsapp-auto-exe\whatsapp-auto.exe"
if not exist "%NEW%" (
    echo The new program was not found: %NEW%
    pause
    exit /b 1
)

echo Closing Pulse normally so everything is saved...
"%NEW%" stop -config "%~dp0config.yaml"
timeout /t 3 /nobreak >nul

tasklist /fi "imagename eq whatsapp-auto.exe" | find /i "whatsapp-auto.exe" >nul
if not errorlevel 1 (
    echo.
    echo Another Pulse window is still open. Close every Pulse window
    echo ^(Ctrl+C in each one^), then double-click this file again.
    pause
    exit /b 1
)

echo Keeping the old program as whatsapp-auto.old.exe...
copy /y "whatsapp-auto.exe" "whatsapp-auto.old.exe" >nul

echo Installing the new program...
copy /y "%NEW%" "whatsapp-auto.exe" >nul
if errorlevel 1 (
    echo Could not replace whatsapp-auto.exe. Nothing was changed.
    pause
    exit /b 1
)

echo Starting Pulse...
start "Pulse" "%~dp0whatsapp-auto.exe" app -config "%~dp0config.yaml"
echo.
echo Done. In your browser press Ctrl+F5 on the dashboard.
echo If anything looks wrong, close Pulse and rename whatsapp-auto.old.exe back.
pause
