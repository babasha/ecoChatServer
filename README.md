# ecoChatServer

## HTTPS webhook endpoint

The server can now expose an HTTPS listener alongside the regular HTTP port. Set the following environment variables before starting the service:

- `ENABLE_HTTPS=true` to enable TLS mode (automatically enabled when both certificate paths are present).
- `TLS_CERT_FILE` absolute or relative path to the certificate file in PEM format.
- `TLS_KEY_FILE` path to the private key file.
- `HTTPS_PORT` optional port override (defaults to `8443`).

Both listeners reuse the same Gin router, so `/api/instagram/webhook` is available on HTTP and HTTPS simultaneously. During shutdown both servers are stopped gracefully.

### Local trusted certificate

For local tests you can generate a self-signed certificate, for example:

```bash
mkcert localhost 127.0.0.1 ::1
```

Then point `TLS_CERT_FILE` to the generated `localhost.pem` and `TLS_KEY_FILE` to `localhost-key.pem`.

## Instagram Direct integration checklist

1. Configure required environment variables or `app_settings` entries:
   - `INSTAGRAM_VERIFY_TOKEN` – the same token you provide to Meta when registering the webhook.
   - `INSTAGRAM_APP_SECRET` – used to validate `X-Hub-Signature-256` (set empty to skip signature validation while testing).
   - `INSTAGRAM_BUSINESS_ACCOUNT_ID` – set to your IG business account, e.g. `17841400772641672`.
   - `INSTAGRAM_CLIENT_API_KEY` – optional logical client key, defaults to `instagram_default_client`.
2. Register the webhook URL (e.g. `https://<your-domain>/api/instagram/webhook`) in Meta App Dashboard. The GET verifier endpoint still responds with the `hub.challenge` when the verify token matches.
3. Send a message to the connected Instagram account. The webhook responds with a JSON payload that now includes sender diagnostics:

```json
{
  "status": "received",
  "processed": 1,
  "messages": [
    {
      "sender_id": "1784...",
      "sender_username": "john_doe",
      "recipient_id": "1784...",
      "message_id": "mid.$cAAP...",
      "message_type": "text",
      "timestamp": "2024-03-01T12:00:00Z",
      "preview": "Hello from Instagram!"
    }
  ]
}
```

The same data is logged to the server logs, which helps to verify that the sender ID and username reach the backend correctly.
