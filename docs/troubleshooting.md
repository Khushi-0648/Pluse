# Troubleshooting & Diagnostics

## Common Issues & Solutions

### 1. Webhook Verification Fails (`403 Forbidden` / Token Mismatch)
- **Symptom**: Meta reports "The URL couldn't be validated."
- **Root Cause**: The `hub.verify_token` sent by Meta does not match `META_WEBHOOK_VERIFY_TOKEN` configured in `.env`.
- **Fix**: Verify your `.env` value matches the verify token entered in the Meta App Developer Portal.

### 2. Message Sending Returns `131047: Re-engagement message`
- **Symptom**: Outbound message fails with error code `131047`.
- **Root Cause**: More than 24 hours have elapsed since the user's last message to your business number. Free-form text cannot be sent outside this window.
- **Fix**: Send an approved Meta WhatsApp Message Template instead.

### 3. Signature Verification Fails (`401 Invalid Signature`)
- **Symptom**: Incoming webhook payload is rejected.
- **Root Cause**: `META_APP_SECRET` does not match the App Secret of the Meta application issuing the webhook.
- **Fix**: Retrieve the App Secret from Meta App Dashboard > App settings > Basic, and update your configuration.

### 4. Database Connection Refused
- **Symptom**: Go backend exits with `dial tcp: connection refused`.
- **Fix**: If running standalone without Docker, ensure `DB_DRIVER=sqlite` in `.env`, or ensure local PostgreSQL service is started on port 5432.
