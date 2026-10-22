# Pulse architecture

Pulse is one Go program (`cmd/whatsapp-auto`) with an embedded web dashboard. Since 2.0 it connects only through Meta's WhatsApp Business Platform (Cloud API).

```
cmd/whatsapp-auto      start-up: config, data folder, migration, wiring, shutdown, CLI commands
pkg/cloudapi           WhatsApp Cloud API: Graph API calls (text, replies, media, templates,
                       read receipts, typing), connection checks, webhook parsing and dedupe
pkg/bot                incoming-message pipeline, rule matching, opening hours
pkg/msgtemplate        {{variable}} rendering and validation shared by every sender
pkg/campaign           campaign plans, paced sending, reports, official templates (24-hour window)
pkg/scheduler          scheduled messages
pkg/contacts           contacts, consent, opt-out, CSV import/export
pkg/history            message history, delivery status, conversations, statistics, retention
pkg/templates          saved message templates (Pulse's own text snippets)
pkg/webhook            outbound events to the owner's other systems, with retries
pkg/notify             staff alert hook (alerts are shown in the dashboard only)
pkg/securestore        encrypted, atomic file storage (AES-GCM, key protected by DPAPI) and sealed settings secrets
pkg/security           sessions, rate limits, CSRF/Host/Origin checks, SSRF guard, attachments, audit log
pkg/config             config.yaml, presets, rule editing, setup checklist, Business API credentials
pkg/server             HTTP API, the Cloud API webhook receiver and the embedded dashboard (pkg/server/web)
cmd/uismoke            browser click-through test with a stand-in Graph API (build tag uismoke)
```

## Credentials

Each install (and each `-session` profile) stores its own phone number ID, WhatsApp Business account ID, access token, app secret, verify token, Graph API version and public address in the `cloud_api` section of its `config.yaml`. `config.SetCloudCredentials` is the single place they are checked, sealed (`securestore.SealString`, DPAPI) and saved; the Settings form calls it, and a future sign-up flow can call it too. `config.ClearCloudCredentials` removes them (Settings > WhatsApp connection > Disconnect, `whatsapp-auto disconnect`). `cloudapi.Client` reads the settings on every call, so a change applies without a restart. A new `-session` profile starts as a copy of the main settings with the `cloud_api` section cleared.

## Incoming message

1. Meta posts to `https://<public address>/cloud/webhook` through the owner's tunnel. `server.handleCloudWebhook` checks the `X-Hub-Signature-256` HMAC with the app secret, then hands the raw body to `cloudapi.Client.HandleWebhook`. Parsing is separate from the signature check, so another authenticated source (for example a relay) can feed the same parser.
2. `HandleWebhook` ignores deliveries for another phone number ID, drops items it has already handled (Meta repeats deliveries), turns messages into `cloudapi.Message` (buttons and list replies become their text; photos, documents, locations become labels) and statuses into `cloudapi.Receipt`.
3. `main` passes each message to `bot.Engine.Receive`, which queues it per customer, so messages from one customer are handled in order while different customers are handled in parallel. The webhook answers Meta straight away.
4. The pipeline: store the message (before any reply) -> webhook `message.received` -> automation switch -> opt-out/opt-in -> do-not-message, blocked, test-mode allow-list, paused -> hand-over request -> cooldown, per-customer cap, loop detection, daily limit -> opening hours (away reply once per visit) -> rules (numbers only inside an open menu; replies with empty variables are skipped) -> fallback reply (once per 30 minutes) -> reply quoting the customer's message, or record as dry run.
5. Receipts update `history` and the campaign report; a "failed" status records Meta's reason in plain words.

## Outgoing messages

Staff sends, scheduled messages, campaigns, the inbound integration endpoint and the `send` command all render text through `msgtemplate` and refuse to send when a variable is unknown or empty. They check opt-out, blocked numbers, test mode, consent rules, the daily limit and practice mode, and record the message in `history`.

WhatsApp's 24-hour customer service window: free text only goes to customers whose last message is less than 24 hours old (`campaign.NeedsTemplate`). Outside it, campaigns and scheduled messages send the approved template the owner entered (`campaign.Template`), and other senders refuse with a plain message before calling Meta. Meta's own error 131047 is mapped to the same explanation as a safety net.

`cloudapi` retries a request only when Meta refused it (HTTP 429 or 5xx) or the connection could not be opened at all; a timeout after the request was sent is never repeated, so a customer never gets a message twice.

## Storage

Customer data lives in the data folder (`%APPDATA%\Pulse` by default), restricted to the Windows user. JSON files are encrypted with AES-256-GCM; the key is wrapped with Windows DPAPI. Writes are atomic with a `.bak` copy and are batched. Message history is capped (default 50,000) and optionally deleted after a retention period. There is no WhatsApp session database any more; the files left by 1.x (`whatsapp.db`, `whatsapp.db.sealed`, `-wal`, `-shm`) are listed in Settings > Privacy and data and deleted only when the owner asks.

`whatsapp-auto stop` (and `scripts\Stop-Background.ps1`) closes the app normally through a loopback-only endpoint protected by a random key in `control.token`.

Automatic backups: once a day the backup bundle is written encrypted to `backups\pulse-backup-YYYY-MM-DD.json` (last 7 kept), optionally also to a folder the owner chooses.

## Dashboard

Plain HTML, CSS and JavaScript embedded in the binary, served under a Content Security Policy without `unsafe-inline`. All values are HTML-escaped by the `P.html` template helper. Every control uses `data-action`, `data-form` or `data-change` and is bound through one registry; a unit test checks that each one has a handler.
