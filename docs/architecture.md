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

Contacts are keyed by the full international number, digits only (`contacts.NormalizePhone`). Numbers from Meta (`from`, `wa_id`) are taken as they are (`contacts.CleanWAID`).

**The rule: a stored number is an exact identity.** A number Pulse stored (a contact, a message in the history, a scheduled message, a campaign recipient, a webhook event, a backup) is compared digit for digit everywhere: contacts, history, scheduler, campaigns, erasure, exports and display. Nothing that decides who is messaged, whose data is shown or exported, or who is erased ever adds a country code to a stored number or matches one by its last digits.

India's 91 is guessed in one place only, `contacts.ResolveTyped`, which turns something a person typed (a form, the "send to" field, an erasure or export request, a campaign's number list, an integration's `to`, the `send` command) into the stored number it means, and only when that is unambiguous:

1. typed with `+` or `00`: the number has its country code, nothing is guessed;
2. bare digits that are a stored number (a contact, or known to the history or scheduler): exactly that number, so Singapore's 6591234567 stays Singapore even when India's 916591234567 is also a customer;
3. a bare 10-digit mobile starting 6-9 that is not stored: India (`contacts.CleanPhone`); a longer number: taken as international;
4. anything else of 10 digits or fewer is refused (`ErrNeedsCountryCode`), and formatted digits without `+` that are stored both as typed and with 91 in front are refused (`ErrAmbiguousNumber`).

No path matches a number by its last 10 digits; the contact search box matches parts of numbers but never decides anything.

- `server.resolvePhone` (contacts, history and scheduled messages as "known") resolves a request's number once; the handler then uses the result with exact lookups only. `contacts.Store.Resolve` and `Store.Get` (typed lookup, contacts only) use the same function, as do `scheduler.Validate` and `campaign.BuildPlan`.
- `Store.GetExact` matches a stored number exactly. Everything that decides whether or where a message goes (campaigns, scheduled messages, the inbox, test messages, integrations, the bot), the inbox and overview display, backup restore and every update (`SetDND`, `SetConsent`, `RecordInbound`, delete, erase) uses it; a number that is not a contact is checked with `WasOptedOut`.
- `history.Query`'s phone filter, `ClearPhone`, `Scheduler.ForgetPhone`, `Runner.ForgetPhone` and `Dispatcher.Forget` match stored numbers exactly and never fall back to a typed reading.
- `Store.Save` stores a typed number (through `ResolveTyped`; an existing contact keeps its stored number); `Store.SaveExact` stores digits that are already international, such as the number an integration just sent to or a number the server resolved. Neither ever replaces another number's contact.
- Blocked and test-number lists are typed lists matched with `MatchesTyped` / `AllowListed`, which only ever widen the block (the safe side).

### Numbers saved by Pulse 2.0

Pulse 2.0 stored foreign 10-digit numbers with 91 in front (`contacts.LegacyWAID`: 6591234567 became 916591234567). The opt-out list file (`contacts-suppressed.json`) records when 2.1 first ran on the list (`upgraded_at`). 2.1.0 recorded nothing and 2.1.1 recorded its own first start, so on lists 2.1.0 had used, customers added under 2.1.0 looked like 2.0 contacts. Since then the time is worked out once (`upgrade_checked`, `contacts.settleUpgradeLocked`) from evidence on the PC: the activity log's `APP_STARTED` entries name each version (the first 2.1 start is exact when a 2.0 start is still logged before it; the log keeps the newest 2,000 entries); a contact under a bare 10-digit number starting 6-9 can only have been written by 2.1 (2.0 ran every number through `CleanPhone`); 2.1.1's `upgraded_at`. When the first 2.1 start is not known exactly, `legacy_before` marks the contacts that surely predate it (before a logged 2.0 start, else none).

- A contact under the 91 form that surely comes from 2.0 and came from WhatsApp, wrote or opted out is moved to the real number when that number writes (`Store.Upsert`, which calls the `OnRenamed` hooks so history, campaigns and scheduled messages follow); its consent never moves.
- One that only may come from 2.0 is never moved; its opt-out still applies to the real number (the safe side). An opt-out under the old form is also applied once to a contact 2.1.0 created under the real number before Pulse looked for the old form (`legacy_aware_at`, `legacy_done`).
- Erasing the real number also erases the 91-form contact and its messages, scheduled and campaign entries, webhook events and backup entries when it surely is 2.0's record of that customer (`Store.ForgetLegacy`); otherwise it is left alone (it may be an Indian customer) and the response tells the admin (`not_erased`). The 91 form is masked in the activity log and blocked from restores either way, and stays opted out if it had opted out.
- Erasure deletes the `.bak` copies of every file it changes (history, contacts, scheduled messages, campaigns, webhook queue, activity log), since a `.bak` holds the previous version.

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
