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

## Phone numbers

Contacts are keyed by the full international number, digits only (`contacts.NormalizePhone`). Numbers from Meta (`from`, `wa_id`) are taken as they are (`contacts.CleanWAID`); only a number a person typed goes through `contacts.CleanPhone`, which adds India's 91 to a bare 10-digit mobile starting 6-9 (a number typed with `+` or `00` keeps its country).

- `Store.GetExact` matches a stored number exactly. Everything that decides whether or where a message goes (campaigns, scheduled messages, the inbox, test messages, integrations, the bot), backup restore and every update (`SetDND`, `SetConsent`, `RecordInbound`, delete, erase) uses it; a number that is not a contact is checked with `WasOptedOut`.
- `Store.Get` is a lookup aid for typed input only (forms, search, "send to" fields): an exact match first, then the 91 reading and a unique last-10-digit match for a bare number.
- `Store.Save` stores the number as typed (an existing contact keeps its stored number); `Store.SaveExact` stores digits that are already international, such as the number an integration just sent to. Neither ever replaces another number's contact.
- Pulse 2.0 stored foreign 10-digit numbers with 91 in front (`contacts.LegacyWAID`). The opt-out list file (`contacts-suppressed.json`) records `upgraded_at`, the first time 2.1 opened the list; only contacts created before it can be moved to the real number (`Store.Upsert`, which calls the `OnRenamed` hooks so history, campaigns and scheduled messages follow) or have their old-form opt-out applied once (`legacy_done`).

## Incoming message

1. Meta posts to `https://<public address>/cloud/webhook` through the owner's tunnel. `server.handleCloudWebhook` checks the `X-Hub-Signature-256` HMAC with the app secret, then hands the raw body to `cloudapi.Client.HandleWebhook`. Parsing is separate from the signature check, so another authenticated source (for example a relay) can feed the same parser.
2. `HandleWebhook` ignores deliveries for another phone number ID, drops items it has already handled (Meta repeats deliveries), turns messages into `cloudapi.Message` (buttons and list replies become their text; photos, documents, locations become labels) and statuses into `cloudapi.Receipt`.
3. `main` passes each message to `bot.Engine.Receive`, which queues it per customer, so messages from one customer are handled in order while different customers are handled in parallel. The webhook answers Meta straight away.
4. The pipeline: store the message (before any reply) -> webhook `message.received` -> automation switch -> opt-out/opt-in -> do-not-message, blocked, test-mode allow-list, paused -> hand-over request -> cooldown, per-customer cap, loop detection, daily limit -> opening hours (away reply once per visit) -> rules (numbers only inside an open menu; replies with empty variables are skipped) -> fallback reply (once per 30 minutes) -> reply quoting the customer's message, or record as dry run.
5. Receipts update `history` and the campaign report; a "failed" status records Meta's reason in plain words.

## Outgoing messages

Staff sends, scheduled messages, campaigns, the inbound integration endpoint and the `send` command all render text through `msgtemplate` and refuse to send when a variable is unknown or empty. They check opt-out, blocked numbers, test mode, consent rules, the daily limit and practice mode, and record the message in `history`.

Integration secrets: outbound events (`pkg/webhook`) are signed with `webhook.secret`; the inbound integration endpoint should accept `webhook.inbound_secret` (`config.Config.InboundSecret()`), which falls back to `webhook.secret` only when it is empty, for settings from 2.1.0 and older; Pulse then prints a warning at start recommending a separate inbound secret. When the outbound queue is full (1,000 events), the oldest event is dropped and logged in the activity log (`WEBHOOK_EVENT_DROPPED`, at most once a minute with a count). An erasure request removes queued and failed events whose `data.phone` is exactly that customer's number.

WhatsApp's 24-hour customer service window: free text only goes to customers whose last message is less than 23 hours 30 minutes old (`campaign.NeedsTemplate`, `contacts.FreeTextWindow`), leaving a margin for pacing and delays before Meta's 24 hours run out. The daily sending limit counts the last 24 hours (rolling), and campaign and scheduled sends write a labelled "sending" record before calling Meta, so a send cut off by a crash still counts and is corrected when Meta reports it. Outside it, campaigns and scheduled messages send the approved template the owner entered (`campaign.Template`), and other senders refuse with a plain message before calling Meta. Meta's own error 131047 is mapped to the same explanation as a safety net.

`cloudapi` retries a request only when Meta refused it with "too many requests" (HTTP 429), or the connection could not be opened at all. A server error (5xx) is retried only for reads and media uploads: Meta may have queued a message before failing, so a message send answered with 5xx is reported as "may or may not have been sent" instead. A timeout after the request was sent is never repeated, so a customer never gets a message twice.

Campaigns write each recipient's "sending" mark to disk before the message goes out; after a crash that recipient is marked interrupted (check before sending again) rather than sent a second time on Resume.

## Storage

Settings (`config.yaml`, one `config-<session>.yaml` per extra profile) live in the data folder unless `-config` is given. A `config.yaml` that an older version left in the program folder is moved there on the first start, unless that version is still running. `whatsapp-auto serve -keep-running` (used by the background task and "start with Windows") runs the server as a child process and starts it again after a crash; a normal stop ends both.

Customer data lives in the data folder (`%APPDATA%\Pulse` by default), restricted to the Windows user. JSON files are encrypted with AES-256-GCM; the key (`store.key`, created once with O_EXCL so two copies starting together share one key) is wrapped with Windows DPAPI. Each file's name is part of the GCM additional data, so one data file cannot be swapped for another; files written by 2.1.0 and older (header only) still open and are rewritten in the new form on the next save. On macOS and Linux there is no DPAPI: the key and the sealed settings secrets rely on file permissions only, and Pulse warns about it at start. Only one copy may use a data folder: `run` takes an exclusive OS lock on `pulse.lock` (LockFileEx on Windows, flock elsewhere) before any data file is read, moved or replayed; a second `app` opens the running copy's window and exits, and `stop`, `disconnect`, `reset-password` and `send` treat a held lock as "running". Writes are atomic with a `.bak` copy and are batched. Message history is capped (default 50,000) and optionally deleted after a retention period. There is no WhatsApp session database any more; the files left by 1.x (`whatsapp.db`, `whatsapp.db.sealed`, `-wal`, `-shm`) are listed in Settings > Privacy and data and deleted only when the owner asks.

`whatsapp-auto stop` (and `scripts\Stop-Background.ps1`) closes the app normally through a loopback-only endpoint protected by a random key in `control.token`.

Automatic backups: once a day the backup bundle is written encrypted to `backups\pulse-backup-YYYY-MM-DD.json` (last 7 kept), optionally also to a folder the owner chooses.

## Dashboard

Plain HTML, CSS and JavaScript embedded in the binary, served under a Content Security Policy without `unsafe-inline`. All values are HTML-escaped by the `P.html` template helper. Every control uses `data-action`, `data-form` or `data-change` and is bound through one registry; a unit test checks that each one has a handler.
