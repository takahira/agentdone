# Contributing

`agentdone` is a small, focused tool — a low-noise Claude Code → Slack notifier.
It is maintained as a personal/reference project, so issues and pull requests are
welcome but may be triaged when time allows.

For anything beyond a small fix, please open an issue first so we can agree it's
in scope before you write code — it keeps the tool focused and saves you effort.

## Development

The Go module lives at the repository root (no subdirectory). Requires Go 1.24+.

```sh
go build ./...
go vet ./...
go test -race ./...   # CI runs -race; internal/state has a goroutine race smoke test
go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...   # lint (CI runs this too; pinned to the go 1.24 floor)
```

`sh demo/demo.sh` prints what a notification would look like, with no Slack or
network needed (it sets `AGENTDONE_STDOUT=1`).

## Layout

- `cmd/agentdone` — entry point; runs as a hook, or `init` / `uninstall` / `doctor` / `version`
- `internal/handler` — one file per hook event (`stop`, `notification`, …)
- `internal/{state,transcript,slack,config,suppress}` — supporting packages
- `pkg/cchooks` — standalone Go types for Claude Code hook payloads (stdlib-only)

## Pull requests

- Keep `go build` / `go vet` / `go test -race` / `staticcheck` green.
- Match the surrounding style, and add a test for any behavior change.
- **Changing what counts as a confirmation question** (`looksLikeQuestion` in
  `internal/handler/question.go` and the vocab lists in `internal/handler/lang.go`)?
  Read and update
  [`docs/question-detection.md`](docs/question-detection.md) — the behaviour
  contract and decision log — together with `TestQuestionContract`. It records
  the deliberate trade-offs so a fix doesn't silently reverse an earlier one.
- Notifications default to English; `AGENTDONE_LANG=ja` switches to Japanese
  (otherwise the POSIX locale `LC_ALL` > `LC_MESSAGES` > `LANG` decides, so
  `LANG=ja*` applies only when the first two are unset). Adding a language is one
  more entry in the catalog in `internal/handler/lang.go` (and its question
  phrases for `looksLikeQuestion`).
