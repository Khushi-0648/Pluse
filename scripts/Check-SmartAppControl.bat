@echo off
rem Called by Run-App.bat and Run-Server.bat before Pulse starts.
rem Smart App Control (Windows 11) blocks unsigned programs before they can start, so
rem whatsapp-auto.exe can never explain this itself: the check has to run here.
rem Exit code 0: go on. Exit code 1: Smart App Control is on, this copy of Pulse has no
rem valid trusted signature, and nothing was started.
setlocal
set SAC=0x0
for /f "tokens=3" %%v in ('reg query "HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy" /v VerifiedAndReputablePolicyState 2^>nul ^| find "0x"') do set SAC=%%v
rem 0x0 = off, 0x1 = on, 0x2 = evaluation (watches only, does not block)
if /i not "%SAC%"=="0x1" exit /b 0

rem A program signed with a trusted certificate (status Valid) is allowed to start.
set EXE=%~dp0..\whatsapp-auto.exe
if not exist "%EXE%" set EXE=%~dp0whatsapp-auto.exe
set SIG=NotFound
for /f "delims=" %%s in ('powershell -NoProfile -Command "try { (Get-AuthenticodeSignature -LiteralPath $env:EXE -ErrorAction Stop).Status } catch { 'Unknown' }" 2^>nul') do set SIG=%%s
if /i "%SIG%"=="Valid" exit /b 0

echo.
echo ===================================================
echo  Windows Smart App Control is ON on this computer.
echo ===================================================
echo  It blocks programs that are not signed with a trusted certificate.
echo  This copy of Pulse has no valid trusted signature (status: %SIG%),
echo  so Windows will not let it start.
echo.
echo  To use Pulse on this computer, switch Smart App Control off:
echo    Windows Security ^> App ^& browser control ^> Smart App Control settings ^> Off
echo.
echo  Before you decide:
echo   - Only do this if you got Pulse from a source you trust.
echo   - It lowers this computer's protection against unknown programs.
echo   - On most Windows versions it cannot be switched back on without
echo     resetting Windows.
echo.
choice /c YN /m "Open Windows Security now so you can switch it off"
if errorlevel 2 (
    echo.
    echo Nothing was changed. Pulse was not started.
    exit /b 1
)
start "" "windowsdefender://appbrowser"
echo.
echo Windows Security is open. Switch Smart App Control off there,
echo then start Pulse again.
exit /b 1
