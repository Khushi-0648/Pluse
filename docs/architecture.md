# System Architecture & Technical Design

## 1. Executive Summary

The Pulse WhatsApp Automation Platform is an autonomous, single-binary automation solution engineered in Go. It connects as a linked companion device using the WhatsApp Web Multi-Device protocol (powered by the production-tested `go.mau.fi/whatsmeow` library) and provides interactive conversational workflows, CRM contact management, scheduled dispatches, broadcast campaigns, and Google Gemini AI smart replies.

```
+-----------------------------------------------------------------------------------+
|                        Pulse WhatsApp Automation Platform                         |
|                               (whatsapp-auto.exe)                                 |
+-----------------------------------------------------------------------------------+
|  [Glassmorphic Web UI & Dashboard]        [Desktop Window Launcher (Edge/Chrome)] |
|  - HTML5 / CSS3 / Vanilla JS (Zero Node)  - Native App-Mode Window                |
+-----------------------------------------+-----------------------------------------+
                                          |
                                    HTTP / JSON
                                          v
+-----------------------------------------------------------------------------------+
|                            Go Application Core Engine                             |
+-----------------------------------------------------------------------------------+
|  • Security & Auth (bcrypt, CSRF tokens, Host/Origin checks, rate limiting)       |
|  • Automation Engine (Keyword matching, regex, cooldowns, business hours)         |
|  • Inbound & Outbound Webhooks (SSRF protection, HMAC-SHA256 signing, retries)     |
|  • AI Reply Engine (Google Gemini 3.8 Flash SDK)                                  |
|  • Campaign Dispatcher (Anti-ban jitter pacing, opt-out compliance)               |
+-----------------------------------------+-----------------------------------------+
                                          |
                        +-----------------+-----------------+
                        |                                   |
                        v                                   v
+-----------------------------------+   +-------------------------------------------+
|    Local Data & Persistence       |   |      WhatsApp Network Integration         |
+-----------------------------------+   +-------------------------------------------+
| • SQLite DB: Session keys & crypt |   | • whatsmeow Multi-Device WebSocket Client |
| • JSON Stores: Contacts & History |   | • End-to-End Encrypted Signal Protocol    |
| • Audit Log: Immutable ring buffer|   | • QR Code & 8-Digit Phone Pairing         |
+-----------------------------------+   +-------------------------------------------+
```

---

## 2. Key Architecture Pillars

### 2.1 Single-Binary Go Engine
- Compiled with symbol and path stripping (`go build -trimpath -ldflags="-s -w"`).
- Zero external Node.js, Python, or runtime dependencies.
- Runs self-contained on Windows as a portable desktop app or background service.

### 2.2 WhatsApp Web Multi-Device Protocol
- Connects directly to WhatsApp's noise-protocol WebSocket servers using `whatsmeow`.
- Functions as an authorized secondary linked device under WhatsApp's multi-device framework.
- Independent operation: Does not require WhatsApp Web or desktop clients to remain open.
- Supports both QR code scanning and 8-digit phone pairing codes.

### 2.3 Security by Design
- **Localhost Isolation**: Listens strictly on `127.0.0.1` by default.
- **DNS Rebinding Guard**: Rejects requests where the `Host` header does not match localhost.
- **CSRF & Origin Verification**: Validates anti-CSRF tokens and rejects foreign `Origin` / `Referer` headers.
- **Brute-Force Protection**: 5-attempt login lockout rate limiter based on actual socket IP (ignores spoofed proxy headers).
- **SSRF Defense**: Outbound webhooks strictly block loopback, link-local, private RFC 1918 IPs, and cloud metadata endpoints.
- **Immutable Audit Trail**: Administrative actions and security events are logged with timestamps, IPs, and user context.

### 2.4 Anti-Ban & Policy Compliance
- **Opt-Out Enforcement**: Automatic keyword listeners for `STOP`, `UNSUBSCRIBE`, `CANCEL`, `QUIT` immediately halt automated dispatches.
- **Resubscribe Handling**: `START` keyword specifically handles resubscription without colliding with generic welcome menus.
- **Human Handover**: When a customer requests a live agent, auto-replies are paused for 24 hours.
- **Jitter Delays**: Broadcast campaigns feature randomized 4–10s human-like pacing with automatic circuit-breakers on consecutive errors.
