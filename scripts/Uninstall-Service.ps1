# scripts/Uninstall-Service.ps1
# Removes the background task that keeps Pulse running, after closing Pulse normally.

param(
    [string]$TaskName = "Pulse"
)

Write-Host "Removing the background task '$TaskName'..." -ForegroundColor Yellow
# Close Pulse normally (saves everything) before the task is gone for good.
& (Join-Path $PSScriptRoot "Stop-Background.ps1")
Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue

Write-Host "Pulse will no longer start in the background. Your data was kept." -ForegroundColor Green
