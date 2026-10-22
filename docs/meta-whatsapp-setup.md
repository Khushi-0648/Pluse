# Connecting Pulse to the WhatsApp Business Platform

From version 2.0, Pulse connects only through **Meta's official WhatsApp Business Platform (Cloud API)**. This is the way WhatsApp's terms allow businesses to automate messages. Every Pulse install uses the owner's **own** Meta app, number and access token: nothing goes through the Pulse publisher, and each `-session` profile (for a second number) is set up separately with its own details.

The linked-device (WhatsApp Web style, QR code) connection of Pulse 1.x has been removed.

## What you need

> [!WARNING]
> **Phone Number Coexistence Limitation (Mobile WhatsApp Disconnection):**
> Meta's official WhatsApp Business Platform (Cloud API) is strictly mutually exclusive with the WhatsApp mobile apps.
> - A phone number registered to Cloud API **cannot be logged into the WhatsApp consumer app or WhatsApp Business app on a mobile phone at the same time**.
> - If you register an existing mobile number, you must delete/deregister the WhatsApp account on your smartphone before adding it to Meta's API Setup.
> - Once connected, all messaging and customer chats are handled exclusively through Pulse's Web Inbox. Attempting to log back into WhatsApp on a phone will deactivate Cloud API message routing.
> - **Recommendation:** Use a dedicated business SIM, a virtual phone number, or a business landline.

