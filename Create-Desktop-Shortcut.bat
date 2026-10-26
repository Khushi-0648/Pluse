@echo off
cd /d "%~dp0"
if not exist "whatsapp-auto.exe" (
    echo whatsapp-auto.exe was not found in this folder.
    echo Unzip the whole Pulse folder first, then run this file from inside it.
    pause
    exit /b 1
)
echo Creating a Pulse shortcut on your Desktop...
"whatsapp-auto.exe" shortcut
if errorlevel 1 (
    echo.
    echo The shortcut could not be created. See the message above.
    pause
    exit /b 1
)
echo Done. Look for Pulse on your Desktop.
pause
