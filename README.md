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
5. Set environment variable `TUN_TOKEN` to the output of `openssl rand -hex 32`
6. Set "Auto-Deploy" to "On Commit"
7. Set "Health Check Path" to `/health`
8. Deploy

Server endpoints:

- `GET /health` - Health check; the body names the build, as in `ok 1215dd91b61b`
- `GET /tunnel` - WebSocket endpoint for tunnel client
- `* /*` - All other requests forwarded through tunnel

`tund` accepts one active tunnel connection at a time.
A new connection closes the previous one.
Server logs look like:

```
tund 1215dd91b61b listening on :10000
[croaky] tunnel connected, tun "v0.1.2-0.20261119133951-1215dd91b61b"
200 POST /slack/events 147.33ms
[croaky] tunnel disconnected
```

Render deploys `tund` on each merge to `main`.
A deploy closes the tunnel, and the client connects again.
To see that a fix is live, compare the commit in `/health`
and in the `tunnel connected` line with the commit of the fix.
When a change needs a new environment variable on Render,
set the variable before you merge the change.

Configure the Slack app's "Event Subscriptions URL" to:
`https://your-service.onrender.com/slack/events`.
Render provides HTTPS automatically.

Install `tun` on a laptop:

```sh
go install github.com/croaky/tun/cmd/tun@main
```

croaky/laptop installs it from `main` in `laptop.sh`.

Create a `.env` file in the directory you run `tun`:

```
TUN_SERVER=wss://your-service.onrender.com/tunnel
TUN_LOCAL=http://localhost:3000
TUN_ALLOW="POST /slack/events GET /health"
TUN_HEADERS="X-Slack-Signature X-Slack-Request-Timestamp"
TUN_TOKEN=your-shared-secret
```

`TUN_ALLOW` accepts space-separated `METHOD /path` pairs (exact match, no wildcards).
All requests not matching a rule return 403 Forbidden.
`tun` does not follow redirects from the local service.
It returns each 3xx response to the caller, so `TUN_ALLOW` checks the next request.

`tun` forwards only these request headers to the local service:
`Accept`, `Accept-Encoding`, `Accept-Language`, `Content-Encoding`,
`Content-Type`, `User-Agent`, and the names in `TUN_HEADERS`.
It drops each other header, such as `Cookie`, `Authorization`, and `X-Forwarded-For`.
Add a header to `TUN_HEADERS` only when the local service needs it.
The Slack example needs the two headers that sign each event.
`tun` exits at start when `TUN_HEADERS` names a header
that some frameworks read to change the method or the path:
`X-HTTP-Method-Override`, `X-HTTP-Method`, `X-Method-Override`,
`X-Original-URL`, or `X-Rewrite-URL`.
`tun` cannot see a method override in the request body,
such as the `_method` form field in Rails and Laravel.
If the local service reads that field, turn it off for the allowed paths.

`TUN_TOKEN` is required on both client and server.
The client authenticates using `Authorization: Bearer <token>`.
`tund` exits at start when `TUN_TOKEN` has fewer than 32 characters,
and it logs each failed authentication without the token that the client sent.

Run:

```sh
tun
```

Client logs look like:

```
[croaky] tun v0.1.2-0.20261119133951-1215dd91b61b connected to wss://your-service.onrender.com/tunnel, forwarding to http://localhost:3000
POST /slack/events
```

The client auto-reconnects with exponential backoff (500ms to 30s).
Requests timeout after 30 seconds.
The tunnel carries bodies of up to 1 MiB.
`tund` returns 413 for a larger request body,
and `tun` returns 502 for a larger response body.

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
#   TUN_HEADERS=X-Slack-Signature X-Slack-Request-Timestamp
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
