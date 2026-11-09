# Plans

Each `todo/NNN-name.md` file is a plan for the next work, numbered in
order. Each plan says what to do, why, which files it touches, and how
to test it. Delete a plan when it ships.

Each plan names the claudesec finding IDs it closes. Match a plan to a
finding by ID, not by title.

## Threat model

The plans use this model:

- The attacker is an anonymous client on the internet. The attacker
  can reach every path on `tund` and has no `TUN_TOKEN`.
- The boundaries are `tund` on Render, the tunnel WebSocket, and the
  `tun` client on the laptop of a developer.
- The assets are the local service, the network that the laptop can
  reach, and the availability of the tunnel.
- `TUN_ALLOW` is the control that stops a request at the laptop.

## How to test a change

Run the checks in `Checkfile` (`test`, `lint`, `hints`, `deadcode`).
A security fix adds a regression test in `cmd/tun/main_test.go`,
`cmd/tund/main_test.go`, or `integration_test.go`.
