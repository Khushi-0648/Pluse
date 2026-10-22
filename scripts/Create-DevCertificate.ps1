# scripts/Create-DevCertificate.ps1
# Creates a self-signed code-signing certificate for local testing,
# exports .cer / .pfx, adds to TrustedPublisher, and signs whatsapp-auto.exe.

param(
    [string]$SubjectName = "PulseAutomation",
    [string]$ExportDir = (Join-Path (Split-Path -Parent $PSScriptRoot) "certs"),
    [string]$PfxPassword = "PulseDev123!",
    [switch]$SignBinary = $true,
    [string]$BinaryPath = (Join-Path (Split-Path -Parent $PSScriptRoot) "whatsapp-auto-exe\whatsapp-auto.exe")
)

$ErrorActionPreference = "Stop"

Write-Host "=== Creating Code Signing Certificate: CN=$SubjectName ===" -ForegroundColor Cyan

# 1. Check if an active certificate already exists or create a new one
$existing = Get-ChildItem Cert:\CurrentUser\My -CodeSigningCert | Where-Object { $_.Subject -eq "CN=$SubjectName" -and $_.NotAfter -gt (Get-Date) } | Select-Object -First 1

if ($existing) {
    Write-Host "Found existing valid certificate in Cert:\CurrentUser\My:" -ForegroundColor Yellow
    $cert = $existing
} else {
    Write-Host "Generating fresh self-signed code signing certificate..." -ForegroundColor Gray
    $cert = New-SelfSignedCertificate -Type CodeSigningCert `
        -Subject "CN=$SubjectName" `
        -CertStoreLocation "Cert:\CurrentUser\My" `
        -NotAfter (Get-Date).AddYears(3) `
        -KeyExportPolicy Exportable `
        -KeySpec Signature `
        -KeyLength 2048 `
        -HashAlgorithm SHA256
}

Write-Host "Certificate Details:" -ForegroundColor Green
Write-Host "  Subject:    $($cert.Subject)"
Write-Host "  Thumbprint: $($cert.Thumbprint)"
Write-Host "  Expires:    $($cert.NotAfter)"

# 2. Export public .cer and private .pfx
if ($ExportDir) {
    if (-not (Test-Path $ExportDir)) {
        New-Item -ItemType Directory -Path $ExportDir -Force | Out-Null
    }
    
    # Export public CER
    $cerPath = Join-Path $ExportDir "$SubjectName.cer"
    [System.IO.File]::WriteAllBytes($cerPath, $cert.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Cert))
    Write-Host "  Public cert (.cer): $cerPath" -ForegroundColor Gray

    # Export PFX with private key
    $pfxPath = Join-Path $ExportDir "$SubjectName.pfx"
    $pfxBytes = $cert.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Pfx, $PfxPassword)
    [System.IO.File]::WriteAllBytes($pfxPath, $pfxBytes)
    Write-Host "  Private key (.pfx): $pfxPath (Password: $PfxPassword)" -ForegroundColor Gray
}

# 3. Add to TrustedPublisher store
try {
    $cerPath = Join-Path $ExportDir "$SubjectName.cer"
    & certutil -addstore -user TrustedPublisher $cerPath | Out-Null
    Write-Host "  Added to TrustedPublisher store." -ForegroundColor Green
} catch {
    Write-Warning "Could not register to TrustedPublisher: $($_.Exception.Message)"
}

# 4. Sign binary if requested
if ($SignBinary -and (Test-Path $BinaryPath)) {
    Write-Host "`nSigning executable: $BinaryPath" -ForegroundColor Cyan
    $tsUrl = "http://timestamp.digicert.com"
    try {
        $sig = Set-AuthenticodeSignature -FilePath $BinaryPath -Certificate $cert -HashAlgorithm SHA256 -TimestampServer $tsUrl -IncludeChain All -ErrorAction Stop
    } catch {
        Write-Warning "Timestamp server unreachable, signing without timestamp: $($_.Exception.Message)"
        $sig = Set-AuthenticodeSignature -FilePath $BinaryPath -Certificate $cert -HashAlgorithm SHA256
    }
    
    Write-Host "  Signature Status:  $($sig.Status)" -ForegroundColor Green
    Write-Host "  Status Message:    $($sig.StatusMessage)" -ForegroundColor Gray
    
    # Update SHA256SUMS.txt
    $hash = (Get-FileHash -Path $BinaryPath -Algorithm SHA256).Hash.ToLower()
    $sums = Join-Path (Split-Path -Parent $BinaryPath) "SHA256SUMS.txt"
    "$hash  $([System.IO.Path]::GetFileName($BinaryPath))" | Set-Content -Path $sums -Encoding ascii
    Write-Host "  Updated checksum:  $sums" -ForegroundColor Gray
    Write-Host "  Binary SHA-256:    $hash" -ForegroundColor Gray
}

Write-Host "`nLocal certificate created and executable signed successfully." -ForegroundColor Green
Write-Host "To trust this self-signed certificate on your Windows machine:" -ForegroundColor Cyan
Write-Host "  Right-click 'certs\$SubjectName.cer' > 'Install Certificate' > 'Local Machine' > 'Place in: Trusted Root Certification Authorities'" -ForegroundColor Gray
