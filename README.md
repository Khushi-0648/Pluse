# Pulse WhatsApp Automation Platform (Go)

A high-performance, enterprise-grade autonomous **WhatsApp Automation Platform** built with **Go (Golang)** and [whatsmeow](https://github.com/tulir/whatsmeow) (WhatsApp Multi-Device Web Protocol).

It allows organizations to automate customer support, notifications, and interactive chat workflows **without Meta Cloud API fees, per-conversation surcharges, or approval bottlenecks**.

---

## ✨ Enterprise Capabilities
* **Two-Way Live Team Inbox**: Real-time WhatsApp-style chat interface with message bubbles, status indicators, and 1-click Canned Quick Reply chips.
* **Human Agent Handover**: Automatically pauses bot auto-replies for 24 hours when a customer requests a live agent (`option_agent`, `support`, `human`).
* **Meta & Anti-Ban Compliance**: Full support for `STOP` / `UNSUBSCRIBE` opt-outs, randomized jitter delays (4–10s), warmup mode, and automatic circuit breaker aborts after 5 consecutive delivery failures.
* **Media & PDF Document Dispatch**: Dispatch images (PNG, JPEG, WebP) and documents (PDF, DOCX) up to 16 MB with automatic MIME-type validation.
* **Inbound & Outbound CRM Webhooks**: Real-time integration with Shopify, Zapier, Make.com, n8n, and custom backends with HMAC-SHA256 signature signing and SSRF protection.
* **Google Gemini AI Assistant**: Automatic conversational fallback when no fixed keyword rules match.
* **Scheduled Reminders**: Schedule follow-ups and appointment reminders for specific future timestamps.
* **GDPR & DPDP Privacy Tools**: Automated message retention purges (30/60/90 days) and full customer data export bundle.
* **Hardened Security**: Bcrypt master password hashing, localhost-only binding (`127.0.0.1`), CSP and security headers, double-submit CSRF protection, and system audit trail.

---

## 🚀 Quick Start
### 1. Launch the Desktop App
Double-click `whatsapp-auto.exe` or `Run-App.bat`. This automatically starts the secure engine and launches the native standalone desktop application window.

### 2. Browser Dashboard
Access the dashboard in your web browser:
```
http://127.0.0.1:8085
```
* **Default Admin Password**: `admin` *(Update immediately in the Settings tab upon first login)*.

### 3. Pairing Your WhatsApp Account
1. On the Dashboard tab, choose **QR Code Scan** or **8-Digit Phone Pairing Code**.
2. In WhatsApp on your mobile phone, navigate to **Settings > Linked Devices > Link a Device**.
3. Scan the QR code or enter the pairing code. Connection establishes within 2-4 seconds.

---

## 📚 Documentation & Guides
* **[COMPLIANCE.md](COMPLIANCE.md)**: WhatsApp & Meta anti-spam policies, opt-in rules, and anti-ban warmup schedule.
* **[SETUP-GUIDE.md](SETUP-GUIDE.md)**: Pairing instructions, 24/7 always-on deployment (preventing Windows sleep, NSSM service), and multi-instance hosting.
* **[BUILD.md](BUILD.md)**: Instructions for compiling with `-trimpath -ldflags="-s -w"`, running tests, and binary hardening.
* **[DEPENDENCIES.md](DEPENDENCIES.md)**: Third-party library inventory, security review, and vulnerability update plan.
* **[LICENSE](LICENSE)**: MIT License.

---

## 🔒 Security Architecture
1. **SSRF Mitigation**: Outbound webhook URLs are validated against private, loopback (RFC 1918/4193), and cloud metadata (`169.254.169.254`) ranges.
2. **CSRF Mitigation**: State-changing API endpoints require valid `X-CSRF-Token` headers matched against unguessable session tokens.
3. **Audit Logging**: All administrative operations (logins, broadcasts, exports, rule changes) are recorded in an in-memory audit ring buffer.
4. **Input Sanitization**: All incoming chat messages and contact names are HTML-escaped to prevent Stored XSS attacks.

---

## ⚖️ Legal Disclaimer
Pulse WhatsApp Automation Platform is an independent automation software solution. It is not affiliated, associated, authorized, endorsed by, or in any way officially connected with WhatsApp LLC, Meta Platforms, Inc., or any of their subsidiaries or affiliates.