- A Meta (Facebook) account and a Meta Business portfolio for your business.
- A dedicated phone number for the business (SMS or voice call capable for receiving Meta's 6-digit registration code).
- A permanent HTTPS webhook ingress (e.g. a Named Cloudflare Tunnel or static ngrok domain) so Meta can deliver incoming messages to Pulse.
- A permanent System User Access Token from Meta Business Manager with `whatsapp_business_messaging` permissions.

## Step by step

1. **Create the app.** At [developers.facebook.com](https://developers.facebook.com), choose *My Apps > Create app*, pick the type **Business**, and connect it to your business portfolio.
2. **Add WhatsApp.** In the app, add the **WhatsApp** product. Under *WhatsApp > API Setup*, add your business phone number and verify it.
3. **Copy the two IDs.** On *WhatsApp > API Setup*, copy the **Phone number ID** and the **WhatsApp Business account ID**. (They are long numbers. The phone number ID is not your phone number.)
4. **Create a permanent access token.** The token shown on API Setup expires after 24 hours. Instead, in [Meta Business settings](https://business.facebook.com/settings) > *Users > System users*, add a system user (role Admin), use *Assign assets* to give it your app and your WhatsApp account (full control), then *Generate new token* for your app with the permissions **whatsapp_business_messaging** and **whatsapp_business_management**, and expiry *Never*. Copy the token; Meta shows it only once.
5. **Copy the app secret.** In the app, *App settings > Basic*, press *Show* next to **App secret** and copy it. Pulse uses it to check that incoming messages really come from Meta.
6. **Expose Pulse over https (Webhook Ingress).** Meta needs a public https URL to deliver incoming customer messages to Pulse:

   - **Quick Test (Changes on restart):** `cloudflared tunnel --url http://127.0.0.1:8085` prints a temporary address (e.g. `https://xyz.trycloudflare.com`). **Note:** Quick tunnels generate a new address each time they restart, which breaks Meta's webhook delivery until updated.
   - **Production (Fixed Hostname):** Set up a free **Named Cloudflare Tunnel** with a permanent custom domain (e.g. `webhook.yourcompany.com`) or reserve a free static ngrok domain (`ngrok http 8085 --domain=your-name.ngrok-free.app`). These retain the exact same URL across reboots and run as an automatic Windows service.
   - **Tunnel Privacy Disclosure:** Managed tunnels (Cloudflare, ngrok) terminate TLS at their edge, meaning the tunnel provider technically decrypts the incoming webhook payload (customer numbers, names, and messages) before forwarding it to your computer. For strict data sovereignty (healthcare, legal, finance), terminate TLS directly on your own infrastructure using a local reverse proxy (such as Caddy or Nginx with Let's Encrypt), or use a private TCP pass-through stream proxy.
   - **Isolation:** Only Pulse's webhook address (`/cloud/webhook`) answers through the public tunnel; the dashboard and sign-in page remain private (they answer 404 under the tunnel's hostname).
7. **Enter the details in Pulse.** In the dashboard, open *Settings > WhatsApp connection*. Paste the phone number ID, the WhatsApp Business account ID, the access token, the app secret and, as **Public address of this computer**, the https address of the tunnel. Press **Test connection**: Pulse shows your number and the name WhatsApp displays to customers. Press **Save** and enter your admin password.
8. **Set up the webhook in Meta.** Pulse now shows the **Webhook address** (the public address followed by `/cloud/webhook`) and a **Verify token**. In the app, *WhatsApp > Configuration*, press *Edit* next to Webhook, paste both, and press *Verify and save*. Then, under *Webhook fields*, **subscribe to `messages`**. Send a message from another phone to your business number: it appears in the Pulse inbox, and the *Get started* list on Home ticks off "Receive messages".
9. **Create message templates.** WhatsApp lets you reply freely for **24 hours** after a customer's last message. To reach anyone outside that window (campaigns, reminders), create templates in [WhatsApp Manager](https://business.facebook.com/wa/manage/message-templates/) and wait for Meta to approve them. In Pulse, enter the template name, language code and which values fill `{{1}}`, `{{2}}`... under *Official WhatsApp template* in a campaign or scheduled message.
10. **Billing.** Meta charges per message for some categories (mostly templates: marketing, utility, authentication); replies within the 24-hour window are generally free. Add a payment method in WhatsApp Manager and check Meta's current pricing for your country.

## How Pulse follows the platform's rules

| Rule | What Pulse does |
| --- | --- |
| 24-hour customer service window | Free text (auto-replies, inbox, the `send` command, the integration endpoint) only goes to customers who wrote in the last 24 hours; otherwise it is refused before anything is sent. Campaigns and scheduled messages send your approved template instead, or skip the customer. |
| Opt-in | Campaigns go only to contacts with a recorded opt-in (on by default). |
| Opt-out | STOP and similar words are honoured at once on every way of sending. |
| Quality rating | Shown on Home. A red rating counts as "at risk": running campaigns are paused and new ones are blocked. |
| Messaging limits | Meta limits how many people a new number can message per day. Keep the Careful pace while the limit grows. |
| Rate limits | Requests that Meta refuses with "too many requests" or a server error are retried after a pause; a request that may already have reached Meta is never repeated, so customers never get duplicates. |

## Troubleshooting

| Pulse says | What to do |
| --- | --- |
| The access token has expired or is wrong | Create a permanent system-user token (step 4) and paste it in Settings > WhatsApp connection. |
| More than 24 hours have passed... | Use an approved template (campaign or scheduled message), or wait for the customer to write. |
| WhatsApp refused the template | Check the template name, language code and number of values in WhatsApp Manager, and that it is approved. |
| This number is not on your test number's list | You are using Meta's test number: add the recipient under API Setup, or register your real number. |
| Messages are being sent too fast | Wait a few minutes; slow the pace in Settings > Safety and pace. |
| Nothing arrives in the inbox | Check that the tunnel is running and its address matches the Public address, that the webhook is verified in Meta and subscribed to `messages`, and that the app secret is saved. Rejected deliveries are listed in the activity log as "Blocked a webhook call that did not come from Meta". |

## Moving from Pulse 1.x (linked device)

Contacts, messages, templates, campaigns, scheduled messages and settings are kept. The old WhatsApp session files (`whatsapp.db`, `whatsapp.db.sealed`) are no longer used and are not touched automatically; *Settings > Privacy and data* offers to delete them (admin password required). On the phone, remove "Pulse" from *WhatsApp > Linked devices*. Note that a number registered on the Cloud API can no longer be used in the WhatsApp app on a phone at the same time.
