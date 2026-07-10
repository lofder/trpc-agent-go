# Slack Agent (Events API + Web API)

Let users talk to a trpc-agent-go agent in **Slack** — DM the bot directly or
`@mention` it in a channel (it replies in-thread). Slack apps are free; the
only setup is creating an app in your workspace.

```
User (DM / @mention) ──▶ Slack ──▶ POST /slack/events (this server)
                                        │
                                        ▼
                               runner.Run(agent) ── tools ──▶ answer
                                        │
                                        ▼
                        chat.postMessage ──▶ User (Slack)
```

Slack requires event deliveries to be acknowledged within **3 seconds**, so
the handler acks immediately and runs the agent asynchronously — same pattern
as the WhatsApp and Messenger examples. Request authenticity is verified via
the `X-Slack-Signature` (v0 HMAC-SHA256) header with replay protection, and
Slack's automatic retries (`X-Slack-Retry-Num`) are deduplicated.

## What the demo agent can do

- `order_status` — look up a mock order by ID (try `A1001`, `A1002`, `A1003`)
- `current_time` — current time for a timezone

Replace the bodies in `tools.go` with real database / API calls.

## Setup

1. Create an app at [api.slack.com/apps](https://api.slack.com/apps) → From
   scratch → pick your workspace.
2. **OAuth & Permissions** → Bot Token Scopes: add `chat:write`,
   `app_mentions:read`, `im:history`. Install the app to the workspace and
   copy the **Bot User OAuth Token** (`xoxb-...`).
3. **Basic Information** → copy the **Signing Secret**.
4. Export environment variables:

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="https://api.deepseek.com/v1"   # optional, provider-specific
export SLACK_BOT_TOKEN="xoxb-..."
export SLACK_SIGNING_SECRET="your_signing_secret"
```

## Run

```bash
cd examples/slack
go run . -model deepseek-v4-flash -addr :8080
```

Expose the port publicly:

```bash
ngrok http 8080
```

In your app config → **Event Subscriptions**: enable, set the Request URL to
`https://xxxx.ngrok.io/slack/events` (Slack sends a `url_verification`
challenge — this server answers it automatically), then under *Subscribe to
bot events* add `message.im` and `app_mention`, and save (reinstall if
prompted).

Then in Slack:

- DM the bot: `What's the status of order A1001?`
- In a channel: `@YourBot what time is it in UTC?` (replies in a thread)

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `OPENAI_API_KEY` | yes | Model provider API key |
| `OPENAI_BASE_URL` | no | Model provider base URL (for non-OpenAI providers) |
| `SLACK_BOT_TOKEN` | yes | Bot User OAuth token (`xoxb-...`) |
| `SLACK_SIGNING_SECRET` | yes | Verifies `X-Slack-Signature` |
| `SLACK_API_BASE` | no | Web API base override (tests) |

## Notes

- Sessions are scoped to `channel:user`, so DMs and channel threads keep
  separate conversation context.
- Bot/self messages and message subtypes (edits, joins) are ignored to avoid
  reply loops.
- Long replies are split at 3,900 characters (Slack's limit is 4,000).
- Alternative transport: Slack also offers Socket Mode (WebSocket, no public
  URL); this example uses the Events API to match the other channel examples.
