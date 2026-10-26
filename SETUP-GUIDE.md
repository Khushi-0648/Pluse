# Pulse setup guide

## 1. Before you start

- Pulse connects through **Meta's official WhatsApp Business Platform (Cloud API)** with your own Meta account. You need a Meta Business portfolio, a developer app and a business phone number registered to the platform. [docs/meta-whatsapp-setup.md](docs/meta-whatsapp-setup.md) explains every step.
- Meta delivers incoming messages over the internet, so the computer running Pulse needs an https tunnel or reverse proxy (for example Cloudflare Tunnel) while it runs.
- Read [COMPLIANCE.md](COMPLIANCE.md): opt-in, the 24-hour window, templates and quality rating.

## 2. First start

1. Start `whatsapp-auto.exe` (or `Run-App.bat`). A window opens with the dashboard.
2. Sign in with the **one-time password**. It is shown in the console window and saved in `FIRST-RUN-PASSWORD.txt` in the data folder (`%APPDATA%\Pulse\`). The file is deleted when you choose your password.
3. Choose a password: at least 8 characters, letters and numbers, no easy words such as "admin" or "password".

## 3. Connect your WhatsApp Business API

1. Create your Meta app and get the **phone number ID**, **WhatsApp Business account ID**, a permanent **access token** and the **app secret** (docs/meta-whatsapp-setup.md, steps 1-5).
2. Start an https ingress for Pulse (so Meta can deliver incoming messages):
   - **Production (Fixed URL):** For daily business use, set up a free **Named Cloudflare Tunnel** with a fixed custom domain, a static ngrok domain, or a direct HTTPS reverse proxy (Caddy/Nginx). Do not use quick tunnels (`cloudflared tunnel --url...`) in production: they assign a random temporary address that changes on every reboot, breaking message reception.
   - **Privacy Notice:** Managed tunnels (Cloudflare, ngrok) terminate TLS at their cloud edge, meaning the tunnel provider can inspect incoming webhook payloads. For full data sovereignty, terminate TLS directly on your own server.
   - Note the https address (e.g., `https://webhook.yourcompany.com`).
3. In **Settings > WhatsApp connection**, paste the values and the tunnel address as *Public address of this computer*, press **Test connection**, then **Save** (admin password).
4. In Meta's *WhatsApp > Configuration*, set the **Callback URL** to the *Webhook address* and the **Verify token** shown by Pulse, verify, and subscribe to the **messages** field.

The access token and app secret are stored sealed with your Windows account and are never shown again; leave the fields empty when saving other changes to keep them.

## 4. Finish the checklist

The **Get started** card on Home lists what is left:

1. **WhatsApp's messaging rules** - read and accept them.
2. **Connect your WhatsApp Business API** and **Receive messages** - see step 3.
3. **Business details** (Settings > Business details) - name and opening hours are required; services, prices, address and website are optional. Default replies use these through variables like `{{business_hours}}`. A reply that needs an empty detail is never sent with a gap: the customer gets the holding reply instead ("A member of our team will reply to you here shortly", editable under Auto-replies) and the chat appears under *Needs attention*. Like a request for a person, this pauses automatic replies in that chat (24 hours by default, Settings > Safety and pace) until someone presses *Resume automatic replies*.
4. **Test message** - sends a message to your own mobile (not the business number). Send any message from that mobile to the business number first: WhatsApp only allows free text within 24 hours of the customer writing.
5. **Switch automatic replies on.**

Then review **Auto-replies** (rules and built-in replies) and **Settings > Opening hours**.

## 5. Choose a sending pace

Settings > Safety and pace:

| Pace | Use it for | Per day | Gap between campaign messages |
| --- | --- | --- | --- |
| Careful (default) | New numbers, first two weeks | 100 (50 while warm-up is on) | 8-20 s |
| Balanced | Numbers with a few weeks of normal conversations | 300 | 5-12 s |
| Fast | Numbers with months of history and almost no reports | 800 | 3-7 s |

Pulse also stops replying to a customer after a set number of automatic replies per hour, detects bot-to-bot loops, and pauses campaigns when many messages fail or people opt out.

Scheduled messages use the same pace as campaigns: the same gaps between messages and the same daily limit. Messages you type and send yourself from the dashboard also count towards the daily limit.

**Testing:** type a message under *Auto-replies > Test a message* to see the reply a customer would get. To try it for real, turn on *Test mode* with your own mobile as a test number and write to the business number from it. *Practice mode* shows what would be sent without sending anything, including the messages you send yourself from the dashboard.

**The 24-hour window:** WhatsApp lets you reply freely for 24 hours after a customer's last message. After that only message templates approved by Meta can be sent. Create them in WhatsApp Manager, then enter the template name, language code and values under *Official WhatsApp template* in a campaign or scheduled message. Messages typed in the inbox to customers outside the window are refused with an explanation.

## 6. Staff

Settings > Account > Staff sign-in creates a second password. Staff can use the inbox, contacts, templates and scheduled messages but cannot see or change settings, rules or campaigns. In the inbox, each chat can be **assigned** to a person (the *Unassigned* filter shows what nobody has picked up) and given an **internal note** that the customer never sees. Every sign-in and change is in the activity log (Settings > Account), which you can download as CSV.

## 7. Keep it running

- Settings > Account > **Start Pulse when Windows starts** runs it in the background after every restart.
- In Windows power settings, set the computer to never sleep while plugged in.
- Alternatively run `scripts\Install-Service.ps1` (right-click > Run with PowerShell) to register a scheduled task that starts Pulse when you sign in and starts it again if it crashes. It also starts Pulse straight away; to only create the task, run it with `-NoStart`. `scripts\Stop-Background.ps1` closes it normally and `scripts\Uninstall-Service.ps1` removes the task.
- Keep the tunnel running too (Cloudflare can install `cloudflared` as a Windows service). While it is off, Meta keeps incoming messages for a while and delivers them later.
- Close Pulse normally (Ctrl+C in its window, or `whatsapp-auto stop`). Then the last changes are saved.
- Only one copy can run per data folder (the running copy holds a lock on `pulse.lock` there). Starting a second copy opens the dashboard of the running one; `stop`, `disconnect`, `reset-password` and `send` also see a copy that is still starting.
- Every start and stop is in the activity log with the reason, and a crash writes `crash.log` in the data folder, so an unexpected stop can be explained.

## 8. Alerts

When a customer asks for a person, or asks something whose answer needs a business detail you have not filled in, the chat appears under **Needs attention**, the inbox badge goes up and the dashboard plays a short chime. Alerts stay inside the Pulse window: customer messages are never shown as Windows pop-ups. Settings > Safety and pace > Alerts lets you mute the chime and play a test. Keep the Pulse window open (it can be minimised) to hear it, or forward the `handover.requested` webhook event to email or chat tools.

## 8a. Backups and forgotten passwords

- Every day Pulse saves an encrypted backup (contacts, rules, templates, business details, opening hours) in `%APPDATA%\Pulse\backups` and keeps the last 7. Settings > Privacy and data can also save a copy to another folder on this computer, such as a USB drive or a synced folder (for example OneDrive), and restore any of them. Network folders (addresses that start with `\\`, and network drives mapped to a letter) are not allowed, and the folder must be one your Windows account can write to. These copies only open on the same Windows account; to move to a new computer use *Download backup*.
- Forgot the admin password? Close Pulse and double-click `Reset-Password.bat`. Windows asks for administrator permission; choose *Yes*. You get a new one-time password. Run it from your own Windows account; administrator rights are a hurdle against a quick takeover, not full protection, so keep your Windows account locked. Messages, contacts and the WhatsApp connection are kept, every reset is in the activity log, and the dashboard warns about it for 7 days.
- On a **standard (non-administrator) Windows account**: typing an administrator's password when Windows asks runs the reset as the administrator, whose account holds no Pulse data, so nothing is changed (Pulse's data only opens on the account that created it). Instead, an administrator makes your account an administrator for a moment (Settings > Accounts > Other users > your account > Change account type), you sign out and in again as yourself, close Pulse and run `Reset-Password.bat`, and the administrator then changes your account back to *Standard user*. See also HOW-TO-RUN.txt.

