# scripts/Install-Service.ps1
# Keeps Pulse running in the background: starts it when you sign in to Windows and
# restarts it if it stops. Remove it again with Uninstall-Service.ps1.

param(
    [string]$BinaryPath = "whatsapp-auto.exe",
    [string]$TaskName = "Pulse",
    [switch]$NoStart   # create the task but do not start Pulse now
)

$ErrorActionPreference = "Stop"

# The task runs for the account that runs this script. Run it from your own account
# without "Run as administrator", or the task may belong to another account.
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Warning "Running as administrator. The task is created for $env:USERNAME; make sure that is the account that uses Pulse."
}

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host " Pulse - run in the background" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# The program folder is the one above this scripts folder, so the script works no
# matter which folder it is started from.
$workDir = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path (Join-Path $workDir $BinaryPath))) { $workDir = (Get-Location).Path }
$resolvedExe = Join-Path $workDir $BinaryPath
if (-not (Test-Path $resolvedExe)) {
    Write-Error "whatsapp-auto.exe was not found in $workDir. Keep this script in the scripts folder next to whatsapp-auto.exe."
    exit 1
}

Write-Host "Program: $resolvedExe" -ForegroundColor Gray

$existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($existing) {
    Write-Host "[1/3] Replacing the existing background task..." -ForegroundColor Yellow
    & (Join-Path $PSScriptRoot "Stop-Background.ps1")   # closes Pulse normally so nothing is lost
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
} else {
    Write-Host "[1/3] No background task yet." -ForegroundColor Yellow
}

Write-Host "[2/3] Creating the background task..." -ForegroundColor Yellow
$action = New-ScheduledTaskAction -Execute $resolvedExe -Argument "serve -keep-running" -WorkingDirectory $workDir
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit (New-TimeSpan -Days 0) `
    -RestartCount 5 `
    -RestartInterval (New-TimeSpan -Minutes 1) `
    -StartWhenAvailable `
    -MultipleInstances IgnoreNew

Register-ScheduledTask `
    -TaskName $TaskName `
    -Action $action `
    -Trigger $trigger `
    -Settings $settings `
    -Description "Pulse - keeps automatic replies, scheduled messages and the dashboard running, and starts Pulse again if it crashes." `
    -User $env:USERNAME `
    -RunLevel Limited | Out-Null
Write-Host "  -> Task '$TaskName' created for $env:USERNAME." -ForegroundColor Green

if (-not $NoStart) {
    Write-Host "[3/3] Starting Pulse in the background..." -ForegroundColor Yellow
    Start-ScheduledTask -TaskName $TaskName
    Start-Sleep -Seconds 2
    Write-Host "  -> Status: $((Get-ScheduledTask -TaskName $TaskName).State)" -ForegroundColor Green
}

$port = 8085
$cfg = Join-Path $env:APPDATA "Pulse\config.yaml"
if (Test-Path $cfg) {
    $m = Select-String -Path $cfg -Pattern '^\s+port:\s*(\d+)' | Select-Object -First 1
    if ($m) { $port = $m.Matches[0].Groups[1].Value }
}
Write-Host "`nDone. Pulse now starts when you sign in to Windows and starts again if it crashes." -ForegroundColor Cyan
Write-Host "Dashboard: http://127.0.0.1:$port" -ForegroundColor White
Write-Host "To undo this, run scripts\Uninstall-Service.ps1." -ForegroundColor Gray
