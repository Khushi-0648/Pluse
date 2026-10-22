# Pulse setup guide

## 1. Before you start

### Critical Phone Number & Mobile App Requirements
Pulse connects exclusively through **Meta's official WhatsApp Business Platform (Cloud API)** with your own Meta account.
- **Dedicated Phone Number Required:** Meta does **not** allow a phone number registered to Cloud API to be active in the WhatsApp consumer app or the WhatsApp Business app on a mobile phone at the same time.
  - **Best Practice:** Use a separate business SIM, a virtual phone number, or a business landline.
  - **If Migrating an Existing Mobile Number:** You must delete/deregister the WhatsApp account on your smartphone before registering it in Meta's Developer portal. Once connected to Cloud API, all chats, customer communications, and history are managed through the Pulse Web Inbox on your computer. Logging into WhatsApp on your phone will disconnect the Cloud API.
- **Meta Platform Prerequisites ("Heavy Setup"):**
  1. A Meta Developer Account ([developers.facebook.com](https://developers.facebook.com)) and a Meta Business Portfolio.
  2. A permanent System User Access Token with `whatsapp_business_messaging` and `whatsapp_business_management` permissions.
  3. A fixed HTTPS webhook ingress (e.g. a Named Cloudflare Tunnel or static ngrok domain) so Meta can deliver incoming messages to Pulse.
  4. Pre-approved Message Templates in WhatsApp Manager for outbound customer-initiated conversations outside the 24-hour service window.
- Read [docs/meta-whatsapp-setup.md](docs/meta-whatsapp-setup.md) for step-by-step instructions.
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
3. **Business details** (Settings > Business details) - name and opening hours are required; services, prices, address and website are optional. Default replies use these through variables like `{{business_hours}}`. A reply that needs an empty detail is never sent with a gap: the customer gets the holding reply instead ("A member of our team will reply to you here shortly", editable under Auto-replies) and the chat appears under *Needs attention*.
4. **Test message** - sends a message to your own mobile (not the business number). Send any message from that mobile to the business number first: WhatsApp only allows free text within 24 hours of the customer writing.
5. **Switch automatic replies on.**

## 5. Keep Pulse running

Pulse processes messages, schedules and the bot only while it is running.

- **Tray / desktop mode:** Keep Pulse open or minimized to the taskbar.
- **Windows background service:** Run `powershell -ExecutionPolicy Bypass -File scripts\Install-Service.ps1` as administrator. Pulse runs silently in the background, starts on Windows boot, and restarts automatically if terminated.
- **To stop background service:** Run `powershell -ExecutionPolicy Bypass -File scripts\Stop-Background.ps1` or `Uninstall-Service.ps1`.

## 6. Upgrading and Backups

- Data is automatically migrated and backed up to `%APPDATA%\Pulse`.
- Daily encrypted rolling backups (7 days) are saved automatically.