## 8b. Signing out and deleting data

- **Sign out** only signs you out of the dashboard. Nothing is deleted and WhatsApp stays connected.
- **Disconnect** (Settings > WhatsApp connection, or `Run-Unlink.bat`) removes the access token and app secret from this computer. Messages and contacts are kept unless you choose otherwise.
- To delete all of Pulse's data on this computer, go to **Settings > Privacy and data**. Pulse asks for the admin password before deleting anything. This also deletes the daily backups, including the copies in the extra backup folder (download a backup first if you want to keep one). Opt-outs are remembered, so restoring a backup later never messages someone who said STOP.

## 9. Upgrading

Close the old version first, then unzip the new version into any folder (it does not have to be the old one; `C:\Program Files` works too). Settings, the password and the WhatsApp connection live in `%APPDATA%\Pulse\config.yaml` together with the data, so every copy of Pulse uses them.

**Going back to 2.0.x is not supported.** From 2.1 on, every encrypted data file is tied to its file name, and Pulse 2.0.x cannot open files that 2.1 has written (2.1 still opens the files 2.0.x wrote). Before the first start of 2.1, close Pulse and copy the whole `%APPDATA%\Pulse` folder somewhere safe. To return to 2.0.x later, close Pulse and put that copy back (or a backup made by 2.0): changes made since the upgrade are then lost. Do not start 2.0.x on a data folder that 2.1 has used.

