# CLAUDE.md — working notes for `gits`

`gits` is a multi-repo git orchestration CLI. The authoritative requirements live in
[`docs/spec.md`](docs/spec.md); this file records how the code is organised and the traps that are
easy to fall into. **When the two disagree, the spec wins** — and the fix is to change the code, not
to reinterpret the spec.

## Toolchain

| | |
| --- | --- |
| Go | 1.26+ |
| Lint | `golangci-lint` v2, pinned in `Makefile` as `GOLANGCI_LINT_VERSION` |
| Deps | `github.com/spf13/cobra` (+ `pflag`), `gopkg.in/yaml.v3`. **Keep it at that.** |

```
make hooks              # one-time per clone: point git at .githooks
make test               # unit tests only — fast, this is what pre-commit runs
make test-integration   # -tags integration; spawns a real git binary
make lint               # the same gate pre-commit enforces
make build              # -> bin/gits
```

`.githooks/pre-commit` blocks a commit unless staged Go files are gofmt-clean, `golangci-lint run`
passes over the whole module, and `go test ./...` is green. Do not bypass it with `--no-verify`;
fix the cause.

## Architecture

Clean architecture, dependencies pointing inward only. **A package may only import from the layers
above it in this list:**

```
internal/domain/     entities + rules. Imports nothing but the stdlib. No git, no I/O, no YAML.
internal/app/        use cases, one per command. Depends on domain + the ports it declares.
internal/adapter/    implementations of the ports: git subprocesses, YAML manifest, renderers.
internal/cli/        cobra wiring, flag parsing, exit-code mapping.
cmd/gits/            main(): builds the adapters and hands them to the CLI.
```

The ports (`internal/app/ports.go`) are declared by the use cases, not by the adapters — the
consumer owns the interface. This is what lets every use case be tested against an in-memory fake
with no git binary in sight.

### Why the layering is worth respecting here

`gits` shells out to `git` for everything (a deliberate spec decision — §10 — so that the user's
credential helpers, GPG signing, hooks and `includeIf` behave exactly as they do outside the tool).
That makes the git adapter the one genuinely slow, environment-dependent, hard-to-test part of the
system. Keeping every decision — state priority, baseline selection, the write boundary, summary
tallies — in `domain` and `app` means the fiddly logic is tested in milliseconds and only the thin
subprocess layer needs a real repo.

## Testing

- **Unit tests** run against fakes and cover domain + app. They must stay fast: `go test ./...` sits
  in the commit gate.
- **Integration tests** carry `//go:build integration` and build real temp repos with a real git.
  They cover the adapter's parsing of `git status --porcelain=v2 --branch`, `rev-list
  --left-right`, and friends — where the real risk lives, since that output is the contract with an
  external program we do not control.

## Invariants that are easy to break

These each encode a specific failure the spec calls out. Changing one changes behaviour the spec
promises.

1. **Output must be deterministic.** Repo order follows the manifest, never completion order.
   No timestamps, no durations, no elapsed times in stdout. Diffing two runs is a supported
   workflow, and any noise breaks it (§6.5 rule 2). Parallel work is collected and only then
   printed in order.
2. **Never block on a non-TTY.** No prompt, ever, when stdin is not a terminal — fail with
   `E_NEEDS_YES` instead (§6.7). A hung process with no output is the worst outcome for an agent
   caller; it usually ends in a caller-side timeout with no clue why.
3. **Every git subprocess gets the hardened environment** in `internal/adapter/git/runner.go`
   (`GIT_TERMINAL_PROMPT=0`, empty `GIT_ASKPASS`/`SSH_ASKPASS`, `ssh -o BatchMode=yes`,
   `GIT_OPTIONAL_LOCKS=0`) and a timeout that kills the whole process tree. Git will happily sit
   waiting for a password forever otherwise (§6.8).
4. **`--json` puts exactly one object on stdout.** Progress, warnings, verbose git output and
   prompts all go to stderr, or `gits status --json | jq` breaks (§6.4).
5. **Untracked files do not make a repo `dirty`.** Only tracked modifications do. The spec's own
   example carries `"state": "behind"` next to `"dirty": {"tracked": 0, "untracked": 1}`.
6. **`no-write` is a boundary, not a permission.** It excludes a repo from every write command for
   *every* caller. There is deliberately no flag that relaxes it for humans only — agents would
   simply not pass it (§9).
7. **`deps` compares against the canonical repo's remote-tracking ref**, never its HEAD or current
   branch. The canonical checkout is routinely sitting on a feature branch, and using HEAD makes
   one workspace report different answers on two machines (§7.11).
8. **Never skip the two-way commit count in `deps`.** A one-way `rev-list --count` returns a
   plausible number for a commit that is not an ancestor at all, turning "ahead 3, behind 3" into
   an innocent-looking "behind 3" (§7.11).
9. **Submodule identity comes from the URL, normalised.** Never from the submodule's path — most
   dependents call the same submodule `proto`. Normalisation must ignore scheme, credentials, port
   and a trailing `.git`.
10. **The root repo syncs first, then the manifest is reloaded** (§7.1, §7.3). The manifest lives
    inside the root repo, so skipping this means a repo added on another machine never appears. If
    the root repo cannot fast-forward, warn loudly that the list may be stale — never continue
    silently.
11. **`gits` is the only writer of `gits.yaml`.** Writes go through the yaml.v3 Node API so comments
    and formatting survive, and new entries are inserted in `name` order — appending guarantees a
    merge conflict when two machines each add a repo (§10.1).
12. **One canonical layout, declared once.** The `layout` values in
    `internal/adapter/manifest/format.go` are the single description of how each manifest file is
    laid out — `manifestLayout` for `gits.yaml`, `localLayout` for `gits.local.yaml` — and
    `Create` (text), `AddRepo` (nodes) and `fmt` all have to produce it. yaml.v3 cannot record blank
    lines, so `spaceSections` re-imposes them on every write rather than trying to remember them.
    `TestFormat_IsNoOpOnWhatGitsWrites` is what keeps the three writers in step: if it fails, one of
    them drifted, and every `gits add` would leave the file wanting a `gits fmt`.
13. **All user-facing text is English**, regardless of locale, including JSON `message`/`hint`.
    Agents match on those strings. Only user-authored manifest fields (like `description`) may be in
    any language (§6.4).

## Error codes and exit codes

`domain.ErrCode` values are a public contract: they appear verbatim in JSON and in human output.
Never change what a code means — add a new one. `E_AUTH` and `E_NETWORK` must stay distinct:
retrying a network failure can work, retrying an auth failure only burns an agent's budget.

Exit codes (§6.10): `0` success · `1` at least one repo operation failed · `2` usage error ·
`3` nothing failed but something needs attention (only with `--exit-code`) · `130` interrupted.
`1` and `3` are deliberately separate; do not collapse them.
