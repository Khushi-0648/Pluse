# Security and privacy in Pulse

Pulse is a single program that runs on one Windows computer and connects to Meta's WhatsApp Business Platform (Cloud API). This page describes what the code actually does.

## Signing in

- **First start:** Pulse creates a random one-time password, prints it in its window and saves it in `FIRST-RUN-PASSWORD.txt` in the data folder. After the first sign-in the admin must choose their own password, and the file is deleted.
- **Passwords** are stored as bcrypt hashes in `config.yaml`. They must be at least 8 characters, mix letters and numbers, and are refused when easy to guess. The admin and staff passwords must differ.
- **Forgotten password:** `Reset-Password.bat` (or `whatsapp-auto reset-password`) only works while Pulse is closed and must run with Windows administrator rights, so someone who only has the Windows account cannot take over. (The 1.x check that sent a code to the linked phone's own chat is gone: a Cloud API number cannot message itself.) Every reset is written to the activity log, and the dashboard shows a notice for 7 days.
- **Sessions:** a random token in an HttpOnly, SameSite=Strict cookie, valid 7 days. Changing a password signs out the other sessions.
- **Lockout:** 5 wrong passwords from one address lock sign-in (and password-protected actions) for 5 minutes. The API is limited to 300 requests a minute per address.
- **Roles:** the admin can do everything. Staff can use the inbox, contacts, templates and scheduled messages; they cannot see settings (including the WhatsApp connection), rules, campaigns, the activity log or privacy tools.

## The dashboard

- Listens on `127.0.0.1:8085` only. The `Host` header must be a local name (or one listed in `server.allowed_hosts`), which blocks DNS-rebinding attacks.
- Every change needs the session's CSRF token in `X-CSRF-Token`, and a cross-site `Origin` is refused.
- Strict Content Security Policy: no inline scripts or styles; all text is escaped before it is shown.
- Deleting data, restoring a backup, changing keys and changing or removing the WhatsApp connection need the admin password again. The developer API key can do none of these.
- **Sign out** only ends the session. It never deletes data.

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
- The path is exempt only from the Host-header, CSRF/Origin and session checks, which a server-to-server call cannot pass. It keeps its own rate limit.
- Under the tunnel's host name (the configured public address, in `Host` or `X-Forwarded-Host`) every other path answers 404, even the sign-in page and even if that name was added to `server.allowed_hosts`: the dashboard is never exposed through the tunnel.

### Tunnel Privacy and TLS Termination Disclosure

- **In-Transit Decryption at Tunnel Providers:** When using third-party reverse-proxy tunnels (such as Cloudflare Tunnel or ngrok), the HTTPS connection from Meta terminates at the tunnel provider's edge servers. The tunnel provider decrypts the incoming HTTP webhook payload before transmitting it through the local tunnel daemon to Pulse. The payload contains customer phone numbers, sender names, and message text.
- **True End-to-End Privacy:** If your organization requires zero third-party visibility (e.g. healthcare, legal, or financial compliance), do not use managed reverse-proxy tunnels. Instead, terminate TLS directly on your own infrastructure using a local reverse proxy (such as Caddy or Nginx with Let's Encrypt certificates), or use a private TCP pass-through stream proxy (e.g. WireGuard or HAProxy SNI pass-through) where TLS certificates reside exclusively on your machine.
- **Quick Tunnel Instability:** Ephemeral quick tunnels (such as `cloudflared tunnel --url ...`) generate temporary URLs that change on every process restart or reboot. When the URL changes, Meta cannot deliver incoming messages until the Callback URL is manually updated in Meta's developer console. For reliable production use, configure a **Named Cloudflare Tunnel** with a fixed domain, a static ngrok domain, or Dynamic DNS.

## Integrations

- **Inbound** (`POST /api/v1/webhook/send`): needs the shared security key in `X-Webhook-Secret`, or an HMAC-SHA256 signature over `timestamp + "." + body` with a timestamp no older than 5 minutes. The same safety rules apply as for staff, including the 24-hour window.
- **Outbound** events are signed with `X-Pulse-Signature: t=<timestamp>,v1=<HMAC>`. Addresses on the local network, loopback, link-local and cloud metadata ranges are refused.
- **API key:** random, stored as a hash, compared in constant time. It can send messages and read data. It cannot change settings or the WhatsApp connection, download backups or data exports, or change passwords.

## Data on disk

- Everything lives in `%APPDATA%\Pulse`, restricted to the Windows user.
- Contacts, messages, templates, campaigns, schedules, the activity log and backups are encrypted with AES-256-GCM. The key is protected with Windows DPAPI.
- Pulse 2.0 keeps no WhatsApp session database. Session files left by 1.x are not opened; Settings > Privacy and data offers to delete them (admin password required).
- Retention: messages older than the chosen number of days are deleted once a day; the message count is capped.
- Per customer: download their data or delete everything about them.

## What Pulse cannot protect against

- **Tunnel Provider In-Transit Visibility:** When using third-party managed tunnels (Cloudflare, ngrok), the tunnel provider terminates TLS at its edge and can inspect webhook payloads. Terminate TLS directly on your own infrastructure if third-party decryption is not permitted.
- Meta's own rules: a number with many blocks or reports gets lower limits or is restricted.
- Someone who is signed in to the same Windows account while Pulse is running can read what Pulse can read, including the unsealed token in memory. Use a Windows password and lock the computer.
- Anyone who obtains the access token can send messages as the business: revoke it in Meta Business settings if a computer is lost.
- The program is not code-signed unless you sign it with a trusted certificate (BUILD.md).
