# WhatsApp Agent (via Twilio)

Let customers talk to a trpc-agent-go agent over **WhatsApp**, using **Twilio**
as the WhatsApp Business Solution Provider (BSP). No Meta business verification
is required to test — Twilio's free **WhatsApp Sandbox** is enough.

```
Customer (WhatsApp) ──▶ Twilio ──▶ POST /whatsapp (this server)
                                        │
                                        ▼
                              runner.Run(agent) ── tools ──▶ answer
                                        │
                                        ▼
                        Twilio REST API ──▶ Customer (WhatsApp)
```

Because an LLM turn can take longer than Twilio's webhook timeout, the handler
acknowledges the webhook immediately with an empty TwiML response and sends the
real reply asynchronously through the Twilio REST API.

## What the demo agent can do

The agent is wired with two demo tools so it actually *does work*, not just chat:

- `order_status` — look up a mock order by ID (try `A1001`, `A1002`, `A1003`)
- `current_time` — current time for a timezone

Replace the bodies in `tools.go` with real database / API calls for your use case.

## Prerequisites

1. A Twilio account (free trial works).
2. Twilio **WhatsApp Sandbox** activated:
   Twilio Console → **Messaging → Try it out → Send a WhatsApp message**.
3. Your phone joined to the sandbox: send the `join <code>` message shown there
   to the sandbox number (`+1 415 523 8886`) from WhatsApp.
4. A model provider key (`OPENAI_API_KEY`, and `OPENAI_BASE_URL` if not OpenAI).
5. [`ngrok`](https://ngrok.com/) (or any public HTTPS tunnel) for local testing.

## Configure

```bash
# Model provider
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="https://api.deepseek.com/v1"   # optional, provider-specific

# Twilio (from the Console; keep the auth token secret!)
export TWILIO_ACCOUNT_SID="ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
export TWILIO_AUTH_TOKEN="your_auth_token"
export TWILIO_WHATSAPP_FROM="whatsapp:+14155238886"    # sandbox number
```

## Run

```bash
cd examples/whatsapp
go run . -model deepseek-v4-flash -addr :8080
```

In another terminal, expose the port:

```bash
ngrok http 8080
# copy the https URL, e.g. https://xxxx.ngrok.io
```

Then in Twilio Console → Sandbox settings → **"When a message comes in"**, paste:

```
https://xxxx.ngrok.io/whatsapp        (Method: POST)
```

Click **Save**, then message your sandbox number from WhatsApp:

- `What time is it in UTC?`
- `What's the status of order A1001?`

The agent replies right in the WhatsApp chat.

## Signature validation

By default the server validates Twilio's `X-Twilio-Signature`. For the check to
pass, the reconstructed URL must match what Twilio calls. If you hit signature
errors behind a proxy, set the exact public URL:

```bash
export TWILIO_PUBLIC_URL="https://xxxx.ngrok.io/whatsapp"
```

To disable validation while experimenting:

```bash
export TWILIO_VALIDATE_SIGNATURE=false
```

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `OPENAI_API_KEY` | yes | Model provider API key |
| `OPENAI_BASE_URL` | no | Model provider base URL (for non-OpenAI providers) |
| `TWILIO_ACCOUNT_SID` | yes | Twilio Account SID (`AC...`) |
| `TWILIO_AUTH_TOKEN` | yes | Twilio Auth Token (secret) |
| `TWILIO_WHATSAPP_FROM` | yes | Sender number, e.g. `whatsapp:+14155238886` |
| `TWILIO_PUBLIC_URL` | no | Exact public webhook URL for signature validation |
| `TWILIO_VALIDATE_SIGNATURE` | no | `false` to skip signature validation |

## Going to production

- Replace the sandbox number with a real WhatsApp sender (Twilio → Senders).
- Outside the 24h customer-service window you must use pre-approved templates.
- Persist sessions (swap the in-memory session service for a durable one).
- Handle media messages and delivery status callbacks as needed.
