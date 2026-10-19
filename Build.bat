@echo off
echo ========================================================
echo   Pulse WhatsApp Automation - Production Build Script
echo ========================================================
echo.
echo Running unit tests...
go test ./...
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Unit tests failed! Build aborted.
    pause
    exit /b %ERRORLEVEL%
)

echo.
echo Compiling trimmed production binary (whatsapp-auto.exe)...
go build -trimpath -ldflags="-s -w" -o whatsapp-auto.exe ./cmd/whatsapp-auto
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Build failed!
    pause
    exit /b %ERRORLEVEL%
)

echo.
echo [SUCCESS] Binary compiled successfully: whatsapp-auto.exe
pause
