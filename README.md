# tun

Tunnel local services to the public internet.

## Tutorial

The following example shows how to use `tun`
with the [Slack Events API](https://docs.slack.dev/apis/events-api).

Run `tund` on a server:

1. Create a new "Web Service" on [Render](https://render.com/)
2. Set "Public Git Repository" to `https://github.com/croaky/tun`
3. Set build command: `go build -o tund ./cmd/tund`
4. Set start command: `./tund`
5. Set environment variable `PORT` to `8080`
6. Deploy

Endpoints:

- `GET /health` - Health check for Render
- `GET /tunnel` - WebSocket endpoint for tunnel client
- `* /*` - All other requests forwarded through tunnel

Render provides HTTPS automatically.

Configure the Slack app's "Event Subscriptions URL" to:
`https://your-service.onrender.com/slack/events`

Install `tun` on a laptop:

```sh
go install github.com/croaky/tun/cmd/tun@latest
```

Create a `.env` file in the directory you run `tun`:

```
TUN_SERVER=wss://your-service.onrender.com/tunnel
TUN_LOCAL=http://localhost:3000
TUN_ALLOW=POST /slack/events GET /health
TUN_TOKEN=your-shared-secret
```

`TUN_ALLOW` accepts space-separated `METHOD /path` pairs.
All requests not matching a rule return 403 Forbidden.

**Security:** `TUN_TOKEN` is required on both client and server.
Without it, anyone could connect to your tunnel server.
The client authenticates using `Authorization: Bearer <token>`.

Then run:

```sh
tun
```

`tund` accepts one active tunnel; a new connection closes the previous one.

## Developing tun

```sh
# setup
brew install go
git checkout -b user/feature

# terminal 1: start server
# Required: set PORT and TUN_TOKEN in ./.env for local dev
# ./.env (ignored by git):
#   PORT=8080
#   TUN_TOKEN=your-shared-secret
# Run tund from the directory that contains .env so it is picked up.
go run ./cmd/tund

# terminal 2: start client (ENV-only)
# Place .env in the directory you run `tun`:
#   TUN_SERVER=ws://localhost:8080/tunnel
#   TUN_LOCAL=http://localhost:3000
#   TUN_ALLOW=POST /slack/events
#   TUN_TOKEN=your-shared-secret

go run ./cmd/tun

# terminal 3: exercise the tunnel
curl -X POST http://localhost:8080/slack/events -d '{"test": true}' -H "Content-Type: application/json"

# build & test
goimports -local "$(go list -m)" -w .
go test ./...
go vet ./...

# open pull request
git add -A
git commit -m "tund: add new feature" # commit with prefix, imperative mood, hard-wrap 72 cols
```

## License

MIT
