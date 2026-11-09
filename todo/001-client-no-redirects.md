# client: do not follow redirects from the local service

Status: Planned. Do this first.

Findings: `find_01L5QHHRaqWDxbc8ebYwREQo` (high, CWE-918).

Outcome: Fix.

## Threat

- Attacker: an anonymous client on the internet. The attacker needs no
  token.
- Boundary: the `tun` client on the laptop. `TUN_ALLOW` checks only
  the first request. The `http.Client` at `cmd/tun/main.go:220` then
  follows each 3xx from the local service, for 10 hops, to any host.
- Consequence: the attacker reads local paths that `TUN_ALLOW` does
  not permit. If the app builds `Location` from request headers or
  body, the attacker also reads other ports and other hosts that the
  laptop can reach. A 307 or 308 sends the attacker's body again to
  the new target.

## Change

In `handleRequest` in `cmd/tun/main.go`, set `CheckRedirect` to return
`http.ErrUseLastResponse`. The client then sends the 3xx, its
`Location`, and its body back through the tunnel, as a reverse proxy
does. The public caller gets the redirect and must send a new request.
`TUN_ALLOW` then checks that new request.

Make one `http.Client` for each `client` value, not one for each
request. Keep `Timeout: requestTimeout`.

Cost: about 3 lines of code and one test. No new moving parts. There
is no speed cost. A caller that followed redirects through the tunnel
now gets the 3xx. For the Slack case that is correct, because Slack
does not follow redirects.

## Options not chosen

- Check each hop against `TUN_ALLOW` in `CheckRedirect`. This adds
  more code and still sends requests that the caller did not make.
- Remove the feature. The fix is the removal: the client stops
  following redirects.

## Files

- `cmd/tun/main.go`
- `cmd/tun/main_test.go` or `integration_test.go`

## How to test

Start a local service that answers an allowed route with 301, 302,
303, 307, and 308. Point `Location` at (a) a path on the same service
that `TUN_ALLOW` does not permit, (b) a different port, and (c) a
different host. For each case:

1. The second target gets no request.
2. The public caller gets the original status and `Location`.

Then run the checks in `Checkfile`.
