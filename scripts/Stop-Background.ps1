# scripts/Stop-Background.ps1
# Closes the Pulse started from this folder normally, so messages are saved. Only if it
# does not close within 20 seconds is it stopped by force. Other copies are left alone.

$program = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $program "whatsapp-auto.exe"
function Get-Pulse { Get-Process -Name "whatsapp-auto" -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exe } }

if (-not (Get-Pulse)) {
    Write-Host "Pulse is not running." -ForegroundColor Gray
    exit 0
}

if (Test-Path $exe) {
    Write-Host "Asking Pulse to close and save everything..." -ForegroundColor Cyan
    Push-Location $program
    & $exe stop
    Pop-Location
}

for ($i = 0; $i -lt 20 -and (Get-Pulse); $i++) {
    Start-Sleep -Seconds 1
}

$left = Get-Pulse
if ($left) {
    Write-Host "Pulse did not close by itself; stopping it now. Changes from the last second may be lost." -ForegroundColor Yellow
    $left | Stop-Process -Force
}
Write-Host "Pulse is stopped." -ForegroundColor Green
