# tun

Tunnel local services to the public internet.
Self-hosted ngrok alternative.

## Tutorial

The following example shows how to use `tun` and `tund` to expose a local web
service to the [Slack Events API](https://docs.slack.dev/apis/events-api).

Set up `tund` on a server:

1. Create a new "Web Service" on [Render](https://render.com/)
2. Set "Public Git Repository" to `https://github.com/croaky/tun`
3. Set build command: `go build -o tund ./cmd/tund`
4. Set start command: `./tund`
5. Set environment variable `TUN_TOKEN` to something secret
6. Set "Auto Deploy" to "Off"
7. Set "Health Check Path" to `/health`
8. Deploy

Server endpoints:

- `GET /health` - Health check
- `GET /tunnel` - WebSocket endpoint for tunnel client
- `* /*` - All other requests forwarded through tunnel

`tund` accepts one active tunnel connection at a time.
A new connection closes the previous one.
Server logs look like:

```
[croaky] tunnel connected
200 POST /slack/events 147.33ms
[croaky] tunnel disconnected
```

Configure the Slack app's "Event Subscriptions URL" to:
`https://your-service.onrender.com/slack/events`.
Render provides HTTPS automatically.

Install `tun` on a laptop:

```sh
go install github.com/croaky/tun/cmd/tun@latest
```

Create a `.env` file in the directory you run `tun`:

```
TUN_SERVER=wss://your-service.onrender.com/tunnel
TUN_LOCAL=http://localhost:3000
TUN_ALLOW="POST /slack/events GET /health"
TUN_TOKEN=your-shared-secret
```

`TUN_ALLOW` accepts space-separated `METHOD /path` pairs (exact match, no wildcards).
All requests not matching a rule return 403 Forbidden.

`TUN_TOKEN` is required on both client and server.
The client authenticates using `Authorization: Bearer <token>`.

Run:

```sh
tun
```

Client logs look like:

```
[croaky] connected to wss://your-service.onrender.com/tunnel, forwarding to http://localhost:3000
POST /slack/events
```

The client auto-reconnects with exponential backoff (500ms to 30s).
Requests timeout after 30 seconds.

## Developing tun

```sh
# setup
brew install go
git checkout -b user/feature

# terminal 1: start server
# Place .env in the directory you run `tund`:
#   PORT=8080
#   TUN_TOKEN=your-shared-secret
go run ./cmd/tund

# terminal 2: start client
# Place .env in the directory you run `tun`:
#   TUN_SERVER=ws://localhost:8080/tunnel
#   TUN_LOCAL=http://localhost:3000
#   TUN_ALLOW=POST /slack/events
#   TUN_TOKEN=your-shared-secret
go run ./cmd/tun

# terminal 3: exercise the tunnel
curl -X POST http://localhost:8080/slack/events -d '{"test": true}' -H "Content-Type: application/json"

# checks
goimports -local "$(go list -m)" -w .
go vet ./...
go test ./...
deadcode -test ./...

# commit
git add -A
git commit -m "tund: add new feature" # commit with prefix, imperative mood, hard-wrap 72 cols
```

## License

MIT
