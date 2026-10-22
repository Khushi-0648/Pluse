# scripts/Sign-Executable.ps1
# Signs whatsapp-auto.exe with a code-signing certificate and writes SHA256SUMS.txt.
#
# Windows (SmartScreen and Smart App Control) automatically trusts programs signed with a
# certificate from a public certificate authority (or Microsoft Azure Trusted Signing).
# Self-signed or private CA certificates require installation into the local Trusted Root /
# Trusted Publishers store.
#
# Ways to sign:
#   - Commercial OV/EV code-signing certificate (DigiCert, Sectigo, SSL.com):
#       .\scripts\Sign-Executable.ps1 -PfxPath C:\certs\pulse.pfx
#   - Installed certificate by thumbprint:
#       .\scripts\Sign-Executable.ps1 -Thumbprint B76B717440F111E5CFE3D54E743EAA54C573AFD7
#   - Self-signed / local test certificate:
#       .\scripts\Sign-Executable.ps1 -Thumbprint <THUMBPRINT> -AllowSelfSigned

param(
    [string]$BinaryPath = (Join-Path (Split-Path -Parent $PSScriptRoot) "whatsapp-auto-exe\whatsapp-auto.exe"),
    [string]$PfxPath,
    [string]$Thumbprint,
    [string]$TimestampServer = "http://timestamp.digicert.com",
    [switch]$AllowSelfSigned,
    [switch]$NoTimestamp
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $BinaryPath)) {
    Write-Error "Program not found: $BinaryPath. Run Build.bat first."
    exit 1
}
$BinaryPath = (Resolve-Path $BinaryPath).Path

if ($PfxPath) {
    $pw = Read-Host "Password for $PfxPath" -AsSecureString
    $PfxPath = (Resolve-Path $PfxPath).Path
    $flags = [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]'UserKeySet,PersistKeySet'
    try {
        $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($PfxPath, $pw, $flags)
    } catch {
        Write-Error "Could not open $PfxPath. Check the password. ($($_.Exception.InnerException.Message))"
        exit 1
    }
} elseif ($Thumbprint) {
    $cert = Get-ChildItem Cert:\CurrentUser\My, Cert:\LocalMachine\My -CodeSigningCert |
        Where-Object { $_.Thumbprint -eq $Thumbprint.Replace(" ", "").ToUpper() } | Select-Object -First 1
} else {
    Write-Error "Give the certificate with -PfxPath or -Thumbprint. Example: .\scripts\Sign-Executable.ps1 -Thumbprint <THUMBPRINT>"
    exit 1
}
if (-not $cert) { Write-Error "Certificate not found."; exit 1 }
if (-not $cert.HasPrivateKey) { Write-Error "The certificate has no private key, so it cannot sign."; exit 1 }

if ($cert.Subject -eq $cert.Issuer) {
    if ($AllowSelfSigned) {
        Write-Warning "Certificate is self-signed. Windows SmartScreen may still warn unless imported into the local machine's Trusted Root store."
    } else {
        Write-Warning "This certificate is self-signed. If you want to use it for testing, re-run with -AllowSelfSigned."
        exit 1
    }
} else {
    $chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
    if (-not $chain.Build($cert) -and -not $AllowSelfSigned) {
        $why = ($chain.ChainStatus | ForEach-Object { $_.StatusInformation.Trim() }) -join "; "
        Write-Warning "Certificate chain not trusted by default root store: $why"
    }
}

if ($cert.NotAfter -lt (Get-Date)) { Write-Error "The certificate expired on $($cert.NotAfter)."; exit 1 }

Write-Host "Signing $BinaryPath" -ForegroundColor Cyan
Write-Host "  with: $($cert.Subject)" -ForegroundColor Gray

if (-not $NoTimestamp) {
    try {
        $sig = Set-AuthenticodeSignature -FilePath $BinaryPath -Certificate $cert -HashAlgorithm SHA256 -TimestampServer $TimestampServer -IncludeChain All -ErrorAction Stop
    } catch {
        Write-Warning "Timestamp server unreachable. Signing without timestamp..."
        $sig = Set-AuthenticodeSignature -FilePath $BinaryPath -Certificate $cert -HashAlgorithm SHA256
    }
} else {
    $sig = Set-AuthenticodeSignature -FilePath $BinaryPath -Certificate $cert -HashAlgorithm SHA256
}

Write-Host "  -> Signature status: $($sig.Status)" -ForegroundColor Green

# Update checksum file
$hash = (Get-FileHash -Path $BinaryPath -Algorithm SHA256).Hash.ToLower()
$sums = Join-Path (Split-Path -Parent $BinaryPath) "SHA256SUMS.txt"
"$hash  $([IO.Path]::GetFileName($BinaryPath))" | Set-Content -Path $sums -Encoding ascii
Write-Host "  -> SHA-256 written to $sums" -ForegroundColor Green
