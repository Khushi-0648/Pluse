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

echo [2/5] Running tests...
go test ./...
if errorlevel 1 goto failed

echo [3/5] Building whatsapp-auto.exe...
go build -trimpath -ldflags="-s -w" -o whatsapp-auto-exe\whatsapp-auto.exe ./cmd/whatsapp-auto
if errorlevel 1 goto failed
copy /y BUILD.md whatsapp-auto-exe\ >nul
copy /y SETUP-GUIDE.md whatsapp-auto-exe\ >nul
copy /y COMPLIANCE.md whatsapp-auto-exe\ >nul
copy /y LICENSE whatsapp-auto-exe\ >nul
copy /y contacts.example.csv whatsapp-auto-exe\ >nul
copy /y config.example.yaml whatsapp-auto-exe\ >nul
copy /y Unblock-Pulse.bat whatsapp-auto-exe\ >nul
rem Note: Internal audit and test reports are excluded from customer packages per BUILD.md line 69
if not exist whatsapp-auto-exe\docs mkdir whatsapp-auto-exe\docs
copy /y docs\meta-whatsapp-setup.md whatsapp-auto-exe\docs\ >nul
copy /y docs\architecture.md whatsapp-auto-exe\docs\ >nul
copy /y docs\security.md whatsapp-auto-exe\docs\ >nul
if not exist whatsapp-auto-exe\scripts mkdir whatsapp-auto-exe\scripts
copy /y scripts\Install-Service.ps1 whatsapp-auto-exe\scripts\ >nul
copy /y scripts\Uninstall-Service.ps1 whatsapp-auto-exe\scripts\ >nul
copy /y scripts\Stop-Background.ps1 whatsapp-auto-exe\scripts\ >nul
copy /y scripts\Sign-Executable.ps1 whatsapp-auto-exe\scripts\ >nul
copy /y scripts\Create-DevCertificate.ps1 whatsapp-auto-exe\scripts\ >nul

echo [4/5] Signing...
rem Set PULSE_SIGN_PFX (a .pfx file) or PULSE_SIGN_THUMBPRINT to a code-signing
rem certificate. Without one the program stays unsigned and Windows may block it.
if defined PULSE_SIGN_PFX (
    powershell -NoProfile -ExecutionPolicy Bypass -File scripts\Sign-Executable.ps1 -PfxPath "%PULSE_SIGN_PFX%" -AllowSelfSigned
    if errorlevel 1 goto failed
) else if defined PULSE_SIGN_THUMBPRINT (
    powershell -NoProfile -ExecutionPolicy Bypass -File scripts\Sign-Executable.ps1 -Thumbprint "%PULSE_SIGN_THUMBPRINT%" -AllowSelfSigned
    if errorlevel 1 goto failed
) else (
    echo [WARNING] No signing certificate configured: the program is UNSIGNED.
    echo           Smart App Control and SmartScreen may block it on customers' PCs. See BUILD.md.
    powershell -NoProfile -Command "(Get-FileHash whatsapp-auto-exe\whatsapp-auto.exe -Algorithm SHA256).Hash.ToLower() + '  whatsapp-auto.exe' | Set-Content -Encoding ascii whatsapp-auto-exe\SHA256SUMS.txt"
)

echo [5/5] Packaging dist\Pulse-%VERSION%.zip (only the files customers need)...
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$ErrorActionPreference = 'Stop';" ^
  "$v = '%VERSION%'; $stage = Join-Path 'dist' ('Pulse-' + $v);" ^
  "$files = 'whatsapp-auto.exe','SHA256SUMS.txt','HOW-TO-RUN.txt','BUILD.md','SETUP-GUIDE.md','COMPLIANCE.md','LICENSE','config.example.yaml','contacts.example.csv','Run-App.bat','Run-Server.bat','Run-Unlink.bat','Reset-Password.bat','Create-Desktop-Shortcut.bat','Unblock-Pulse.bat','scripts\Install-Service.ps1','scripts\Uninstall-Service.ps1','scripts\Stop-Background.ps1','scripts\Sign-Executable.ps1','scripts\Create-DevCertificate.ps1','docs\meta-whatsapp-setup.md','docs\architecture.md','docs\security.md';" ^
  "if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }; New-Item -ItemType Directory -Force (Join-Path $stage 'scripts') | Out-Null; New-Item -ItemType Directory -Force (Join-Path $stage 'docs') | Out-Null;" ^
  "foreach ($f in $files) { if (Test-Path (Join-Path 'whatsapp-auto-exe' $f)) { Copy-Item (Join-Path 'whatsapp-auto-exe' $f) (Join-Path $stage $f) } };" ^
  "Set-Content -Path (Join-Path $stage 'VERSION.txt') -Value ('Pulse ' + $v) -Encoding ascii;" ^
  "$extra = Get-ChildItem whatsapp-auto-exe -Recurse -File | Where-Object { $files -notcontains $_.FullName.Substring((Resolve-Path whatsapp-auto-exe).Path.Length + 1) };" ^
  "if ($extra) { Write-Host ('[NOTE] Not shipped (not on the list): ' + (($extra | ForEach-Object Name) -join ', ')) };" ^
  "$zip = Join-Path (Resolve-Path 'dist').Path ('Pulse-' + $v + '.zip'); if (Test-Path $zip) { Remove-Item -Force $zip };" ^
  "Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem; $root = (Resolve-Path $stage).Path;" ^
  "$za = [System.IO.Compression.ZipFile]::Open($zip, [System.IO.Compression.ZipArchiveMode]::Create);" ^
  "try { foreach ($fi in (Get-ChildItem $root -Recurse -File)) { $name = $fi.FullName.Substring($root.Length + 1).Replace([char]92, [char]47); [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($za, $fi.FullName, $name, [System.IO.Compression.CompressionLevel]::Optimal) } } finally { $za.Dispose() };" ^
  "$h = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower(); Set-Content -Path ($zip + '.sha256') -Value ($h + '  ' + (Split-Path $zip -Leaf)) -Encoding ascii;" ^
  "Write-Host ('Created ' + $zip)"
if errorlevel 1 goto failed

echo.
echo Done: dist\Pulse-%VERSION%.zip and dist\Pulse-%VERSION%.zip.sha256
echo Test the dashboard with: go run -tags uismoke ./cmd/uismoke
if not "%1"=="--no-pause" pause
exit /b 0

:failed
echo.
echo [ERROR] Build stopped. Fix the problem above and run Build.bat again.
if not "%1"=="--no-pause" pause
exit /b 1
