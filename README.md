# Pitwall

The pit wall for Those Who Run v2: the web app and its API, which reads
sessions and laps through `logbook`.
The API is a Connect service defined in `contracts`, and the web app is
served from the same origin.

Issues are off in this repository. Every issue, the roadmap and the decisions
(ADRs) live in [those-who-run/paddock](https://github.com/those-who-run/paddock). A pull request here closes
its paddock issue with `Closes those-who-run/paddock#N.`

## Checks

`make check` runs what CI runs; [`AGENTS.md`](AGENTS.md) says what that is and
what a new clone runs first.
