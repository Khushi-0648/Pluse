# Pulse WhatsApp Automation — Comprehensive Setup & Deployment Guide

## 1. Quick Start
1. **Launch Desktop Window**: Double-click `Run-App.bat`. This starts the local server on `http://127.0.0.1:8085` and launches a native standalone desktop app window.
2. **Launch Background Server**: Double-click `Run-Server.bat` if you prefer running headless in the background or accessing via a web browser.
3. **Log In & First-Time Setup**: Open `http://127.0.0.1:8085` in any browser. On first boot, the application generates a secure random temporary master password displayed in your console. Enter it to log in, and you will be immediately prompted to set your permanent master password.

---

## 2. Linking Your WhatsApp Account
Pulse connects as a linked secondary device using the WhatsApp Web Multi-Device protocol (via the open-source `whatsmeow` engine). It runs independently on your computer without requiring WhatsApp Web to stay open in a browser.

### Option A: QR Code Scan
1. On the Dashboard tab, locate the QR code box.
2. Open WhatsApp on your mobile phone.
3. Tap **Settings** (or the 3 dots on Android) > **Linked Devices** > **Link a Device**.
4. Point your phone camera at the QR code on your computer screen.
5. The device status will switch to **Connected** within 2-4 seconds.

### Option B: 8-Digit Phone Pairing Code
1. If your camera is unavailable or blurry, navigate to **Method 2** on the Dashboard.
2. Enter your phone number with country code (e.g., `+14155552671` or `+919876543210`).
3. Click **Get Code**. An 8-character code will appear on the dashboard.
4. On your mobile phone, go to **Linked Devices** > **Link a Device** > **Link with phone number instead**.
5. Enter the 8-character code to pair.

---

## 3. Running 24/7 (Always-On Guide)
WhatsApp Multi-Device requires the host computer to maintain an active network connection. If your computer goes to sleep, the bot cannot process incoming messages.

### Method 1: Prevent Windows Sleep
1. Open **Windows Settings** > **System** > **Power & battery**.
2. Under **Screen and sleep**, set:
   * "When plugged in, put my device to sleep after" -> **Never**.

### Method 2: Running as a Windows Background Service (NSSM)
To keep Pulse running continuously even after logging out:
1. Download NSSM (Non-Sucking Service Manager) from `https://nssm.cc/`.
2. Open Administrator Command Prompt:
   ```cmd
   nssm install PulseWhatsApp "C:\Path\To\whatsapp-auto.exe"
   nssm set PulseWhatsApp AppDirectory "C:\Path\To\"
   nssm start PulseWhatsApp
   ```

### Method 3: Cloud VPS / Dedicated Server
Deploy `whatsapp-auto.exe` on a low-cost Windows VPS (or Linux binary) with a static IP for 99.99% automated uptime.

---

## 4. Multi-Instance Support (Multiple WhatsApp Numbers)
To run multiple WhatsApp accounts on the same computer:
1. Duplicate the app folder into a separate directory (e.g. `WhatsApp-Auto-2`).
2. Edit `config.yaml` in the second folder:
   ```yaml
   server:
     port: 8086 # Different port
   whatsapp:
     db_path: whatsapp_num2.db # Different database file
     session_name: number2
   ```
3. Run the second instance. It will operate independently without session conflicts.

---

## 5. Security & Firewall Recommendations
* By default, the server listens exclusively on `127.0.0.1:8085` (localhost) to prevent exposure on local networks.
* If you need remote access, use an encrypted reverse proxy such as **Cloudflare Tunnel** (`cloudflared`) or a VPN rather than exposing raw HTTP ports.
* Ensure you configure a strong permanent master password (minimum 8-12 characters) during first-time initialization.

---

## 6. Troubleshooting
| Issue | Resolution |
| :--- | :--- |
| **Port 8085 in use** | Change `port: 8086` in `config.yaml` or terminate existing process using `netstat -ano \| findstr :8085`. |
| **QR Code expired** | Click **🔄 Reset / Relink** on the Dashboard to refresh the QR code stream. |
| **Session disconnected** | Ensure phone has internet access, or unlink device in phone settings and scan QR code again. |

---

## 7. Contact CSV Format & Template Variables

### CSV Import Format
When preparing your contact lists in Excel or Google Sheets, export as CSV with these exact columns:
```csv
phone,name,email,tags,notes
+14155552671,Alice Smith,alice@example.com,VIP;Client,Key account lead
+919876543210,Rahul Verma,rahul@example.in,Customer;Lead,Mumbai office
+447911123456,Emma Watson,emma@example.co.uk,Prospect,UK branch
```

* **Phone**: Always include the international country code with a leading `+` (e.g. `+1` for USA/Canada, `+91` for India, `+44` for UK).
* **Tags**: Separate multiple tags using semicolons (`;`).
* **Name & Notes**: Optional but recommended for personalized templates.

### Dynamic Message Variables
You can use variables in broadcast campaigns, automated rules, and canned templates:
* `{{greeting}}` or `{greeting}` — Automatically selects "Good morning", "Good afternoon", "Good evening", or "Hello" based on time.
* `{{name}}` or `{name}` — Recipient's full name (falls back to "there" if unnamed).
* `{{first_name}}` or `{first_name}` — Recipient's first name.
* `{{phone}}` or `{phone}` — Recipient's normalized phone number.
* `{{time}}` or `{time}` — Current dispatch time (`15:04:05`).
* `{{date}}` or `{date}` — Current date (`YYYY-MM-DD`).
* `{{message}}` or `{message}` — The incoming message text (in auto-replies).
