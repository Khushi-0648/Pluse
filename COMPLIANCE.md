# Using Pulse within WhatsApp's rules

This is practical guidance, not legal advice. Check Meta's current [WhatsApp Business Messaging Policy](https://business.whatsapp.com/policy), [Commerce Policy](https://www.whatsapp.com/legal/commerce-policy) and the [WhatsApp Business Platform terms](https://www.whatsapp.com/legal/business-terms) yourself.

## The connection

Pulse connects only through **Meta's official WhatsApp Business Platform (Cloud API)**, with your own Meta app, number and access token. This is the way WhatsApp's terms allow businesses to send automated messages. (Pulse 1.x linked to WhatsApp like WhatsApp Web; that connection has been removed.)

Using the official platform does not make every message acceptable: Meta's policies still apply, Meta rates the quality of your number, and numbers that get many blocks or reports have their limits lowered or are restricted.

## Meta's main rules and how Pulse helps

| Rule | What Pulse does |
| --- | --- |
| Opt-in before messaging | Each contact has a consent record (source and date). Campaigns go only to opted-in contacts unless you switch that off. CSV import can read a consent column. Integrations cannot mark someone as opted in; the opt-in must be recorded in Pulse. |
| 24-hour customer service window | Free text is only sent within 24 hours of the customer's last message. Messages typed in the inbox, the `send` command and the integration endpoint are refused outside it, with a plain explanation. Campaigns and scheduled messages send the Meta-approved template you chose to customers outside the window, or skip them. Automatic replies always answer a message that just arrived. |
| Templates | Created and approved in WhatsApp Manager. In Pulse you enter the template name, language and which values (for example `{{first_name}}`) fill `{{1}}`, `{{2}}`... A template with an empty value is not sent. |
| Honour opt-outs | STOP, UNSUBSCRIBE, "rok do", "band karo", "mat bhejo" and similar mark the contact do-not-message immediately, on every way of sending. START or "shuru karo" opts back in. |
| Quality rating and messaging limits | Home shows Meta's quality rating. A red rating counts as "at risk": campaigns are paused and blocked. Keep the Careful pace while Meta's daily limit for a new number grows. |
| Blocked numbers, test mode, practice mode, daily limit | Apply to every way of sending: automatic replies, the inbox, scheduled messages, campaigns, integrations and the `send` command. |
| Reach a person | AGENT, HUMAN, PERSON, "talk to someone", "baat karni hai" or menu option 4 pause the bot in that chat and put it under *Needs attention*. |
| Do not spam | Daily limit, warm-up limit (50 a day), cooldown and per-customer reply cap, loop detection, randomised gaps and batch pauses, automatic pause after 5 failed sends or when several recipients opt out. |
| Accurate information | Default replies only use details you entered. A reply whose details are empty is never sent with a gap. |
| Protect personal data | Customer data is encrypted on disk; the access token and app secret are sealed with your Windows account; per-customer export and deletion; retention period. |

## Costs

Meta bills some messages (mostly templates) to your Meta account; prices depend on the category and the customer's country. Check WhatsApp Manager for current prices.

## Content

Do not send messages about illegal goods, gambling, adult content, weapons, drugs, deceptive offers, or anything Meta's Commerce and Business Messaging policies prohibit. Do not ask customers for card numbers, passwords or one-time codes.

## Independence

Pulse is independent software. It is not affiliated with, authorised, maintained, sponsored or endorsed by WhatsApp LLC or Meta Platforms, Inc. WhatsApp is a trademark of WhatsApp LLC.
