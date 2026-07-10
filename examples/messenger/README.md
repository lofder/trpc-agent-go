# Messenger Agent (Meta Messenger Platform)

Let customers talk to a trpc-agent-go agent over **Facebook Messenger** — the
largest messaging app among US consumers (~195M users). Core two-way
messaging carries **no per-message fee** (unlike WhatsApp).

```
Customer (Messenger) ──▶ Meta ──▶ POST /messenger (this server)
                                       │
                                       ▼
                              runner.Run(agent) ── tools ──▶ answer
                                       │
                                       ▼
                       Graph API Send API ──▶ Customer (Messenger)
```

The webhook is acknowledged immediately and the agent runs asynchronously,
replying through the Send API — same pattern as the WhatsApp example.

Key platform rules (2026):

- Users must message the Page first (organic, Click-to-Messenger ads, m.me
  links, or the website chat plugin); then you reply freely within the
  standard **24-hour window**.
- Production access needs a Facebook Page + Meta App Review (Advanced Access
  to `pages_messaging`) + Meta Business Verification — plan 1–4 weeks.
  Before approval you can fully test with accounts that have a role on the app.
- Message tags `CONFIRMED_EVENT_UPDATE` / `POST_PURCHASE_UPDATE` /
  `ACCOUNT_UPDATE` stopped working on 2026-04-27; out-of-window
  transactional sends should migrate to Utility Message templates.

## What the demo agent can do

- `order_status` — look up a mock order by ID (try `A1001`, `A1002`, `A1003`)
- `current_time` — current time for a timezone

Replace the bodies in `tools.go` with real database / API calls.

## Setup

1. Create a Facebook Page and a Meta app (Business type) at
   [developers.facebook.com](https://developers.facebook.com), add the
   **Messenger** product.
2. In Messenger settings, generate a **Page access token** and note your
   **app secret** (App settings → Basic).
3. Export environment variables:

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="https://api.deepseek.com/v1"   # optional, provider-specific
export MESSENGER_PAGE_TOKEN="EAAG..."
export MESSENGER_APP_SECRET="your_app_secret"
export MESSENGER_VERIFY_TOKEN="any-string-you-choose"
```

## Run

```bash
cd examples/messenger
go run . -model deepseek-v4-flash -addr :8080
```

Expose the port publicly:

```bash
ngrok http 8080
```

In Meta App Dashboard → Messenger → Settings → Webhooks, set:

- Callback URL: `https://xxxx.ngrok.io/messenger`
- Verify token: the value of `MESSENGER_VERIFY_TOKEN`
- Subscription fields: `messages`

Then subscribe your Page and message it from a test account:

- `What's the status of order A1001?`
- `What time is it in UTC?`

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `OPENAI_API_KEY` | yes | Model provider API key |
| `OPENAI_BASE_URL` | no | Model provider base URL (for non-OpenAI providers) |
| `MESSENGER_PAGE_TOKEN` | yes | Page access token |
| `MESSENGER_APP_SECRET` | yes | App secret for `X-Hub-Signature-256` validation |
| `MESSENGER_VERIFY_TOKEN` | yes | Webhook verification token (your choice) |
| `MESSENGER_API_BASE` | no | Graph API base override (tests / version pinning) |

## Notes

- Mainland-China note: `graph.facebook.com` is blocked by the GFW — run this
  server overseas.
- Long replies are split at 1,900 characters (Messenger's limit is 2,000).
- Echo events (`message.is_echo`) are skipped so the bot never replies to its
  own messages.
