# Dependency Inventory & Upstream Synchronization Governance Plan (D14)

## 1. Direct Dependency Inventory

| Package | Version | Purpose | Upstream Source & Security Profile |
| :--- | :--- | :--- | :--- |
| `go.mau.fi/whatsmeow` | `v0.0.0-20261013142727` | WhatsApp Multi-Device Protocol Implementation | Maintained by tulir / mau.fi; provides Signal protocol end-to-end encryption and noise handshake. |
| `modernc.org/sqlite` | `v1.60.1` | Embedded SQLite Database Engine (Cgo-free) | Pure Go translation of SQLite; eliminates Cgo compile dependencies and cross-platform native runtime issues. |
| `golang.org/x/crypto` | `v0.57.0` | Bcrypt password hashing & cryptographic primitives | Official Go cryptographic extensions from Google; vetted for constant-time comparisons. |
| `gopkg.in/yaml.v3` | `v3.0.1` | YAML configuration parser and serializer | Standard safe YAML deserializer with strict type enforcement. |
| `rsc.io/qr` | `v0.2.0` | Native QR code encoding and PNG generation | Lightweight, pure Go QR code matrix generator by Russ Cox; zero external dependencies. |
| `google.golang.org/protobuf` | `v1.36.12` | Protocol Buffers serialization | Official Google Protocol Buffers implementation for WhatsApp message schema definitions. |

---

## 2. Upstream WhatsApp (`whatsmeow`) Synchronization Protocol

WhatsApp frequently rolls out server-side protocol migrations, presence packet shifts, and multi-device connection handshakes. To prevent silent connection degradation:

### A. Monthly Upstream Sync Review
1. **Repository Tracking**: Monitor releases and master commits of `go.mau.fi/whatsmeow` at `https://github.com/tulir/whatsmeow`.
2. **Protocol Version Audits**: When WhatsApp deploys new client versions (e.g. `2.3000.x`), pull the latest `whatsmeow` commit:
   ```bash
   go get -u go.mau.fi/whatsmeow
   go mod tidy
   ```
3. **Changelog Triage**: Review any breaking API changes or modifications to event handlers (`events.Message`, `events.Connected`, `events.LoggedOut`).

### B. Emergency Protocol Patch Procedure
If WhatsApp pushes a mandatory server update that breaks websocket pairing:
1. Check the `whatsmeow` issue tracker for hotfixes and protocol overrides.
2. Update `go.mod` to the tagged hotfix commit immediately.
3. Run the automated integration test suite:
   ```bash
   go test -v ./pkg/client ./pkg/bot ./pkg/server
   ```
4. Rebuild the production binary with symbols stripped:
   ```cmd
   go build -trimpath -ldflags="-s -w" -o whatsapp-auto.exe ./cmd/whatsapp-auto
   ```
5. Deploy the hotfixed binary to `whatsapp-auto-exe/`.

---

## 3. Vulnerability Scanning & Tooling

To ensure zero known vulnerabilities enter production:

1. **Static Vulnerability Scans (`govulncheck`)**:
   Run `govulncheck` prior to every release:
   ```bash
   go install golang.org/x/vuln/cmd/govulncheck@latest
   govulncheck ./...
   ```
   Any high or critical CVE must be patched before distribution.

2. **Module Graph Audit**:
   Inspect transitive dependency health quarterly:
   ```bash
   go list -m -u all
   ```

3. **Cryptographic Checksum Verification**:
   All dependencies and sub-dependencies are locked in `go.sum` with SHA-256 hashes. Module downloading through untrusted proxies is disabled (`GOPROXY=https://proxy.golang.org,direct`).

---

## 4. Pre-Deployment Validation Checklist

Before releasing any dependency bump to production:
- [ ] `go test -v ./...` passes 100% green without panics or memory leaks.
- [ ] QR code generation renders crisp SVG/PNG without encoding errors.
- [ ] Phone pairing code generation succeeds with valid country codes.
- [ ] Bi-directional text messaging, media receiving, and auto-replies operate without latency regressions.
- [ ] Database schema migrations execute seamlessly against `whatsapp.db`.
- [ ] Host-level security checks and CSRF protections remain intact.
