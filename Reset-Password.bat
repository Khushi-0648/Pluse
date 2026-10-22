@echo off
setlocal
title Pulse - reset admin password
echo ===================================================
echo     Forgot the admin password?
echo.
echo     1. Close Pulse first.
echo     2. This window must run with administrator rights:
echo        right-click Reset-Password.bat and choose
echo        "Run as administrator".
echo     3. You get a new one-time password.
echo.
echo     Messages, contacts and the WhatsApp connection are kept.
echo ===================================================
cd /d "%~dp0"
if not exist "whatsapp-auto.exe" (
    echo [ERROR] whatsapp-auto.exe was not found in:
    echo "%~dp0"
    echo.
    pause
    exit /b 1
)
pause
"%~dp0whatsapp-auto.exe" reset-password
echo.
pause
