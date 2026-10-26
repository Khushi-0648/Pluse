# Security and privacy in Pulse

Pulse is a single program that runs on one Windows computer and connects to Meta's WhatsApp Business Platform (Cloud API). This page describes what the code actually does.

## Signing in

- **First start:** Pulse creates a random one-time password, prints it in its window and saves it in `FIRST-RUN-PASSWORD.txt` in the data folder. After the first sign-in the admin must choose their own password, and the file is deleted.
- **Passwords** are stored as bcrypt hashes in `config.yaml`, which lives in the data folder (`%APPDATA%\Pulse`), not next to the program. If the data folder holds data but no `config.yaml`, Pulse refuses to start instead of creating a new one-time password, so a fresh copy of the program cannot be used to get into existing data. They must be at least 8 characters, mix letters and numbers, and are refused when easy to guess. The admin and staff passwords must differ.
- **Forgotten password:** `Reset-Password.bat` (or `whatsapp-auto reset-password`) only works while Pulse is closed and must run with Windows administrator rights. This is a hurdle, not a hard barrier: whoever uses the Windows account can also edit `config.yaml`, so protect the Windows account itself. It refuses to run when elevation switched to another Windows account (no Pulse data there). (The 1.x check that sent a code to the linked phone's own chat is gone: a Cloud API number cannot message itself.) Every reset is written to the activity log, and the dashboard shows a notice for 7 days.
- **Sessions:** a random token in an HttpOnly, SameSite=Strict cookie, valid 7 days. Changing a password signs out the other sessions.
- **Lockout:** 5 wrong passwords from one address lock sign-in (and password-protected actions) for 5 minutes; IPv6 addresses count per /64 network, so changing addresses inside one network does not reset the count. On top of that, 20 wrong passwords from all callers together within 15 minutes pause every sign-in and password re-check for 15 minutes, doubling with each new pause within a day (up to 4 hours), so a caller with many addresses gets no more guesses. A sign-in made directly on the computer running Pulse (not through a proxy) is exempt from that pause, not from its own lockout, so the owner can always sign in there. Wrong passwords are written to the activity log at most once a minute, with a count of the ones left out. The API is limited to 300 requests a minute per address; a signed-in session and the API key each have their own limit, so a web page firing requests at 127.0.0.1 cannot lock the owner out. Behind a configured trusted proxy the caller's address is read from `X-Forwarded-For` only (right to left), never from `X-Real-IP` or `CF-Connecting-IP`, which a caller could set.
- **Roles:** the admin can do everything. Staff can use the inbox, contacts, templates and scheduled messages; they cannot see settings (including the WhatsApp connection), rules, campaigns, the activity log or privacy tools.

## The dashboard

- Listens on `127.0.0.1:8085` only. The `Host` header must be a local name (or one listed in `server.allowed_hosts`), which blocks DNS-rebinding attacks.
- Every change needs the session's CSRF token in `X-CSRF-Token`, and a cross-site `Origin` is refused.
- Strict Content Security Policy: no inline scripts or styles; all text is escaped before it is shown.
- Deleting several contacts at once, deleting all data or the imported chats, a customer's erasure request, restoring a backup, changing the API key, the integration security key or the event address, and changing or removing the WhatsApp connection need the admin password again. Deleting one contact (with Undo) or one chat needs the signed-in admin. The developer API key cannot delete anything. A customer's erasure request also removes them from the daily backups and masks their number in the activity log; deleting all data also deletes the backups and masks every customer number in the activity log. Disconnecting removes the token and app secret from `config.yaml` and from its backup copy `config.yaml.bak`.
- **Sign out** only ends the session. It never deletes data.
- Making Pulse keep messages for a shorter time, or fewer of them, deletes the older ones, so it needs the admin password again too.

## Using Pulse from a phone (remote access)

Off by default. When the admin switches it on (Settings > Account > Use Pulse on your phone; admin password required, and a fixed `https://` public address must be saved), the dashboard is also served under the public address, so an Android phone can open it and install it as an app. Then:

- It is served there only when the saved public address is `https`, the request is addressed to that host name (`Host`, or the last `X-Forwarded-Host` entry when the tunnel rewrites `Host`), it comes through a trusted proxy (the tunnel program on this computer, cloudflared or ngrok, which connects from 127.0.0.1; or a CIDR in `server.trusted_proxies`) **and** the browser used `https` (`X-Forwarded-Proto: https`). Plain http, a public address that is empty or not https, and forwarding headers from any other address still get 404.
- A request from this computer counts as coming through the tunnel only when it is addressed to the public host name. Any other local request with forwarding headers (another program, or a web page using DNS rebinding) gets 404, and its `X-Forwarded-For` is never believed.
- Sign-in, roles, CSRF/Origin checks and the admin-password re-checks apply as on the computer; through the tunnel an `Origin` must also be `https`. The session cookie is `Secure`, responses carry `Strict-Transport-Security: max-age=31536000`, and a session signed in through the tunnel also ends after 12 hours without use. Wrong passwords lock out the caller's own address (read from `X-Forwarded-For` as added by the tunnel), so one phone cannot lock out the others; the limit across all callers (see Lockout) still applies.
- The developer API key (`X-API-Key` or `Authorization`) and the integration endpoint `POST /api/v1/webhook/send` are refused (404) through the tunnel; there is no setting to allow them. Use them from this computer or the local network.
- **The tunnel must be https-only and must overwrite `X-Forwarded-Proto`** with the scheme it saw, never pass on a caller's value, and must add the caller's address as the last `X-Forwarded-For` entry. Cloudflare Tunnel and ngrok do this; do not expose a plain-http address for the same tunnel.
- Actions for the computer itself (closing Pulse, start with Windows, desktop shortcuts, opening the app window) refuse any request that came through a proxy, so they never work from a phone.
- Anyone who knows the address sees the sign-in page: keep strong admin and staff passwords. The installed app caches nothing: no message or contact is stored on the phone.
- Switching it on or off is written to the activity log.

## WhatsApp Business API credentials

- Each install uses the owner's own Meta app, phone number ID and access token.
- The **access token** and **app secret** are sealed with Windows DPAPI (user scope, separate entropy) before they are written to `config.yaml`; a value typed into the file by hand is sealed at the next start, and the backup copy `config.yaml.bak` is rewritten too. They are never sent to the browser, not even masked: the settings page only learns whether they are saved. They are never written to the console or logs.
- Saving the connection needs the admin password (signed in to the dashboard). **Test connection** checks the form's values with Meta without saving them.
- The access token is only ever sent to `graph.facebook.com` over https. (For automated tests, `PULSE_GRAPH_BASE_URL` can point the program at a stand-in on a loopback address; any other address is ignored.)

## The webhook (incoming messages)

Meta delivers incoming messages and delivery updates to `https://<public address>/cloud/webhook`, through a tunnel or reverse proxy the owner runs (for example Cloudflare Tunnel). This is the only address that answers without signing in:

- It exists only once the phone number ID is saved; before that it answers 404.
- `GET` (Meta's subscription check) answers the challenge only when `hub.verify_token` matches the random verify token (constant-time comparison).
- `POST` bodies are limited to 1 MB and must carry `X-Hub-Signature-256: sha256=<HMAC-SHA256 of the raw body, keyed with the app secret>`, checked in constant time. Without a saved app secret, or with a wrong signature, the request is refused (401) and written to the activity log.
- Deliveries for another phone number ID are ignored; repeated deliveries are recognised by their message ID and handled once.
- The path is exempt only from the Host-header, CSRF/Origin and session checks, which a server-to-server call cannot pass.
- Under the tunnel's host name (the configured public address, in `Host` or `X-Forwarded-Host`) every other path answers 404, even the sign-in page and even if that name was added to `server.allowed_hosts` — unless the admin switched on remote access (see above), and then only over https through a trusted proxy. Requests with forwarding headers (`Cf-Ray`, `Cf-Connecting-IP`, `X-Forwarded-*`, `Forwarded`, `X-Real-IP`, `X-Original-Host`, `True-Client-IP`, `Via` and similar) also get 404 unless they come from `server.trusted_proxies`, so a tunnel that rewrites the Host header to 127.0.0.1 cannot expose the dashboard either. A proxy that adds none of these headers (for example Nginx `proxy_pass` with its defaults) cannot be told apart from a local browser: configure such a proxy to forward only `/cloud/webhook`, or to set `X-Forwarded-For`.
- Only rejected webhook calls are rate limited. Behind a tunnel every caller shares the tunnel's address, so a limit on all calls would let forged calls lock out Meta.

### Tunnel Privacy and TLS Termination Disclosure

- **In-Transit Decryption at Tunnel Providers:** When using third-party reverse-proxy tunnels (such as Cloudflare Tunnel or ngrok), the HTTPS connection from Meta terminates at the tunnel provider's edge servers. The tunnel provider decrypts the incoming HTTP webhook payload before transmitting it through the local tunnel daemon to Pulse. The payload contains customer phone numbers, sender names, and message text.
- **True End-to-End Privacy:** If your organization requires zero third-party visibility (e.g. healthcare, legal, or financial compliance), do not use managed reverse-proxy tunnels. Instead, terminate TLS directly on your own infrastructure using a local reverse proxy (such as Caddy or Nginx with Let's Encrypt certificates), or use a private TCP pass-through stream proxy (e.g. WireGuard or HAProxy SNI pass-through) where TLS certificates reside exclusively on your machine.
- **Quick Tunnel Instability:** Ephemeral quick tunnels (such as `cloudflared tunnel --url ...`) generate temporary URLs that change on every process restart or reboot. When the URL changes, Meta cannot deliver incoming messages until the Callback URL is manually updated in Meta's developer console. For reliable production use, configure a **Named Cloudflare Tunnel** with a fixed domain, a static ngrok domain, or Dynamic DNS.

## Integrations

- **Inbound** (`POST /api/v1/webhook/send`): needs the sending key (`webhook.inbound_secret`) in `X-Webhook-Secret`, or an HMAC-SHA256 signature with it, over `timestamp + "." + body` with a timestamp no older than 5 minutes. The same safety rules apply as for staff, including the 24-hour window: outside it only a Meta-approved template is sent (`"whatsapp_template": {"name", "language", "params", "header_params", "header_media", "button_params"}`), and only to a customer whose opt-in is recorded in Pulse. A new number is added to the contacts only once a message to it was sent; a refused request adds nothing.
- The sending key is separate from the key that signs outgoing events, so a system that only receives events cannot send messages. Settings saved before 2.1 have no sending key: then the signing key is still accepted for sending, Pulse prints a warning at start and the Integrations page shows one until a sending key is set.
- Through the public tunnel, a caller that recently gave a correct password keeps its own lockout but is not paused when guesses from other addresses pause sign-in for everyone else; sign-in at the computer itself is never paused. A tunnel request without a usable `X-Forwarded-For` is counted as one shared "unknown tunnel caller", never as this computer.
- **Outbound** events are signed with `X-Pulse-Signature: t=<timestamp>,v1=<HMAC>`. Addresses on the local network, loopback, link-local and cloud metadata ranges are refused.
- **API key:** random, stored as a hash, compared in constant time. It can only send messages and read messages, conversations, contacts and templates. Everything else (settings, rules, templates, campaigns, scheduled messages, consent and do-not-message changes, pausing the bot, editing or deleting contacts, reports, the activity log, backups, exports, the connection and passwords) needs someone signed in to the dashboard.

## Data on disk

- Everything lives in `%APPDATA%\Pulse`, restricted to the Windows user.
- Contacts, messages, templates, campaigns, schedules, the activity log and backups are encrypted with AES-256-GCM. The key is protected with Windows DPAPI.
- Pulse 2.0 keeps no WhatsApp session database. Session files left by 1.x are not opened; Settings > Privacy and data offers to delete them (admin password required).
- Retention: messages older than the chosen number of days are deleted once a day; the message count is capped.
- Per customer: download their data or delete everything about them. An erasure also masks their number (last 4 digits kept) and removes their name in the activity log, and a later "Restore backup" does not bring them back.
- Opt-outs survive deletion: a contact deleted after opting out (or removed by "delete all") is kept as a salted hash in `contacts-suppressed.json`, so restoring an older backup brings them back only as do-not-message, never with consent.
- "Delete all data" also deletes the daily backups (`backups\pulse-backup-*.json`) and the copies in the extra backup folder.
- Refused webhook calls are written to the activity log at most once a minute with a count, so a flood through the public tunnel cannot push older entries out of the log.

## What Pulse cannot protect against

- **Tunnel Provider In-Transit Visibility:** When using third-party managed tunnels (Cloudflare, ngrok), the tunnel provider terminates TLS at its edge and can inspect webhook payloads. Terminate TLS directly on your own infrastructure if third-party decryption is not permitted.
- Meta's own rules: a number with many blocks or reports gets lower limits or is restricted.
- Someone who is signed in to the same Windows account while Pulse is running can read what Pulse can read, including the unsealed token in memory. Use a Windows password and lock the computer.
- Anyone who obtains the access token can send messages as the business: revoke it in Meta Business settings if a computer is lost.
- The program is not code-signed unless you sign it with a trusted certificate (BUILD.md).
