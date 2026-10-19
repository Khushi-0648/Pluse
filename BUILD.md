# Building Pulse WhatsApp Automation (D4)

This document provides instructions for compiling, testing, and packaging the WhatsApp Automation platform across Windows, Linux, and macOS.

---

## 1. Prerequisites

* **Go Compiler**: Go 1.22+ or newer installed and available in your system `PATH`. Verify via `go version`.
* **Cgo / External Compilers**: **Not required**. The project uses `modernc.org/sqlite`, a pure Go translation of SQLite, eliminating C compiler (GCC/MinGW) requirements.
* **Operating Systems Supported**:
  * Windows 10 / 11 / Server 2019+
  * Linux (Debian, Ubuntu, Fedora, Arch)
  * macOS (Apple Silicon M-series & Intel x86_64)

---

## 2. Running Automated Tests

Run the full end-to-end audit test suite:
```bash
go test -v ./...
```

Run specific test packages or audit suites:
```bash
# Security & E2E audit suite (tests 14 distinct security controls)
go test -v -run TestE2E_FullAuditSuite ./pkg/server

# Bot rule matching, LID fallbacks & agent handover tests
go test -v ./pkg/bot

# Contacts, CSV import metrics & persistence tests
go test -v ./pkg/contacts

# Security header, password complexity & host validation tests
go test -v ./pkg/security
```

Static analysis and lint check:
```bash
go vet ./...
```

---

## 3. Production Build Instructions

To ensure no workstation directory structures, local usernames, or debug symbols leak into the compiled executable (Audit Finding S10), build using `-trimpath` and `-ldflags="-s -w"`:

### Windows (Executable):
```cmd
go build -trimpath -ldflags="-s -w" -o whatsapp-auto.exe ./cmd/whatsapp-auto
```

### Linux (Binary):
```bash
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o whatsapp-auto ./cmd/whatsapp-auto
```

### macOS (Apple Silicon / Intel):
```bash
# Apple Silicon (M1/M2/M3/M4)
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o whatsapp-auto-darwin-arm64 ./cmd/whatsapp-auto

# Intel x86_64
GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o whatsapp-auto-darwin-amd64 ./cmd/whatsapp-auto
```

---

## 4. Multi-Session & Multi-Number Execution (C15)

The binary supports running multiple independent WhatsApp numbers concurrently on different ports with isolated session databases:

### Primary Session (Default: port 8085, `whatsapp.db`):
```cmd
whatsapp-auto.exe app --config config.yaml --port 8085
```

### Secondary Session (e.g. Sales Team: port 8086, `whatsapp-sales.db`):
```cmd
whatsapp-auto.exe app --session sales --port 8086
```

### Support Session (e.g. Support Team: port 8087, `whatsapp-support.db`):
```cmd
whatsapp-auto.exe app --session support --port 8087
```

Each session automatically creates isolated data files:
* Database: `whatsapp-<session>.db`
* Contacts: `contacts-<session>.json`
* Message History: `messages-<session>.json`
* Scheduled Tasks: `scheduled-<session>.json`

---

## 5. Binary Verification Checklist

Verify that sensitive developer paths were stripped:
```cmd
strings whatsapp-auto.exe | findstr /I "Users\\satya"
```
The command should return zero matches, confirming full privacy compliance.

---

## 6. Packaging Customer Release (`whatsapp-auto-exe/`)

To package a clean, production-ready bundle for customer distribution:
1. Ensure the customer release folder `whatsapp-auto-exe/` contains:
   * `whatsapp-auto.exe` (freshly compiled production binary)
   * `config.yaml` (clean template configuration)
   * `contacts.csv` (sample CSV format for broadcast campaigns)
   * `Run-App.bat` (one-click desktop app launcher)
   * `Run-Server.bat` (headless background service launcher)
   * `Create-Desktop-Shortcut.bat` (desktop shortcut installer)
   * `HOW-TO-RUN.txt` (quick start guide)
2. **Never package**:
   * `whatsapp.db` (contains live cryptographic pairing keys)
   * `contacts.json` (contains live customer CRM records)
   * `messages.json` (contains customer message logs)
   * `.env` (contains sensitive API keys)
