# scripts/Stop-Background.ps1
# Closes a running Pulse normally, so messages are saved and the WhatsApp session file
# is encrypted. Only if it does not close within 20 seconds is it stopped by force.

$program = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $program "whatsapp-auto.exe"

if (-not (Get-Process -Name "whatsapp-auto" -ErrorAction SilentlyContinue)) {
    Write-Host "Pulse is not running." -ForegroundColor Gray
    exit 0
}

if (Test-Path $exe) {
    Write-Host "Asking Pulse to close and save everything..." -ForegroundColor Cyan
    Push-Location $program
    & $exe stop
    Pop-Location
}

for ($i = 0; $i -lt 20 -and (Get-Process -Name "whatsapp-auto" -ErrorAction SilentlyContinue); $i++) {
    Start-Sleep -Seconds 1
}

$left = Get-Process -Name "whatsapp-auto" -ErrorAction SilentlyContinue
if ($left) {
    Write-Host "Pulse did not close by itself; stopping it now. Changes from the last second may be lost." -ForegroundColor Yellow
    $left | Stop-Process -Force
}
Write-Host "Pulse is stopped." -ForegroundColor Green
