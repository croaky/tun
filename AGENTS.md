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

The root `Checkfile` lists the checks CI runs on every push. Run the
same things before a commit:

```sh
go tool goimports -local "$(go list -m)" -w .
go vet ./...
go test ./...
go tool deadcode -test ./...
git ls-files -z '*.go' | xargs -0 gopls check -severity=hint
```

Pin every tool a check runs. Declare it with
`go get -tool <pkg>@<version>`, so `go.mod` and `go.sum` hold its
version and checksum, and run it as `go tool <name>`. Do not run
`go run pkg@latest` or `go install pkg@latest` in a `Checkfile`, a
script, or CI. A moving version makes the same commit pass one week
and fail the next, and it runs new tool code nobody reviewed. A pinned
tool changes in a change, where the diff shows the update and CI runs
it.

Keep the `tool` lines in `go.mod`, because no module requires tun.
A `go get -tool` can raise a requirement, and the build then links the
newer version. Keep the raise only when the tests pass and nothing
holds the old version on purpose. Otherwise, and in a library, put the
tools in a `tools.mod` and run them as
`go tool -modfile=tools.mod <name>`. The hml and uni repos show that
form.

Pin the tool, not its data. The `weekly_govulncheck` check reads the
live vulnerability database on every run, so it still finds advisories
published after the commit. Never vendor or cache that data.

## Commits

- Prefix with the area (`tun:`, `tund:`, `test:`, `doc:`, `go:`).
- Use imperative mood and lowercase except for proper nouns.
- Hard-wrap at 72 columns.
- State why, not only what. See `git log` for examples.
