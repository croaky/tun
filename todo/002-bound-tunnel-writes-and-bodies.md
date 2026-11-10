# client, tund: bound tunnel writes, locks, and body sizes

Status: Planned.

Findings:

- `find_01HDsEd17sgf7Nt9A58mNzzU` (medium, CWE-667): a tunnel write
  under the global lock with no deadline.
- `find_01QzsfYDCQKLGjVuUTkWF7mE` (medium, CWE-770): an unbounded
  request body.

Outcome: Fix both in one change. Both findings touch the same lines in
`handleRequest` in `cmd/tund/main.go` (202-241) and the same lock
`s.mu`. A body cap and a write deadline are two halves of one bound on
the tunnel.

## Threat

- Attacker: an anonymous client on the internet. The attacker needs no
  token. A tunnel must be connected, which is the normal state.
- Boundary: `tund` on Render, and the `tun` client on the laptop.
  `tund` forwards every path. Only the client checks `TUN_ALLOW`.
- Consequence, write lock: if the tunnel peer stops reading but the
  TCP connection stays open (for example, a laptop in sleep), one
  large body blocks `conn.WriteMessage` while it holds `s.mu`. All
  forwarding stops. A new tunnel with the correct token cannot replace
  the old one. `/health` still answers `ok`, so Render does not
  restart the process. The attacker cannot cause the stall alone.
- Consequence, body size: `io.ReadAll(r.Body)` reads any size and
  waits with no time limit. A large or slow body can use all memory
  on a small Render instance. `tund` then forwards the body, and the
  client decodes it before `TUN_ALLOW` rejects it. That uses laptop
  memory and bandwidth.
- Both are availability only. No data leaks.

## Change

### `cmd/tund/main.go`

1. Add a write mutex to `server`, separate from `s.mu`. `s.mu` then
   guards only `s.conn`, `s.user`, and `s.pending`. The ping writer and
   `handleRequest` take the write mutex for the write. No goroutine
   holds `s.mu` during network I/O.
2. Call `conn.SetWriteDeadline(time.Now().Add(writeWait))` before each
   `WriteMessage`. On a write error, close the connection and clear
   `s.conn` if it is still the same connection. gorilla/websocket keeps
   the first write error, so the next writers fail at once.
3. Wrap `r.Body` in `http.MaxBytesReader` with a fixed limit. Return
   413 when the body is too large. Do this before the request goes on
   `s.pending` and before the write.
4. Replace `http.ListenAndServe` with an `http.Server` that sets
   `ReadHeaderTimeout` and `ReadTimeout`. gorilla/websocket clears
   these deadlines in `Upgrade`, so `/tunnel` keeps its own pong
   deadline.
5. Call `conn.SetReadLimit` on the tunnel connection.

### `cmd/tun/main.go`

1. Call `conn.SetReadLimit` on the tunnel connection.
2. Read the local response body through `io.LimitReader`. Return 502
   when the body is too large. Without this cap, one large response
   from the local service goes above the server read limit and drops
   the tunnel.

### `protocol.go`

Add one constant for the body limit and one for the message limit.
The message limit is the body limit times 4/3 (base64), plus room for
headers and the JSON frame.

Cost: about 30 lines and one mutex field. No new dependency and no new
goroutine. No speed cost. `s.mu` is held for less time than now, so
concurrent requests wait less. The CI cost is two tests that each run
in less than `writeWait`.

## Options

Body limit:

- A: a fixed 1 MiB limit (recommended). A Slack event is a few KB.
  No new configuration.
- B: a fixed 10 MiB limit. This permits larger webhooks. It permits
  10 times more memory for each request.
- C: a `TUN_MAX_BODY` variable. This adds one more setting for each
  administrator to know. Add it only when a user needs a different
  limit.

Write path:

- A: a write deadline and a separate write mutex (recommended). This
  meets both fix criteria in the finding.
- B: a write deadline only. This is 1 line. A stalled peer then
  freezes the server for `writeWait` (5 s) for each write. The lock
  still covers I/O, so the finding stays partly open.
- C: one writer goroutine for each connection, fed by a channel. This
  adds a goroutine and a queue. It gives no gain over A here.

## Feature to remove (proposal)

`tund` forwards every path (`* /*` in `README.md`) and leaves the
allowlist to the client. If `tund` also checked `TUN_ALLOW`, a request
outside it would get 403 at `tund`. That request would not cross the
tunnel or use laptop memory. The client could send its rules once in a
header at connect. Cost: a protocol change on both sides, about 20
lines, and a second place that reads the rules. The body cap above
already closes the memory risk, so this change does not need it.
Decide this as a separate change.

## Files

- `cmd/tund/main.go`
- `cmd/tun/main.go`
- `protocol.go`
- `cmd/tund/main_test.go`, `cmd/tun/main_test.go`, or
  `integration_test.go`
- `README.md`: state the body limit.

## How to test

1. A public request with a body above the limit gets 413. Nothing goes
   to the tunnel.
2. With a tunnel peer that stops reading, a public request with a large
   body returns 502 within `writeWait`.
3. While that write is blocked, other public requests get 502, 503, or
   504 in bounded time.
4. While that write is blocked, a new tunnel with the correct token
   becomes the active tunnel at once.
5. A tunnel message above the read limit closes the connection on each
   side. Neither side reads the full message into memory.
6. A local response above the limit gets 502, and the tunnel stays up.

Then run the checks in `Checkfile`.
