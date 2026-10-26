@echo off
setlocal
title Pulse - reset admin password
echo ===================================================
echo     Forgot the admin password?
echo.
echo     1. Close Pulse first.
echo     2. Windows asks for administrator permission:
echo        choose "Yes" to continue.
echo     3. You get a new one-time password.
echo.
echo     Messages, contacts and the WhatsApp connection are kept.
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
rem fltmc only works with administrator rights (net session also fails when the
rem Server service is off, which would ask for permission again and again).
fltmc >nul 2>&1
if not errorlevel 1 goto reset
if /i "%~1"=="/elevated" (
    echo [ERROR] Windows did not give this window administrator rights.
    echo Sign in to Windows with an administrator account and try again.
    echo.
    pause
    exit /b 1
)
goto elevate

:reset
pause
"%~dp0whatsapp-auto.exe" reset-password
echo.
pause
exit /b 0

:elevate
rem Ask Windows for administrator permission (UAC) and run this file again in a new
rem window. The path goes through the environment so no quoting can break it.
echo.
echo Resetting the password needs administrator rights.
echo Windows will now ask for permission. Choose "Yes".
set "PULSE_BAT=%~f0"
powershell -NoProfile -NonInteractive -Command "try { Start-Process -FilePath $env:PULSE_BAT -ArgumentList '/elevated' -Verb RunAs -ErrorAction Stop } catch { exit 1 }"
if errorlevel 1 (
    echo.
    echo [ERROR] Permission was not given, so nothing was changed.
    echo Run Reset-Password.bat again and choose "Yes" when Windows asks.
    echo.
    pause
    exit /b 1
)
echo Continue in the new window that opened.
exit /b 0