**From 2.0.0:** 2.0.0 kept `config.yaml` in the program folder. Start the new version once from the old folder, or copy the old `config.yaml` next to the new `whatsapp-auto.exe`, and Pulse moves it to `%APPDATA%\Pulse` by itself. If Pulse finds your data but no settings, it does not start with a new password; it tells you where to copy `config.yaml` from.

**From 1.4 - 1.6 (linked device) to 2.0:**

1. Close the old version completely (check Task Manager for `whatsapp-auto.exe`).
2. Replace `whatsapp-auto.exe` with the new one and start it. Contacts, messages, templates, campaigns, scheduled messages, rules and settings load unchanged.
3. Pulse 2.0 no longer links like WhatsApp Web. Until you enter your Business API details (step 3), Home shows *Connect your WhatsApp Business API*, and automatic replies and campaigns do not start.
4. The old session files (`whatsapp.db`, `whatsapp.db.sealed`) are no longer used and are not touched; *Settings > Privacy and data* offers to delete them. On the phone, remove Pulse from *Linked devices*. Settings that only applied to the linked device (importing past chats) are ignored.

**From 1.3.x:** Pulse imports `contacts.json`, `messages.json`, `scheduled.json` and `templates.json` from the program folder into the encrypted data folder. The unencrypted originals are kept in `%APPDATA%\Pulse\old-version-backup\<date>\`; delete that folder after checking your data. If the old version is still running when you start the new one, nothing is imported; close it and start again.

## 10. Remote access

The dashboard listens on `127.0.0.1` only. The tunnel you set up for Meta only reaches the webhook address; the dashboard answers "not found" under the tunnel's name. If you use your own reverse proxy (Caddy, Nginx) for Meta, forward only the path `/cloud/webhook` to Pulse: a proxy that forwards everything and adds no `X-Forwarded-For` header (Nginx's default) looks like a local browser to Pulse and would show the sign-in page to the internet. To open the dashboard from another device, use a second, separately protected tunnel or HTTPS reverse proxy with its own host name, add that name to `server.allowed_hosts` in `%APPDATA%\Pulse\config.yaml`, and add the proxy address to `server.trusted_proxies`. Never expose it directly with `host: 0.0.0.0`.

The **API key** (for connecting other programs) can only send messages and read messages, conversations, contacts and templates. Everything else, including scheduling, consent changes, reports, settings, deleting and exports, needs someone signed in to the dashboard.
