# scripts/Install-Service.ps1
# Keeps Pulse running in the background: starts it when you sign in to Windows and
# restarts it if it stops. Remove it again with Uninstall-Service.ps1.

param(
    [string]$BinaryPath = "whatsapp-auto.exe",
    [string]$TaskName = "Pulse",
    [switch]$NoStart   # create the task but do not start Pulse now
)

$ErrorActionPreference = "Stop"

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
$action = New-ScheduledTaskAction -Execute $resolvedExe -Argument "serve" -WorkingDirectory $workDir
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
    -Description "Pulse - keeps automatic replies, scheduled messages and the dashboard running, and restarts Pulse if it stops." `
    -User $env:USERNAME `
    -RunLevel Limited | Out-Null
Write-Host "  -> Task '$TaskName' created for $env:USERNAME." -ForegroundColor Green

if (-not $NoStart) {
    Write-Host "[3/3] Starting Pulse in the background..." -ForegroundColor Yellow
    Start-ScheduledTask -TaskName $TaskName
    Start-Sleep -Seconds 2
    Write-Host "  -> Status: $((Get-ScheduledTask -TaskName $TaskName).State)" -ForegroundColor Green
}

Write-Host "`nDone. Pulse now starts when you sign in to Windows and restarts if it stops." -ForegroundColor Cyan
Write-Host "Dashboard: http://127.0.0.1:8085" -ForegroundColor White
Write-Host "To undo this, run scripts\Uninstall-Service.ps1." -ForegroundColor Gray
