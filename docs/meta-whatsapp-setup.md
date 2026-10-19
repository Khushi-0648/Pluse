# WhatsApp Connection & Protocol Guide

This guide details how Pulse connects to WhatsApp using the Multi-Device companion protocol.

---

## 1. Connection Architecture

Pulse utilizes the **WhatsApp Web Multi-Device Protocol** (implemented in Go via `go.mau.fi/whatsmeow`).

### How It Works:
1. Pulse registers as a companion device (secondary client) associated with your primary WhatsApp phone.
2. The initial pairing handshake is authenticated either via:
   - **QR Code Scan**: Scanned directly using WhatsApp on your primary phone.
   - **8-Digit Pairing Code**: Generated on your PC and entered in WhatsApp on your phone.
3. Once paired, cryptographic keys are persisted in local SQLite storage (`whatsapp.db`).
4. Pulse establishes an independent, end-to-end encrypted noise connection to WhatsApp servers without needing WhatsApp Web to stay open in a browser.

---

## 2. Comparison: Multi-Device Protocol vs. Meta Cloud API

| Aspect | Pulse Multi-Device Protocol | Meta Cloud API |
| :--- | :--- | :--- |
| **Setup Complexity** | Zero setup; scan QR or enter code | Requires Meta Business verification & developer app |
| **Per-Message Costs** | Free (uses your existing SIM/number) | Paid per conversation tier to Meta |
| **Message History** | Syncs with your mobile WhatsApp app | Standalone Cloud inbox only |
| **Number Mobility** | Uses your existing personal or business number | Requires dedicated phone number not on normal app |
| **Anti-Ban Requirements** | Requires warming up & pacing limits | Subject to Meta template approval & quality rating |

---

## 3. Best Practices & Policy Compliance

To ensure reliable, uninterrupted operation:
1. **Explicit Consent**: Only message contacts who have opted in.
2. **Respect STOP / Unsubscribe**: The system automatically honors `STOP` by setting Do-Not-Disturb on contacts.
3. **Warm-Up New Numbers**: For newly registered numbers, start with 20–50 messages daily before increasing broadcast volume.
4. **Natural Jitter**: Maintain default delay settings (4–10 seconds between outgoing broadcast dispatches).
