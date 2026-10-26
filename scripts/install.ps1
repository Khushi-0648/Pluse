# Pulse one-line installer for Windows 10/11 (no administrator rights needed).
#
#   irm https://pulse.yourdomain.com/install.ps1 | iex
#
# It downloads the Pulse zip for this PC (x64, ARM64 or x86), checks it against the
# SHA-256 written below, installs it to %LOCALAPPDATA%\Programs\Pulse and puts a Pulse
# shortcut on the desktop. Customer data stays in %APPDATA%\Pulse and is never touched.
#
# Files downloaded by PowerShell are not marked as "downloaded from the internet", so
# SmartScreen does not show "Windows protected your PC". Smart App Control (Windows 11)
# still checks every program and blocks unsigned ones: on such a PC this script stops and
# explains why. It never switches any protection off. Only a code-signed release runs there.
#
# Before publishing: set $BaseUrl to the https address where the zips are hosted, and
# update $Version and the checksums for each release (dist\*.zip.sha256 from Build.bat).

$ErrorActionPreference = 'Stop'
$Version = '2.1.1'
$BaseUrl = 'https://pulse.yourdomain.com/downloads'   # <-- your https download folder
$Sha256 = @{
    'x64'   = '0aac06b1205c0f36836e9152510872f24c6a3c714d3fa61a374079168c10876c'
    'arm64' = 'cb3a417c23b3cd1de7a60db33c4430dd0dd3394301c6f3a7fcd0c6f42bed389a'
    'x86'   = '1a1c11bb49305cb2b5b4e3d3440832191a276c8be126f426928e099869c669db'
}
# For testing only: $env:PULSE_INSTALL_BASEURL replaces $BaseUrl (must still be https,
# except http://127.0.0.1 for a local test server), $env:PULSE_INSTALL_DIR the folder,
# and $env:PULSE_INSTALL_NO_SHORTCUT=1 skips the desktop shortcut.
if ($env:PULSE_INSTALL_BASEURL) { $BaseUrl = $env:PULSE_INSTALL_BASEURL.TrimEnd('/') }
$InstallDir = if ($env:PULSE_INSTALL_DIR) { $env:PULSE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\Pulse' }

function Stop-Install([string]$msg) {
    Write-Host ''
    Write-Host $msg -ForegroundColor Red
    Write-Host 'Pulse was not installed. Nothing on this computer was changed.'
    # A plain throw ends `irm | iex` without closing the user's PowerShell window.
    throw 'Pulse installation stopped.'
}

Write-Host "Installing Pulse $Version" -ForegroundColor Cyan

# --- Checks before anything is written ---------------------------------------------
if ([Environment]::OSVersion.Version.Major -lt 10) { Stop-Install 'Pulse needs Windows 10 or 11.' }
if ($BaseUrl -notmatch '^https://' -and $BaseUrl -notmatch '^http://127\.0\.0\.1(:\d+)?(/|$)') {
    Stop-Install "The download address must start with https:// ($BaseUrl)."
}

# The processor this Windows runs on (an x86 PowerShell on a 64-bit PC still gets x64).
$arch = $env:PROCESSOR_ARCHITEW6432
if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
switch ($arch) {
    'AMD64' { $flavor = 'x64'; $suffix = '' }
    'ARM64' { $flavor = 'arm64'; $suffix = '-arm64' }
    'x86'   { $flavor = 'x86'; $suffix = '-x86' }
    default { Stop-Install "This processor ($arch) is not supported." }
}

# Smart App Control: 1 = on (blocks unsigned programs), 2 = evaluation, 0 = off.
$sac = 0
try { $sac = (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy' -Name VerifiedAndReputablePolicyState -ErrorAction Stop).VerifiedAndReputablePolicyState } catch {}

# --- Download and verify -------------------------------------------------------------
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
$zipName = "Pulse-$Version$suffix.zip"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("pulse-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp $zipName
    Write-Host "Downloading $zipName ($flavor)..."
    try {
        $old = $ProgressPreference; $ProgressPreference = 'SilentlyContinue'   # much faster on Windows PowerShell 5.1
        Invoke-WebRequest -Uri "$BaseUrl/$zipName" -OutFile $zip -UseBasicParsing
        $ProgressPreference = $old
    } catch {
        Stop-Install "Could not download $BaseUrl/$zipName : $($_.Exception.Message)"
    }
    $got = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($got -ne $Sha256[$flavor]) {
        Stop-Install "The download does not match the expected checksum.`n  expected $($Sha256[$flavor])`n  got      $got`nIt may be damaged or changed. Do not run it."
    }
    Write-Host "Checksum OK: $got" -ForegroundColor Green

    $stage = Join-Path $tmp 'Pulse'
    Expand-Archive -Path $zip -DestinationPath $stage
    $newExe = Join-Path $stage 'whatsapp-auto.exe'
    if (-not (Test-Path $newExe)) { Stop-Install 'The download does not contain whatsapp-auto.exe.' }

    if ($sac -eq 1 -and (Get-AuthenticodeSignature $newExe).Status -ne 'Valid') {
        Stop-Install ("Smart App Control is on for this PC. It blocks programs that are not signed with a trusted`n" +
            "certificate, and this copy of Pulse is not signed yet, so Windows would not let it start.`n" +
            "Use a signed release of Pulse on this PC. (Switching Smart App Control off is possible in`n" +
            "Windows Security, but it lowers this PC's protection and usually cannot be switched on again.)")
    }

    # --- Install -----------------------------------------------------------------------
    $exe = Join-Path $InstallDir 'whatsapp-auto.exe'
    if (Test-Path $exe) {
        # Close a running copy normally so its data is saved, then keep the old program
        # folder until the new one is in place.
        & $exe stop 2>$null | Out-Null
        Start-Sleep -Seconds 2
    }
    New-Item -ItemType Directory -Force -Path (Split-Path $InstallDir) | Out-Null
    $previous = "$InstallDir.previous"
    if (Test-Path $previous) { Remove-Item -Recurse -Force $previous }
    if (Test-Path $InstallDir) { Move-Item $InstallDir $previous }
    try {
        Move-Item $stage $InstallDir
    } catch {
        if (Test-Path $previous) { Move-Item $previous $InstallDir }
        Stop-Install "Could not install to $InstallDir : $($_.Exception.Message)"
    }
    if (Test-Path $previous) { Remove-Item -Recurse -Force $previous }

    if (-not $env:PULSE_INSTALL_NO_SHORTCUT) {   # set only by tests
        Push-Location $InstallDir
        try { & $exe shortcut | Out-Null } finally { Pop-Location }
    }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host "Pulse $Version is installed in $InstallDir" -ForegroundColor Green
Write-Host 'Start it with the Pulse shortcut on your desktop. The first time, sign in with the'
Write-Host 'one-time password shown in the Pulse window, then choose your own password.'
if ($sac -eq 2) {
    Write-Host ''
    Write-Host 'Note: Smart App Control is in evaluation mode. If Windows later switches it on,' -ForegroundColor Yellow
    Write-Host 'it will block this unsigned copy of Pulse until a signed release is installed.' -ForegroundColor Yellow
}
