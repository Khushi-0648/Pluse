@echo off
setlocal
cd /d "%~dp0"
rem The version comes from pkg\version\version.go only, so the app, VERSION.txt and the
rem zip name always match.
for /f "tokens=2 delims==" %%v in ('findstr /r /c:"^var Version = " pkg\version\version.go') do set VERSION=%%~v
set VERSION=%VERSION: =%
set VERSION=%VERSION:"=%
echo ========================================================
echo   Pulse %VERSION% - release build
echo ========================================================

rem A release must match a commit exactly, or it cannot be rebuilt or traced later
rem (the exe records vcs.modified=true). PULSE_ALLOW_DIRTY=1 allows a local test build.
rem New files that are not committed count too (a new .go file is compiled in), except
rem those .gitignore leaves out: git status --porcelain does not list them.
set DIRTY=
set ZIPVER=%VERSION%
rem Without git, or outside a git repository, git status prints nothing and the check
rem below would pass. Such a build cannot be traced to a commit, so it counts as dirty.
git rev-parse --is-inside-work-tree >nul 2>nul
if errorlevel 1 (
    set DIRTY=1
    echo [WARNING] git is not installed or this folder is not a git repository.
    echo           The build cannot be matched to a commit and counts as uncommitted.
)
for /f %%s in ('git status --porcelain 2^>nul') do set DIRTY=1
if defined DIRTY (
    if not "%PULSE_ALLOW_DIRTY%"=="1" (
        echo [ERROR] There are uncommitted changes or new files, or git could not check. Commit them first, so the release matches a commit.
        echo         For a local test build only: set PULSE_ALLOW_DIRTY=1
        goto failed
    )
    rem The zip name says so, so a test build cannot be mistaken for a release.
    set ZIPVER=%VERSION%-DIRTY
    echo.
    echo ########################################################
    echo #  WARNING: BUILDING FROM UNCOMMITTED CHANGES          #
    echo #  ^(PULSE_ALLOW_DIRTY=1^). THIS IS A TEST BUILD.        #
    echo #  The zips are named Pulse-%VERSION%-DIRTY*.zip.
    echo #  DO NOT SHIP THEM TO CUSTOMERS.                      #
    echo ########################################################
    echo.
)

echo [1/5] Checking code...
go vet ./...
if errorlevel 1 goto failed
rem A syntax error in one dashboard file stops the whole dashboard from loading.
where node >nul 2>nul
if errorlevel 1 (
    echo [WARNING] Node.js not found: dashboard scripts were not syntax-checked. Run the browser test before shipping.
) else (
    for %%f in (pkg\server\web\assets\js\*.js) do (
        node --check "%%f"
        if errorlevel 1 goto failed
    )
)

rem Pulse uses no C code: building without cgo means no C compiler is needed and a
rem broken or 32-bit gcc on the PATH cannot affect the release.
set CGO_ENABLED=0

echo [2/5] Running tests...
go test ./...
if errorlevel 1 goto failed

echo [3/5] Building whatsapp-auto.exe for Windows x64, ARM64 and x86...
rem Icon, version info and manifest (winres\winres.json). Install the tool once with:
rem   go install github.com/tc-hib/go-winres@v0.3.3
set WINRES=go-winres
where go-winres >nul 2>nul
if errorlevel 1 for /f "delims=" %%g in ('go env GOPATH') do set WINRES=%%g\bin\go-winres.exe
"%WINRES%" make --in winres\winres.json --out cmd\whatsapp-auto\rsrc --arch amd64,arm64,386 --product-version %VERSION% --file-version %VERSION%
if errorlevel 1 (
    echo [ERROR] go-winres failed or is not installed. See the comment above this line in Build.bat.
    goto failed
)
rem x64 is the main build (most PCs); ARM64 is for Windows on ARM laptops, x86 for 32-bit Windows 10.
set GOOS=windows
set GOARCH=amd64
go build -trimpath -ldflags="-s -w" -o whatsapp-auto-exe\whatsapp-auto.exe ./cmd/whatsapp-auto
if errorlevel 1 goto failed
set GOARCH=arm64
go build -trimpath -ldflags="-s -w" -o dist\exe-arm64\whatsapp-auto.exe ./cmd/whatsapp-auto
if errorlevel 1 goto failed
set GOARCH=386
go build -trimpath -ldflags="-s -w" -o dist\exe-x86\whatsapp-auto.exe ./cmd/whatsapp-auto
if errorlevel 1 goto failed
set GOARCH=
set GOOS=
copy /y BUILD.md whatsapp-auto-exe\ >nul
copy /y SETUP-GUIDE.md whatsapp-auto-exe\ >nul
copy /y COMPLIANCE.md whatsapp-auto-exe\ >nul
copy /y LICENSE whatsapp-auto-exe\ >nul
copy /y contacts.example.csv whatsapp-auto-exe\ >nul
copy /y config.example.yaml whatsapp-auto-exe\ >nul
copy /y Unblock-Pulse.bat whatsapp-auto-exe\ >nul
copy /y install.ps1 whatsapp-auto-exe\ >nul
copy /y HOW-TO-RUN.txt whatsapp-auto-exe\ >nul
copy /y Run-App.bat whatsapp-auto-exe\ >nul
copy /y Run-Server.bat whatsapp-auto-exe\ >nul
copy /y Run-Unlink.bat whatsapp-auto-exe\ >nul
copy /y Reset-Password.bat whatsapp-auto-exe\ >nul
copy /y Create-Desktop-Shortcut.bat whatsapp-auto-exe\ >nul
rem Note: Internal audit and test reports are excluded from customer packages per BUILD.md line 69
if not exist whatsapp-auto-exe\docs mkdir whatsapp-auto-exe\docs
copy /y docs\meta-whatsapp-setup.md whatsapp-auto-exe\docs\ >nul
copy /y docs\architecture.md whatsapp-auto-exe\docs\ >nul
copy /y docs\security.md whatsapp-auto-exe\docs\ >nul
if not exist whatsapp-auto-exe\scripts mkdir whatsapp-auto-exe\scripts
copy /y scripts\Install-Service.ps1 whatsapp-auto-exe\scripts\ >nul
copy /y scripts\Uninstall-Service.ps1 whatsapp-auto-exe\scripts\ >nul
copy /y scripts\Stop-Background.ps1 whatsapp-auto-exe\scripts\ >nul
copy /y scripts\Check-SmartAppControl.bat whatsapp-auto-exe\scripts\ >nul

