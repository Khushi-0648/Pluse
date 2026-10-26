# Pulse - messaging automation for small businesses

Pulse is a single Windows program (also builds for macOS and Linux) that connects to **Meta's official WhatsApp Business Platform (Cloud API)** with your own Meta account and number, answers common questions automatically, hands chats to a person on request, and gives staff a shared inbox, contacts, templates, scheduled messages and opt-in campaigns.

Every install uses its owner's own Meta app, phone number ID and access token; nothing is routed through anyone else. Version 2.0 removed the old linked-device (WhatsApp Web style) connection: the official platform is the way WhatsApp's terms allow businesses to automate messages. See [COMPLIANCE.md](COMPLIANCE.md) for Meta's rules and how Pulse follows them.

## What it does

- **Inbox** with unread counts, previews, "needs a person" filter, search, drafts, attachments (PNG, JPEG, WebP, PDF) and quick templates.
- **Auto-replies** with keyword rules, a numbered menu that only reacts to numbers while the menu is open, priority order, an on/off switch per rule, a live tester and warnings for overlapping keywords.
- **Hand-over to a person**: customers type AGENT (or "talk to someone", "baat karni hai", ...). The bot pauses in that chat, the chat is flagged under *Needs attention*, and the dashboard plays an alert.
- **Opt-out and opt-in** in English, Hindi and Hinglish (STOP, "rok do", "band karo", START, "shuru karo").
- **Contacts** with consent records, tags, bulk actions and CSV import with column mapping.
- **Campaigns** that only go to opted-in contacts, send your Meta-approved template to customers outside the 24-hour window, explain who was left out and why, pace themselves, pause on failures or opt-out spikes, and report sent / delivered / read / replied.
- **Scheduled messages** with an optional approved template for customers who have not written in 24 hours.
- **Account health** panel with Meta's quality rating, and a daily sending limit with a warm-up mode.
- **Integrations**: signed outbound webhooks with retries and replay, an inbound endpoint for shops and CRMs, and an API key that can send messages and read data but cannot change settings or the WhatsApp connection.
- **Privacy**: data is stored encrypted in your user profile; the access token and app secret are sealed with your Windows account and never shown again; per-customer export and deletion; retention period.

## Quick start (Windows)

1. Double-click `whatsapp-auto.exe` (or `Run-App.bat`).
2. Sign in with the one-time password shown in the window (also saved in `FIRST-RUN-PASSWORD.txt` in the data folder) and choose your own password.
3. Follow [docs/meta-whatsapp-setup.md](docs/meta-whatsapp-setup.md) to create your Meta app, get the phone number ID and a permanent token, start an https tunnel (for example `cloudflared tunnel --url http://127.0.0.1:8085`), and enter everything in **Settings > WhatsApp connection**.
4. On **Home**, finish the **Get started** checklist and switch automatic replies on.

The dashboard is at `http://127.0.0.1:8085` and is only reachable from the same computer. Through the tunnel, only the webhook address `/cloud/webhook` answers.

**Incoming messages need the tunnel.** Meta delivers them to the public https address; while the tunnel (or the computer) is off, Meta retries for a while, and messages arrive when it is back.

## Where data is stored

| What | Where |
| --- | --- |
| Settings and rules | `config.yaml` next to the program (the access token and app secret are stored sealed, not readable) |
| Messages, contacts, templates, campaigns, schedule, activity log | `%APPDATA%\Pulse\` (encrypted with a key protected by your Windows account) |

When you upgrade from 1.x, everything is kept. The old WhatsApp session files are no longer used; Settings > Privacy and data offers to delete them.

## Commands

```
whatsapp-auto app        start and open the dashboard window (default)
whatsapp-auto serve      start in the background, no window
whatsapp-auto status     check the connection to the WhatsApp Business API
whatsapp-auto disconnect remove the access token and app secret from this computer
whatsapp-auto send -to +919876543210 -msg "Hello"   (customer must have written in the last 24 hours)
whatsapp-auto -session sales -port 8086   second number with its own settings and data
```

## Documentation

- [docs/meta-whatsapp-setup.md](docs/meta-whatsapp-setup.md) - connecting your Meta app and number.
- [SETUP-GUIDE.md](SETUP-GUIDE.md) - setup, keeping it running, upgrading.
- [COMPLIANCE.md](COMPLIANCE.md) - Meta's policies, opt-in, templates, quality.
- [BUILD.md](BUILD.md) - building, testing and packaging.
- [DEPENDENCIES.md](DEPENDENCIES.md) - libraries and the update routine.
- [docs/architecture.md](docs/architecture.md) and [docs/security.md](docs/security.md).

Pulse is independent software. It is not affiliated with, authorised, maintained, sponsored or endorsed by WhatsApp LLC or Meta Platforms, Inc.

## Code Signing

Free code signing is provided by [SignPath.io](https://signpath.io) certificate authority for the SignPath Foundation.