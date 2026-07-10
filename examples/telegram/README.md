# Telegram Agent (Bot API)

Let users talk to a trpc-agent-go agent over **Telegram**. Zero cost, zero
review: create a bot with [@BotFather](https://t.me/BotFather) and you are
live the same day. This example uses **long polling**, so no public URL,
webhook, or TLS certificate is needed — it runs from behind any NAT.

```
User (Telegram) ──▶ Bot API ──▶ getUpdates (this process)
                                     │
                                     ▼
                            runner.Run(agent) ── tools ──▶ answer
                                     │
                                     ▼
                          sendMessage ──▶ User (Telegram)
```

Key platform rules (2026):

- Bots **cannot message a user first** — the user must press Start.
- After that there is **no 24-hour window**: replies and proactive pushes are
  free and unlimited (until the user blocks the bot).
- The Bot API has **no platform or per-message fees**.

## What the demo agent can do

- `order_status` — look up a mock order by ID (try `A1001`, `A1002`, `A1003`)
- `current_time` — current time for a timezone

Replace the bodies in `tools.go` with real database / API calls.

## Setup

1. Open [@BotFather](https://t.me/BotFather) in Telegram → `/newbot` → pick a
   name and a username ending in `bot` → copy the token.
2. Export environment variables:

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="https://api.deepseek.com/v1"   # optional, provider-specific
export TELEGRAM_BOT_TOKEN="123456:ABC-DEF..."
```

## Run

```bash
cd examples/telegram
go run . -model deepseek-v4-flash
```

Open your bot in Telegram, press **Start**, then try:

- `What's the status of order A1001?`
- `What time is it in UTC?`

## Notes

- Mainland-China note: `api.telegram.org` is blocked by the GFW — run this
  process on an overseas server.
- For production you can switch to webhook mode (`setWebhook`) or a
  self-hosted Bot API server; set `TELEGRAM_API_BASE` to point elsewhere.
- Long replies are split at 4,000 characters (Telegram's limit is 4,096).
