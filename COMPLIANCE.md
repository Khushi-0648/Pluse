# WhatsApp Automation & Meta Policy Compliance Guide

## Overview
Pulse WhatsApp Automation Platform is designed to operate safely, ethically, and responsibly. 

> [!IMPORTANT]
> **Architecture & Protocol Notice**: Pulse connects using WhatsApp Web's Multi-Device companion protocol (via `whatsmeow`). It is an independent software tool and is **not** an official Meta WhatsApp Cloud API partner application. While Pulse incorporates multi-layered anti-ban safeguards (randomized jitter delays, automatic STOP opt-out compliance, consecutive failure circuit-breakers, warmup pacing), users must practice responsible opt-in messaging to maintain good account reputation.

---

## 1. Explicit Consent & Opt-In Policy
* **Requirement**: You must only send messages to contacts who have explicitly consented (opted in) to receive communications from your organization.
* **Prohibited**: Scraping phone numbers from public directories, purchasing contact lists, or sending cold outreach messages is strictly against WhatsApp terms and risks instant account bans.
* **Best Practice**: Keep records of how each contact opted in (e.g., website contact form, checkout checkbox, or inbound support request).

---

## 2. Mandatory Unsubscribe & STOP Compliance
* **Mechanism**: Pulse Automation includes built-in keyword listeners for:
  `STOP`, `UNSUBSCRIBE`, `OPTOUT`, `CANCEL`, `QUIT`.
* **Behavior**: When an incoming message matches an opt-out keyword, the system:
  1. Instantly tags the contact as `Opted-Out`.
  2. Sets the contact's Do-Not-Disturb (DND) status to `ACTIVE`.
  3. Halts any future broadcast or automated message dispatches to that phone number.
  4. Dispatches a confirmation acknowledgment informing the user how to resubscribe (`START`).

---

## 3. Account Warming-Up Schedule (Anti-Ban Protocol)
When connecting a new phone number to WhatsApp Automation, abrupt high-volume dispatches trigger automated heuristic flags. Follow this graduated schedule:

| Timeline | Max Messages / Day | Recommended Delay | Target Audience |
| :--- | :--- | :--- | :--- |
| **Day 1 – 3** | 20 – 50 messages | 8 – 15 seconds | Existing loyal clients |
| **Day 4 – 7** | 50 – 100 messages | 6 – 12 seconds | Active subscribers |
| **Week 2** | 100 – 250 messages | 5 – 10 seconds | Segmented customer lists |
| **Week 3+** | 250 – 500+ messages | 4 – 10 seconds | General opt-in database |

> [!TIP]
> Enable **Warmup Mode** in Settings to automatically enforce a safe ceiling of 50 messages per day while establishing account reputation.

---

## 4. Anti-Ban Technical Safeguards
Pulse WhatsApp Automation incorporates multi-layered technical protections:
1. **Randomized Jitter Delays**: Broadcast dispatches randomize pacing between 4 and 10 seconds, mimicking human interaction.
2. **Consecutive Error Circuit Breaker**: If 5 consecutive messages fail delivery, the campaign terminates immediately to prevent cascading bans.
3. **Typing Simulation**: The platform simulates the WhatsApp `composing...` status before sending auto-replies.
4. **Human Handover Pause**: When a user requests an agent, automated bot responses are suspended for 24 hours to prevent bot spam during live discussions.

---

## 5. Acceptable Content
* You must not send messages promoting illegal goods, gambling, adult content, fraudulent schemes, or deceptive claims.
* Ensure all transactional updates (order status, appointment reminders) are timely, accurate, and concise.

---

## Legal Disclaimer
Pulse WhatsApp Automation Platform is an independent automation software solution. It is not affiliated, associated, authorized, endorsed by, or in any way officially connected with WhatsApp LLC, Meta Platforms, Inc., or any of their subsidiaries or affiliates.
