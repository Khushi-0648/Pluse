# Building and packaging Pulse

Pulse compiles to a single, self-contained executable on Windows, macOS and Linux.

## Requirements

- Go 1.22 or newer (`go version`)
- Windows 10/11 x64 (for the Windows build)
- Optional: Node.js (for syntax-checking dashboard scripts during `Build.bat`)
- Optional: Code-signing certificate (OV/EV or Microsoft Trusted Signing for production releases; self-signed for local testing)

## Quick build (development)

```cmd
go build ./cmd/whatsapp-auto
```

This creates `whatsapp-auto.exe` in the current directory. To test the dashboard:

```cmd
go run -tags uismoke ./cmd/uismoke
```

## Release build (Windows)

```cmd
Build.bat
```

`Build.bat` runs the checks and tests, builds `whatsapp-auto-exe\whatsapp-auto.exe` with `-trimpath -ldflags="-s -w"`, signs it when a certificate is configured, and writes `dist\Pulse-<version>.zip` plus a `.sha256` checksum file. The zip contains only the files listed under *Customer package* below and a `VERSION.txt`; anything else in `whatsapp-auto-exe\` is left out and named in a note. Always ship the zip from `dist\`, never a hand-made zip of the folder.

The version is set in one place: `pkg/version/version.go`. Change it there before a release; the app, `VERSION.txt` and the zip name all follow.

### Binary Stripping & Symbols

Production builds compile with:
```cmd
go build -trimpath -ldflags="-s -w" -o whatsapp-auto.exe ./cmd/whatsapp-auto
```
- `-s`: Strips the DWARF symbol table (verified via `go tool nm`: reports `no symbols`).
- `-w`: Strips DWARF debug symbols.
- `-trimpath`: Strips absolute file system paths from stack traces and binaries.

*Technical Note on Go Binaries:* As designed in the Go runtime architecture, the Go compiler preserves the PC-Line Table (`runtime.pclntab`) in the `.rdata` PE section. This is required for runtime stack unwinding during panics, garbage collection root-finding, and reflection. Low-level PE inspect tools can read function names from this table; this is normal Go runtime behavior and not an unstripped debug build.

### Code signing (needed so Windows does not block the program)

Windows SmartScreen warns about unsigned programs, and **Smart App Control blocks them completely**.

#### 1. Production Releases (Public CA)
For public distribution, obtain a code signing certificate from a recognized Certificate Authority (DigiCert, Sectigo, SSL.com) or Microsoft Azure Trusted Signing.
Set your certificate environment variable and run `Build.bat`:
```cmd
set PULSE_SIGN_PFX=C:\certs\pulse.pfx
rem or by installed certificate thumbprint:
set PULSE_SIGN_THUMBPRINT=0123ABCD...
Build.bat
```
Alternatively, sign directly via Microsoft SignTool:
```cmd
signtool.exe sign /f "C:\path\pulse.pfx" /p "CertPassword" /tr http://timestamp.digicert.com /td SHA256 /fd SHA256 "whatsapp-auto-exe\whatsapp-auto.exe"
signtool.exe verify /pa "whatsapp-auto-exe\whatsapp-auto.exe"
```

#### 2. Local Testing (Self-Signed Dev Certificate)
For internal testing without a commercial certificate, run:
```cmd
powershell -ExecutionPolicy Bypass -File scripts\Create-DevCertificate.ps1
```
This generates a local code-signing certificate (`CN=PulseAutomation`), exports `certs\PulseAutomation.cer` and `certs\PulseAutomation.pfx` (password: `PulseDev123!`), registers it in the local `TrustedPublisher` store, and signs `whatsapp-auto.exe` with RFC 3161 DigiCert timestamping.

To eliminate SmartScreen warnings on the local test machine, import the public certificate into the Windows Trusted Root store:
```cmd
certutil -addstore -user root certs\PulseAutomation.cer
```

## Other platforms

```bash
GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o whatsapp-auto       ./cmd/whatsapp-auto
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o whatsapp-auto-macos ./cmd/whatsapp-auto
```

On macOS and Linux the data key is protected by file permissions only (no DPAPI equivalent is used).

## Customer package (`whatsapp-auto-exe/`)

`Build.bat` packs exactly these files:

- `whatsapp-auto.exe` (fresh, signed build) and `SHA256SUMS.txt`
- `Run-App.bat`, `Run-Server.bat`, `Run-Unlink.bat`, `Reset-Password.bat`, `Create-Desktop-Shortcut.bat`
- `HOW-TO-RUN.txt`, `BUILD.md`, `SETUP-GUIDE.md`, `COMPLIANCE.md`, `LICENSE`, `contacts.example.csv`
- `scripts\Install-Service.ps1`, `scripts\Uninstall-Service.ps1`, `scripts\Stop-Background.ps1`, `scripts\Sign-Executable.ps1`, `scripts\Create-DevCertificate.ps1`
- `docs\meta-whatsapp-setup.md`, `docs\architecture.md`, `docs\security.md`
- `VERSION.txt` (written by the build)

Never ship `config.yaml` (it would give every customer the same password), any `*.db` or `*.json` data files, `.env`, or internal audit and test reports. Pulse creates `config.yaml` and a one-time password on first start.
