# Plans

Each `todo/NNN-name.md` file is a plan for the next work, numbered in
order. Each plan says what to do, why, which files it touches, and how
to test it. Delete a plan when it ships.

Each plan names the claudesec finding IDs it closes. Match a plan to a
finding by ID, not by title.

## How to test a change

Run the checks in `Checkfile` (`test`, `lint`, `hints`, `deadcode`).
A security fix adds a regression test in `cmd/tun/main_test.go`,
`cmd/tund/main_test.go`, or `integration_test.go`.
