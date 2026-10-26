# scripts/Package-Release.ps1
# Called by Build.bat: packs one customer zip per Windows type from the built programs.
#   dist\Pulse-<version>.zip        64-bit Windows 10/11 on Intel/AMD (most PCs)
#   dist\Pulse-<version>-arm64.zip  Windows 10/11 on ARM (Snapdragon laptops, Surface Pro X)
#   dist\Pulse-<version>-x86.zip    32-bit Windows 10
# Each zip holds only the files customers need, its own whatsapp-auto.exe and SHA256SUMS.txt.
# A developer tool: it is not shipped.

param([Parameter(Mandatory = $true)][string]$Version)

$ErrorActionPreference = 'Stop'
Set-Location (Split-Path -Parent $PSScriptRoot)

$files = 'whatsapp-auto.exe', 'SHA256SUMS.txt', 'HOW-TO-RUN.txt', 'BUILD.md', 'SETUP-GUIDE.md', 'COMPLIANCE.md', 'LICENSE',
    'config.example.yaml', 'contacts.example.csv', 'Run-App.bat', 'Run-Server.bat', 'Run-Unlink.bat', 'Reset-Password.bat',
    'Create-Desktop-Shortcut.bat', 'Unblock-Pulse.bat', 'scripts\Install-Service.ps1', 'scripts\Uninstall-Service.ps1',
    'scripts\Stop-Background.ps1', 'scripts\Check-SmartAppControl.bat', 'docs\meta-whatsapp-setup.md', 'docs\architecture.md',
    'docs\security.md'

$staging = (Resolve-Path 'whatsapp-auto-exe').Path
$extra = Get-ChildItem $staging -Recurse -File | Where-Object { $files -notcontains $_.FullName.Substring($staging.Length + 1) }
if ($extra) { Write-Host ('[NOTE] Not shipped (not on the list): ' + (($extra | ForEach-Object Name) -join ', ')) }

$variants = @(
    @{ Suffix = ''; Exe = 'whatsapp-auto-exe'; Label = 'Windows 64-bit (x64)' },
    @{ Suffix = '-arm64'; Exe = 'dist\exe-arm64'; Label = 'Windows on ARM (ARM64)' },
    @{ Suffix = '-x86'; Exe = 'dist\exe-x86'; Label = 'Windows 32-bit (x86)' }
)

Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
$dist = (Resolve-Path 'dist').Path
foreach ($v in $variants) {
    $name = "Pulse-$Version$($v.Suffix)"
    $stage = Join-Path $dist $name
    if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
    New-Item -ItemType Directory -Force (Join-Path $stage 'scripts'), (Join-Path $stage 'docs') | Out-Null
    foreach ($f in $files) {
        if (Test-Path (Join-Path $staging $f)) { Copy-Item (Join-Path $staging $f) (Join-Path $stage $f) }
    }
    # This variant's own program and checksum replace the x64 ones.
    foreach ($f in 'whatsapp-auto.exe', 'SHA256SUMS.txt') {
        $src = Join-Path $v.Exe $f
        if (-not (Test-Path $src)) { throw "$src is missing: build it first." }
        Copy-Item $src (Join-Path $stage $f) -Force
    }
    Set-Content -Path (Join-Path $stage 'VERSION.txt') -Value ("Pulse $Version", $v.Label) -Encoding ascii

    $zip = Join-Path $dist "$name.zip"
    if (Test-Path $zip) { Remove-Item -Force $zip }
    $za = [System.IO.Compression.ZipFile]::Open($zip, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($fi in (Get-ChildItem $stage -Recurse -File)) {
            $entry = $fi.FullName.Substring($stage.Length + 1).Replace([char]92, [char]47)
            [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($za, $fi.FullName, $entry, [System.IO.Compression.CompressionLevel]::Optimal)
        }
    } finally { $za.Dispose() }
    $h = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower()
    Set-Content -Path "$zip.sha256" -Value "$h  $name.zip" -Encoding ascii
    Write-Host "Created $zip ($($v.Label))"
}
