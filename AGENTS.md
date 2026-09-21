# Agents guide

tun tunnels a local service to the public internet, as a self-hosted
alternative to ngrok. `README.md` holds the tutorial and the local
development loop.

"Writing", "Checks", and "Commits" state how an agent works in this
repo. The other sections describe the project.

## Writing

Write every word in ASD-STE100 Simplified Technical English (STE):
Markdown docs, code comments, commit messages, and replies in an agent
conversation. One idea per sentence, active voice with the actor named,
one word for one meaning, and nothing that carries nothing.
`$HOME/blog/AGENTS.md` holds the full rule.

## Layout

- `cmd/tund/` is the server. It runs on Render, accepts one tunnel
  connection at a time, and forwards every other request through it.
- `cmd/tun/` is the client. It dials the server over a WebSocket and
  proxies to a local URL.
- `protocol.go` defines the frames the two sides exchange.
- `env.go` reads a `.env` file from the working directory.

## Configuration

The server reads `PORT` and `TUN_TOKEN`. The client reads
`TUN_SERVER`, `TUN_LOCAL`, `TUN_ALLOW`, and `TUN_TOKEN`. Both read a
`.env` file in the directory you run them from.

`TUN_ALLOW` is the allowlist, such as `POST /slack/events`. A request
outside it does not reach the local service. Keep that default narrow.

## Safety

`TUN_TOKEN` is a shared secret. Never commit it, never write it into a
doc, and never print it in a reply. Name the variable instead.

A tunnel exposes a local service to the internet. State the allowlist
before you widen anything, and prefer the narrowest rule that serves
the case.

## Checks

`bin/pre-push` runs them, and it runs the Go checks only when the
change touches a `.go` file:

```sh
goimports -local "$(go list -m)" -w .
go vet ./...
go test ./...
deadcode -test ./...
```

This repo has no CI. The hook is the gate, so install it:

```sh
git config core.hooksPath bin
```

## Commits

- Prefix with the area (`tun:`, `tund:`, `test:`, `doc:`, `go:`).
- Use imperative mood and lowercase except for proper nouns.
- Hard-wrap at 72 columns.
- State why, not only what. See `git log` for examples.
