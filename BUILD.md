# Building and packaging Pulse

Pulse compiles to a single, self-contained executable on Windows, macOS and Linux.

## Requirements

- Go 1.26 or newer (`go version`; `go.mod` requires 1.26 and pins the toolchain)
- Windows 10/11 x64 (for the Windows build)
- go-winres for the icon, version info and manifest: `go install github.com/tc-hib/go-winres@v0.3.3` (`Build.bat` generates them from `winres/winres.json` and the version in `pkg/version/version.go`)
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

`Build.bat` refuses to build when `git status --porcelain` lists anything: changed files and new files that are not ignored by `.gitignore` (a new `.go` file would be compiled in). When git is not installed or the folder is not a git repository (for example an unpacked source zip), the build cannot be matched to a commit and is treated the same way. For a local test only, `set PULSE_ALLOW_DIRTY=1`: the build then prints a large warning and the zips are named `Pulse-<version>-DIRTY.zip` (and `-DIRTY-arm64`, `-DIRTY-x86`). Never ship a `-DIRTY` zip.

With `PULSE_SIGN_PFX`, the three programs (x64, ARM64, x86) are signed in one run of `scripts\Sign-Executable.ps1`, so the certificate password is asked once. The key is loaded in memory only (`EphemeralKeySet`) where Windows supports it.

The version is set in one place: `pkg/version/version.go`. Change it there before a release; the app, `VERSION.txt` and the zip name all follow.

### Binary Stripping & Symbols

Production builds compile with:
```cmd
go build -trimpath -ldflags="-s -w" -o whatsapp-auto.exe ./cmd/whatsapp-auto
```
- `-s`: Strips the symbol table (verified via `go tool nm`: reports `no symbols`).
- `-w`: Strips DWARF debug information.
- `-trimpath`: Strips absolute file system paths from stack traces and binaries.

*Technical Note on Go Binaries:* As designed in the Go runtime architecture, the Go compiler preserves the PC-Line Table (`runtime.pclntab`) in the `.rdata` PE section. This is required for runtime stack unwinding during panics, garbage collection root-finding, and reflection. Low-level PE inspect tools can read function names from this table; this is normal Go runtime behavior and not an unstripped debug build.

### Code signing (needed so Windows does not block the program)

Windows SmartScreen warns about unsigned programs, and **Smart App Control blocks them completely**.

#### 1. Production Releases (Public CA)
For public distribution, obtain a code signing certificate from a recognized Certificate Authority (DigiCert, Sectigo, SSL.com) or Microsoft Azure Trusted Signing.
Set your certificate environment variable and run `Build.bat`. Build.bat refuses a certificate whose chain is not trusted, unless `PULSE_SIGN_ALLOW_SELF_SIGNED=1` is set for a local test build:
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
This generates a local code-signing certificate (`CN=PulseAutomation`), asks for a password and exports `certs\PulseAutomation.cer` and `certs\PulseAutomation.pfx`, registers it in the local `TrustedPublisher` store, and signs `whatsapp-auto.exe` with RFC 3161 DigiCert timestamping.

**Only trust this certificate on your own test machine.** Adding it to Trusted Root means anyone holding the `.pfx` can sign programs that machine trusts. Never ship the `.pfx`, never ask customers to install the `.cer`, and never sign a release with it: on other machines the signature shows as untrusted.

## Other platforms

```bash
GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o whatsapp-auto       ./cmd/whatsapp-auto
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o whatsapp-auto-macos ./cmd/whatsapp-auto
```

On macOS and Linux the data key is protected by file permissions only (no DPAPI equivalent is used), and the "sealed" settings secrets (access token, app secret) are **not encrypted**: they are only encoded and rely on the 0600 permissions of `config.yaml`. Pulse prints a warning about this at every start on those systems.

## One-line installer (`scripts/install.ps1`)

Customers can install Pulse without downloading a zip in the browser:

```powershell
irm https://pulse.yourdomain.com/install.ps1 | iex
```

The script picks the zip for the PC (x64, ARM64 or x86), checks it against the SHA-256 written in the script, installs to `%LOCALAPPDATA%\Programs\Pulse` (no admin rights), closes a running copy normally before replacing it, and creates the desktop shortcut. Customer data in `%APPDATA%\Pulse` is never touched.

Files that PowerShell downloads carry no "downloaded from the internet" mark, so SmartScreen does not show "Windows protected your PC". This does **not** replace code signing: Smart App Control (Windows 11) checks every program and blocks unsigned ones however they arrived, so on such PCs the script stops with an explanation. It never switches any protection off.

For each release:
1. Upload `dist\Pulse-<version>.zip`, `-arm64.zip` and `-x86.zip` to an **https** folder you control.
2. In `scripts/install.ps1` set `$BaseUrl` to that folder, `$Version`, and the three checksums from `dist\*.zip.sha256`.
3. Publish the script itself over https on your own domain. The checksums in it protect the zips; https protects the script, which users run directly.

## Customer package (`whatsapp-auto-exe/`)

`Build.bat` packs exactly these files:

- `whatsapp-auto.exe` (fresh, signed build) and `SHA256SUMS.txt`
- `Run-App.bat`, `Run-Server.bat`, `Run-Unlink.bat`, `Reset-Password.bat`, `Create-Desktop-Shortcut.bat`
- `HOW-TO-RUN.txt`, `BUILD.md`, `SETUP-GUIDE.md`, `COMPLIANCE.md`, `LICENSE`, `contacts.example.csv`
- `Unblock-Pulse.bat`, `config.example.yaml`
- `scripts\Install-Service.ps1`, `scripts\Uninstall-Service.ps1`, `scripts\Stop-Background.ps1`, `scripts\Check-SmartAppControl.bat`
- `docs\meta-whatsapp-setup.md`, `docs\architecture.md`, `docs\security.md`
- `VERSION.txt` (written by the build)

Publish the SHA-256 of `whatsapp-auto.exe` (and of the zip) on the download page as well: a checksum file inside the zip can be swapped along with the program. The signing scripts are developer tools and stay out of the package.

Never ship `config.yaml` (it would give every customer the same password), any `*.db` or `*.json` data files, `.env`, or internal audit and test reports. Pulse creates `config.yaml` and a one-time password on first start.
