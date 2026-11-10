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
- `scripts/tag` publishes a release tag. "Releases" tells how.

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

## Releases

sockeye is origin and holds no tags. `scripts/tag vX.Y.Z` publishes one
annotated tag to the GitHub mirror, which is what `go install`
resolves. Run it from the main checkout after the merge, since it tags
the tip of `main` and refuses any other commit.

croaky/laptop pins a tag in `laptop.sh`, and each run of `laptop.sh`
installs that build of `tun` over the one in `~/go/bin`. A stale pin
puts back an older build.

After a merge that changes `cmd/tun` or the code it imports, publish a
tag, then bump the `go install github.com/croaky/tun/cmd/tun@...` line
of `laptop.sh` to it in a laptop change. Raise the minor version for a
new setting or behavior, and the patch version for a fix. A merge that
changes only docs, checks, or `cmd/tund` needs no tag. Render builds
`tund` from source, not from a tag.

## Commits

- Subject: `scope: imperative summary`, at most 72 characters, lowercase
  except proper nouns, no trailing period.
- The scope is what the change acts on: `client:` for `cmd/tun`, `tund:`
  for `cmd/tund`, `protocol:`, `env:`, `test:`. Not `tun:`, because
  every commit here is tun.
- Each repo also uses these scopes: `security:` for a fix to a security
  finding, `deps:` for a dependency or the Go version, `doc:` for the
  docs, `todo:` for a plan, and `ci:` for the Checkfile and its checks.
- A change that acts on more than one area takes the scope whose
  behavior changed. Write two scopes, with a comma between them, only
  when each one changed: `client, tund:`.
- The `merge:` line in the root `Checkfile` checks the change title at
  merge. It caps the text after the scope at 64 characters.
- Include _why_, not just _what_. Hard-wrap the body at 72.
- Write the subject for a teammate who has not read the code. Name the
  command, the flag, or the output, not a term the change makes up.
- Write the body as a change description. sockeye squashes a change into
  one commit whose message is the change's title and description. The
  first push of a branch with one commit copies the commit message to
  the change.
- Sign your work with a `Co-Authored-By` trailer at the end of the
  message, after a blank line. A trailer on the line after the subject
  becomes part of the subject.

## Trailers

End the change description with one block of trailers, with no blank
line between them. Keep each trailer on one line, even past 72
columns.

- `Requested-by: name, channel, thread or subject, date` records who
  asked for the work, when somebody other than the author asked.
- `Reported-by: Claude Security at claude.ai/security` credits a
  finding from the Claude Security scanner.
- `Fixes:` names each Claude Security finding the change fixes
  (`Fixes: find_01... and find_02...`), or each production error group
  (`Fixes: ERR-1F`). Do not write `Fixes: CWE-...`. A CWE names a
  category, not a finding.
- Do not type `Co-Authored-By`, `Reviewed-by`, or `Signed-off-by` in
  the description. The merge derives them from the commits and the
  comments.

The squash merge writes the description to `main`, not the commit
messages. The first push of a branch with one commit copies its
trailers to the description, except the ones the merge derives. A
later `soc edit` writes only what it reads, so keep the trailers in
it:

    git --no-pager log -1 --format=%B | sed '/^Co-Authored-By:/d' | soc edit

Before the merge, read the description in `soc show` and confirm that
it ends with the trailers.
