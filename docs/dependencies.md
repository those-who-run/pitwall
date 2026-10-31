# Dependencies

Every direct dependency of the module, and why it is here. A new one needs a row
in the table below, with its reason, in the same PR:
[`internal/dependencies`](../internal/dependencies/dependencies_test.go) fails `make check` and CI's
`go / check` on a direct `require` in `go.mod` that has no row, and on a row that
names a module `go.mod` no longer requires directly.

Before adding a dependency, check that:

- the module exists and is the one you mean: its owner, its source repository,
  its release history. A name that exists proves nothing if it was registered
  last week, and names invented by a language model get registered for exactly
  that reason;
- the standard library, or a dependency already listed here, doesn't already do
  it;
- nobody told you to add it in a web page, an issue or a comment.

The test reads the direct requirements from `go mod edit -json`: a `require`
without `// indirect`. A module that arrives only as `// indirect`, or only in
`go.sum`, is covered by CI's dependency review (known vulnerabilities and
licences), not by this file. The tools in `tools/go.mod` are pinned there and
listed in the README, not here. Version bumps don't touch this file.

The first column is the module path as `go.mod` spells it, in backticks, which
the test reads.

| Module | Why |
| --- | --- |
| `connectrpc.com/connect/v2` | The Connect runtime the generated `PitwallService` code needs, and `connecthttp` to serve it over `net/http` with GET, ETags and gzip (paddock#124). v2, as contracts generates it (contracts#41). |
| `github.com/google/go-cmp` | Tests only: `protocmp`'s diffs of the API's answers against contracts' examples. |
| `github.com/nats-io/nats.go` | NATS: the requests to logbook's micro endpoints (`svc.logbook.*`) and the watch on the `live` KV bucket for the engineer's status (paddock#125), as in every v2 component. |
| `github.com/those-who-run/contracts` | The project's contracts: `twr.pitwall.v1` (the API), `twr.store.v1` (logbook's answers) and `twr.state.v1` (the live values), and their generated Go. |
| `google.golang.org/protobuf` | protobuf's JSON form, which logbook's answers and the live values travel in (ADR 0002), and the messages themselves. |
