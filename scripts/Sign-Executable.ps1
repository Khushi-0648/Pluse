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
#   - Several programs with one password prompt (Build.bat signs x64, ARM64 and x86 so):
#       .\scripts\Sign-Executable.ps1 -PfxPath C:\certs\pulse.pfx -BinaryPath a\whatsapp-auto.exe,b\whatsapp-auto.exe

param(
    [string[]]$BinaryPath = @(Join-Path (Split-Path -Parent $PSScriptRoot) "whatsapp-auto-exe\whatsapp-auto.exe"),
    [string]$PfxPath,
    [string]$Thumbprint,
    [string]$TimestampServer = "http://timestamp.digicert.com",
    [switch]$AllowSelfSigned,
    [switch]$NoTimestamp
)

$ErrorActionPreference = "Stop"

# "powershell -File" passes "a,b,c" as one string, so a comma-separated list is split here.
$BinaryPath = @($BinaryPath | ForEach-Object { $_ -split ',' } | ForEach-Object { $_.Trim() } | Where-Object { $_ })
if ($BinaryPath.Count -eq 1 -and -not (Test-Path $BinaryPath[0])) {
    # Customer package layout: the program sits next to the scripts folder.
    $BinaryPath = @(Join-Path (Split-Path -Parent $PSScriptRoot) "whatsapp-auto.exe")
}
$resolved = @()
foreach ($b in $BinaryPath) {
    if (-not (Test-Path $b)) {
        Write-Error "Program not found: $b. Run Build.bat first."
        exit 1
    }
    $resolved += (Resolve-Path $b).Path
}
$BinaryPath = $resolved

# Loads the .pfx. EphemeralKeySet keeps the private key in memory only, so no copy of it
# is left in the user's key store; older .NET versions do not have it, and then the
# default flags are used.
function Open-Pfx([string]$path, [System.Security.SecureString]$password, [bool]$ephemeral) {
    $type = [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]
    if ($ephemeral) {
        if (-not [Enum]::IsDefined($type, 'EphemeralKeySet')) { return $null }
        $flags = [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::EphemeralKeySet
    } else {
        $flags = [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::DefaultKeySet
    }
    return New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($path, $password, $flags)
}

$pw = $null
$ephemeralCert = $false
if ($PfxPath) {
    # Asked once, however many programs are signed.
    $pw = Read-Host "Password for $PfxPath" -AsSecureString
    $PfxPath = (Resolve-Path $PfxPath).Path
    try {
        $cert = Open-Pfx $PfxPath $pw $true
        $ephemeralCert = [bool]$cert
    } catch {
        $cert = $null # retried below with the default flags; a wrong password fails there too
    }
    if (-not $cert) {
        try {
            $cert = Open-Pfx $PfxPath $pw $false
        } catch {
            Write-Error "Could not open $PfxPath. Check the password. ($($_.Exception.InnerException.Message))"
            exit 1
        }
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
        Write-Error "Certificate chain is not trusted: $why. Use a public CA certificate, or -AllowSelfSigned for local tests."
        exit 1
    }
}

if ($cert.NotAfter -lt (Get-Date)) { Write-Error "The certificate expired on $($cert.NotAfter)."; exit 1 }

Write-Host "Signing with: $($cert.Subject)" -ForegroundColor Gray

function Sign-One([string]$file, $withCert) {
    if ($NoTimestamp) {
        return Set-AuthenticodeSignature -FilePath $file -Certificate $withCert -HashAlgorithm SHA256 -ErrorAction Stop
    }
    try {
        return Set-AuthenticodeSignature -FilePath $file -Certificate $withCert -HashAlgorithm SHA256 -TimestampServer $TimestampServer -IncludeChain All -ErrorAction Stop
    } catch {
        # Without a timestamp the signature stops being valid when the certificate
        # expires, so a release must not be signed that way by accident.
        throw "Signing or the timestamp server $TimestampServer failed ($($_.Exception.Message)). Try again later, or use -NoTimestamp for a local test build only."
    }
}

foreach ($file in $BinaryPath) {
    Write-Host "Signing $file" -ForegroundColor Cyan
    $sig = $null
    try {
        $sig = Sign-One $file $cert
    } catch {
        $why = "$_"
    }
    if ((-not $sig -or $sig.Status -ne "Valid") -and $ephemeralCert) {
        # Some Windows signing components cannot use an in-memory key: load the
        # certificate with the default flags and try once more.
        Write-Warning "Signing with an in-memory key did not work; retrying with the default key storage."
        $ephemeralCert = $false
        $cert = Open-Pfx $PfxPath $pw $false
        $sig = $null
        try { $sig = Sign-One $file $cert } catch { $why = "$_" }
    }
    if (-not $sig) {
        Write-Error $why
        exit 1
    }

    Write-Host "  -> Signature status: $($sig.Status)" -ForegroundColor Green
    if ($sig.Status -ne "Valid" -and -not $AllowSelfSigned) {
        Write-Error "Signing did not produce a valid signature: $($sig.StatusMessage)"
        exit 1
    }

    # Update checksum file
    $hash = (Get-FileHash -Path $file -Algorithm SHA256).Hash.ToLower()
    $sums = Join-Path (Split-Path -Parent $file) "SHA256SUMS.txt"
    "$hash  $([IO.Path]::GetFileName($file))" | Set-Content -Path $sums -Encoding ascii
    Write-Host "  -> SHA-256 written to $sums" -ForegroundColor Green
}
