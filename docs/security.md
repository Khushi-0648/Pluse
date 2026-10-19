# Security, Privacy & Data Governance

## 1. Authentication & Token Management
- **Password Storage**: Passwords are hashed using bcrypt with salt rounds >= 12. Plaintext passwords never touch logs, storage, or external networks.
- **JWT Architecture**:
  - Access Token: Short-lived (15 minutes). Sent in the `Authorization: Bearer <token>` header.
  - Refresh Token: Long-lived (7 days). Stored in secure HTTP-only cookies or encrypted database sessions. Token rotation invalidates superseded refresh tokens upon use.
  - Revocation: Logouts push the JWT jti to a Redis revocation denylist until the expiry window lapses.

## 2. Multi-Tenant Authorization & Data Isolation
- Every relational query is constrained by the authenticated user's active `organization_id`.
- Tenant context is verified at the HTTP middleware boundary. Any attempt to modify resources belonging to another organization yields an immediate `403 Forbidden` and creates an audit log entry.

## 3. Cryptographic Storage of API Secrets
- Integration credentials (Meta Access Tokens, App Secrets) are encrypted at rest using **AES-256-GCM**.
- Initialization Vectors (IV / Nonces) are generated via cryptographically secure pseudo-random generators (`crypto/rand`).
- Frontend responses return only masked credential previews (e.g. `EAAG...abcd`).

## 4. Webhook Integrity & Replay Defense
- All Meta webhooks are verified via HMAC-SHA256 signatures (`X-Hub-Signature-256`). Payloads whose calculated MAC does not match the header are rejected with `401 Unauthorized`.
- Webhook deduplication using a 24-hour Redis/DB cache of message IDs (`wamid`) prevents duplicate workflow executions or duplicate agent replies.

## 5. Protection Against Server-Side Request Forgery (SSRF)
- The workflow action "Call External API" validates target URLs against strict network rules:
  - Private CIDRs (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`) are blocked.
  - Loopbacks (`127.0.0.1`, `localhost`) are blocked.
  - Cloud metadata IPs (`169.254.169.254`, `metadata.google.internal`) are blocked.
  - Maximum timeout is restricted to 10 seconds.

## 6. Privacy & WhatsApp Policy Compliance
- Contact opt-out status (`is_opted_out`) is strictly respected across all marketing broadcasts and campaign triggers.
- Unsubscribe keywords (e.g. `STOP`, `UNSUBSCRIBE`, `CANCEL`) automatically toggle opt-out status and append an immutable consent audit record.