echo [4/5] Signing...
rem Set PULSE_SIGN_PFX (a .pfx file) or PULSE_SIGN_THUMBPRINT to a code-signing
rem certificate. Without one the program stays unsigned and Windows may block it.
rem Self-signed certificates are refused unless PULSE_SIGN_ALLOW_SELF_SIGNED=1 (local tests).
set SIGN_EXTRA=
if "%PULSE_SIGN_ALLOW_SELF_SIGNED%"=="1" set SIGN_EXTRA=-AllowSelfSigned
if not defined PULSE_SIGN_PFX if not defined PULSE_SIGN_THUMBPRINT (
    echo [WARNING] No signing certificate configured: the programs are UNSIGNED.
    echo           Smart App Control and SmartScreen may block them on customers' PCs. See BUILD.md.
)
rem All three programs are signed in one call, so the .pfx password is asked only once.
set SIGN_LIST=whatsapp-auto-exe\whatsapp-auto.exe,dist\exe-arm64\whatsapp-auto.exe,dist\exe-x86\whatsapp-auto.exe
if defined PULSE_SIGN_PFX (
    powershell -NoProfile -ExecutionPolicy Bypass -File scripts\Sign-Executable.ps1 -BinaryPath "%SIGN_LIST%" -PfxPath "%PULSE_SIGN_PFX%" %SIGN_EXTRA%
    if errorlevel 1 goto failed
) else if defined PULSE_SIGN_THUMBPRINT (
    powershell -NoProfile -ExecutionPolicy Bypass -File scripts\Sign-Executable.ps1 -BinaryPath "%SIGN_LIST%" -Thumbprint "%PULSE_SIGN_THUMBPRINT%" %SIGN_EXTRA%
    if errorlevel 1 goto failed
) else (
    for %%d in (whatsapp-auto-exe dist\exe-arm64 dist\exe-x86) do (
        powershell -NoProfile -Command "(Get-FileHash '%%d\whatsapp-auto.exe' -Algorithm SHA256).Hash.ToLower() + '  whatsapp-auto.exe' | Set-Content -Encoding ascii '%%d\SHA256SUMS.txt'"
    )
)

echo [5/5] Packaging one zip per Windows type (only the files customers need)...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\Package-Release.ps1 -Version %ZIPVER%
if errorlevel 1 goto failed

echo.
echo Done: dist\Pulse-%ZIPVER%.zip (x64), dist\Pulse-%ZIPVER%-arm64.zip, dist\Pulse-%ZIPVER%-x86.zip and their .sha256 files
if defined DIRTY echo [WARNING] TEST BUILD FROM UNCOMMITTED CHANGES: do not ship the -DIRTY zips.
echo Publish the SHA-256 of whatsapp-auto.exe on the download page, not only inside the zip.
echo Test the dashboard with: go run -tags uismoke ./cmd/uismoke
if not "%1"=="--no-pause" pause
exit /b 0

:failed
echo.
echo [ERROR] Build stopped. Fix the problem above and run Build.bat again.
if not "%1"=="--no-pause" pause
exit /b 1
