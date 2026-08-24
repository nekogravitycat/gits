# gits

Keep a directory full of related git repos in step, with one command instead of eighteen.

`gits` is a small CLI for the workspace layout where a dozen or more repos sit side by side and
change together. It fetches, reports, commits, pushes and — the part no other tool does — tells you
which repos are pinned to an outdated version of another repo in the same workspace.

Every command speaks two languages equally well: an aligned, colourised report for a person at a
terminal, and a stable `--json` object for a script or an AI agent.

```sh
gits up          # pull everything, clone what's missing, then tell me where I stand
gits status      # what's dirty, what's behind, what isn't even here
```

## Install

```sh
go install github.com/nekogravitycat/gits/cmd/gits@latest
```

`@latest` resolves to the newest [tagged release](https://github.com/nekogravitycat/gits/releases).
No Go toolchain handy? Prebuilt binaries for Linux, macOS and Windows (amd64/arm64) are attached to
every release there too — download, unpack, put `gits` on `PATH`.

Or from a clone: `make build` (→ `bin/gits`).

Building needs Go 1.26+; running needs `git` on `PATH`. `gits` shells out to your real `git`, so your
credential helper, GPG signing, hooks and `includeIf` configuration behave exactly as usual. Linux,
macOS and Windows are equally supported.

## Update

```sh
go install github.com/nekogravitycat/gits/cmd/gits@latest
```

Re-running the same install command fetches the latest release and overwrites the existing binary.
`gits --version` confirms what you ended up with.

Or from a clone: pull, then rebuild.

```sh
git pull
make build   # -> bin/gits
```

## Quick start

Start in the directory that holds your repos — ideally one that is itself a git repo.

```sh
cd ~/Repositories
gits init
```

`init` scans one level down, writes `gits.yaml` describing what it found, and adds
`gits.local.yaml` to `.gitignore`. Open `gits.yaml`, add `groups:` labels, mark any repo you must
never write to with `no-write: true`, then commit it — that commit is what makes the list travel:

```sh
git add gits.yaml && git commit -m "add gits manifest"
git push
```

On your other machine, clone the workspace repo and run `gits up`. Everything else follows from the
manifest.

## A typical workflow

**Morning, or after switching machines** — one command gets you current:

```sh
gits up
```

It syncs the workspace root repo first, reloads `gits.yaml` from it (so a repo your other machine
added last night appears), clones what is missing, fast-forwards the rest, then reports status and
dependencies. Nothing here touches uncommitted work: a dirty, detached, diverged or upstream-less
repo is skipped and reported with the command that would unblock it.

**While working** — check where you stand at any time, without touching the network:

```sh
gits status              # everything
gits status -g game      # just the repos in the "game" group
```

**After a change that spans several repos** — commit and push, repo by repo or all at once:

```sh
gits commit                                  # interactive: walks each dirty repo, asks for a message
gits commit -r game-server -m "fix: timing"  # one repo, one message
gits push -n                                 # show the plan first
gits push                                    # push everything that is ahead
```

**When a shared dependency moved** — see who is pinned to an old commit, then bump them:

```sh
gits deps
gits foreach -g game -- git submodule update --remote proto
gits commit -m "chore: bump shared-proto"
gits push
```

**Adding a repo to the workspace** — register it, then let every machine pick it up:

```sh
gits add shared-proto --url https://git.example.com/shared-proto.git --group-tag platform
git add gits.yaml && git commit -m "add shared-proto" && git push
gits clone -r shared-proto
```

## Reading `gits status`

```
$ gits status
workspace: C:\Users\you\Repositories  (7 repos)

  ✓ workspace     main
  ↑ docs-site     main          ahead 1
  ↓ drawer-tool   main          behind 2
  ● game-server   main          uncommitted 1
  ⚠ shared-proto  feature/new-messages  branch has no upstream (E_NO_UPSTREAM), (default is main)
      -> cd shared-proto && git push -u origin feature/new-messages
  ✗ stack-tools   -             directory does not exist (E_MISSING_DIR)
      -> gits clone -r stack-tools
  ● vendor-sdk    main          uncommitted 1  [no-write]

summary: 7 repos - 2 clean - 1 dirty - 1 ahead - 1 behind - 1 missing - 1 need attention
deps: 2 repos pinned to an outdated dependency (see gits deps for details)
data may be stale (offline); add --fetch for live status
```

| Symbol | Plain | Meaning |
| --- | --- | --- |
| `✓` | `+` | clean |
| `●` | `*` | uncommitted changes to tracked files |
| `↑` | `^` | ahead of upstream |
| `↓` | `v` | behind upstream |
| `⚠` | `!` | needs a decision: diverged, detached, or no upstream |
| `✗` | `x` | missing directory, not a repo, or failed |
| `?` | `?` | not enough information |

Every repo is listed, healthy ones included, so you can see at a glance that nothing was skipped.
`status` does not touch the network by default and runs in milliseconds — ahead/behind come from your
local remote-tracking refs and are labelled as possibly stale. Pass `--fetch` for live numbers.

Colour and symbols degrade to plain ASCII when output is not a terminal, when `NO_COLOR` is set, or
with `--plain`. Being on a feature branch is marked (`default is main`) but is never an error —
`gits` never switches branches for you.

Two quirks worth knowing:

- **Untracked files never make a repo dirty.** Only modifications to tracked files do. Untracked
  files are still counted and shown, just not treated as a reason to act.
- **Dirty `no-write` repos are shown but not tallied**, so local experiments in a repo you never
  commit to do not become permanent noise in the summary.

## Commands

| | Command | Purpose |
| --- | --- | --- |
| **Daily** | `up` | Pull everything, clone what is missing, report. |
| | `status` | The state of every repo. |
| **Writing** | `commit` | Commit pending work, repo by repo or with one message. |
| | `push` | Push everything that is ahead, after showing the plan. |
| **List upkeep** | `init` | Create the manifest for this directory. |
| | `adopt` | Register repos already on disk, asking about each. |
| | `add` | Register one repo, non-interactively. |
| | `clone` | Materialise repos the manifest lists and this machine lacks. |
| | `fmt` | Put the manifest files back in their canonical layout. |
| **Queries** | `list` | What the manifest declares. Manifest only, no I/O. |
| | `deps` | Who is pinned to an outdated version of what. |
| **Precise** | `sync` | The fetch-and-fast-forward half of `up`. |
| **Escape hatch** | `foreach` | Run any command in every selected repo. |

`gits <command> --help` prints the full flag list for any of them.

A few behaviours that are easy to be surprised by:

- **`commit`** only commits tracked modifications by default; untracked files are listed as a
  reminder, not added (`-A` includes them). Interactively it handles one repo at a time and accepts
  `d` / `dd` for a diff, `e` for your editor, empty to skip, `q` to stop. It never pushes.
- **`push`** has no force push of any kind. `-u` will create a missing upstream.
- **`sync`** never merges, rebases or stashes — it only fast-forwards, then runs
  `git submodule update --init --recursive` (`--no-submodules` opts out).
- **`clone`** never overwrites: a directory that exists but is not a git repo is reported and left
  alone.
- **`list --names`** prints one name per line for shell loops; `--format=markdown` prints a table you
  can paste into a README or `CLAUDE.md`, so your repo table is generated instead of hand-written.
- **`foreach`** cannot tell `git log` from `git reset --hard`, so every run counts as a write and
  `no-write` repos are excluded unless you pass `--include-no-write`. It asks first, and caps each
  repo's output at 8KB.
- **`fmt`** sorts the entries by name, puts a blank line between them and orders each entry's
  fields the same way, keeping your comments with the entries they describe. It does the same for
  `gits.local.yaml` when you have one. For the shared list the sort is the point: name order is what
  lets two machines add repos independently and have git merge the result. It rewrites nothing that
  is already canonical, so it is safe in a pre-commit hook; `gits fmt --dry-run --exit-code` checks
  without writing.

## Choosing which repos to act on

Every command takes the same three filters, all repeatable:

| Flag | Effect |
| --- | --- |
| `-g, --group <name>` | only repos in this group (several groups are unioned) |
| `-r, --repo <name>` | only this repo |
| `--exclude <name>` | skip this repo |

With no filter, commands act on every repo in the manifest — minus `disabled` ones always, and minus
`no-write` ones for anything that writes.

## The manifest

`gits.yaml` sits in the workspace root and lists what belongs here. Keep it in version control —
that is the whole mechanism by which the list reaches your other machines.

```yaml
# gits workspace manifest.
version: 1

defaults:
  remote: origin
  branch: main

repos:
  # The workspace root is a repo too: it holds the shared docs and this file.
  - name: workspace
    path: "."
    url: https://git.example.com/workspace.git
    groups: [workspace]
    description: shared docs, CLAUDE.md and the manifest itself

  - name: shared-proto
    url: https://git.example.com/shared-proto.git
    groups: [platform, proto]

  - name: vendor-sdk
    url: https://git.example.com/vendor-sdk.git
    groups: [platform]
    no-write: true            # owned by another team: never commit or push here
```

| Field | Required | Default | Meaning |
| --- | --- | --- | --- |
| `version` | yes | — | Manifest format version. Currently `1`. |
| `defaults.remote` | no | `origin` | Remote name for every repo. |
| `defaults.branch` | no | `main` | Default branch for every repo. |
| `repos[].name` | yes | — | Unique identifier, and the directory name unless `path` says otherwise. |
| `repos[].url` | yes | — | Clone URL. |
| `repos[].path` | no | `name` | Workspace-relative path. `"."` marks the root repo. |
| `repos[].branch` | no | `defaults.branch` | The repo's *default* branch — used when cloning, as the reference point for "you are on another branch", and as the dependency baseline. Never used to switch branches. |
| `repos[].remote` | no | `defaults.remote` | Remote name. |
| `repos[].groups` | no | `[]` | Labels for `-g` filtering. A repo may belong to several. |
| `repos[].no-write` | no | `false` | Exclude from every write command. |
| `repos[].description` | no | `""` | One line, shown in listings. Your text, in any language. |

**`gits` is the only writer of `gits.yaml`.** Use `gits add` rather than editing the file from a
script: writes go through a node-level YAML API so your comments survive, and entries are inserted in
`name` order because two machines each *appending* a repo produce a guaranteed merge conflict.

### `no-write` is a boundary, not a permission

A `no-write` repo takes part in every read-only command — `status`, `sync`, `deps`, `list` — and is
excluded from every command that writes: `commit`, `push`, `foreach`. Use it for repos you pull to
build against but never contribute to.

There is deliberately no flag that relaxes this "just for humans": `gits` cannot tell who is calling
it, so such a flag would only be an option an agent declines to pass. For real differentiated access,
use server-side permissions.

### Machine-local exceptions: `gits.local.yaml`

Two machines are never identical. `gits.local.yaml` records this machine's exceptions and is
gitignored (`gits init` handles that):

```yaml
version: 1
overrides:
  - name: legacy-synth
    disabled: true            # not on this machine on purpose; never report it as missing
  - name: stack-tools
    path: vendor/stack-tools  # elsewhere on this machine; must stay inside the workspace
  - name: drawer-tool
    no-write: true            # may only tighten the boundary, never loosen it
```

You cannot add new repos here — new repos go in the shared list, or the drift problem comes back from
the other direction.

## Cross-repo dependencies

`gits deps` answers "who in this workspace is pinned to an old version of what". There is nothing to
declare: the information is already in `.gitmodules` plus the gitlink SHA in each repo's `HEAD`.

```
$ gits deps
shared-proto  (canonical: ./shared-proto, baseline origin/main @ 29e873f)

  ↓ drawer-tool  ab49581  behind 3
  ↓ game-server  ab49581  behind 3

2 repos depend on shared-proto, all pinned to the same commit

deps reports drift, not breakage; check what the missing commits changed
```

- Dependencies are matched by **normalised URL, never by submodule path** — in a real workspace most
  dependents call the same submodule `proto`, so the path identifies nothing.
- The baseline is the canonical repo's **remote-tracking ref, never its `HEAD`**, so one workspace
  cannot give different answers on two machines.
- Each dependent is judged against the branch it declared (`submodule.<name>.branch`, then the
  dependency's manifest `branch`, then `defaults.branch`), so a repo deliberately tracking a feature
  branch does not carry a permanent warning.
- If the dependency is not itself part of the workspace, `gits` marks the group `E_NO_CANONICAL` and
  names the repo to add — a partial verdict is never presented as a whole one.

`deps` reports facts and never claims something is broken. Whether three missing commits matter is a
question about those three commits.

## For scripts and AI agents

**Every command supports `--json`,** including the ones that write. Exactly one JSON object goes to
stdout; progress, warnings, prompts and verbose git output all go to stderr, so
`gits status --json | jq` is reliable. Failures emit JSON too — you never need a second parser for
the error path.

```jsonc
{
  "schemaVersion": 1,
  "command": "status",
  "workspace": "C:/Users/you/Repositories",
  "network": false,
  "stale": true,
  "repos": [
    {
      "name": "drawer-tool", "path": "drawer-tool", "groups": ["game"],
      "state": "behind", "exists": true,
      "branch": "main", "defaultBranch": "main", "onDefaultBranch": true,
      "upstream": "origin/main", "ahead": 0, "behind": 2,
      "dirty": { "tracked": 0, "untracked": 0 }, "submodulesClean": true
    },
    {
      "name": "stack-tools", "path": "stack-tools", "groups": ["platform"],
      "state": "missing", "exists": false, "defaultBranch": "main",
      "code": "E_MISSING_DIR", "message": "directory does not exist",
      "hint": "gits clone -r stack-tools"
    }
  ],
  "summary": { "total": 7, "clean": 2, "dirty": 1, "ahead": 1,
               "behind": 1, "missing": 1, "attention": 1, "failed": 0, "skipped": 0 },
  "deps": { "outdated": 2, "diverged": 0 }
}
```

Guarantees worth relying on:

- **One `state` per repo**, so you branch on a single value: `clean` · `dirty` · `ahead` · `behind` ·
  `diverged` · `detached` · `no-upstream` · `missing` · `not-a-repo` · `error`. A repo can be both
  dirty and behind; `state` reports whichever needs a human first, in the fixed priority `error` >
  `not-a-repo` > `missing` > `detached` > `no-upstream` > `diverged` > `dirty` > `behind` > `ahead` >
  `clean`. The raw `ahead`, `behind` and `dirty` fields are always present too.
- **Deterministic output.** Fixed field order, repo order following the manifest, no timestamps or
  durations anywhere on stdout. Diffing two runs is a supported workflow.
- **It never blocks.** When stdin is not a terminal and a command needs confirmation, `gits` exits
  `2` with `E_NEEDS_YES` immediately. Every git subprocess runs with terminal prompting disabled,
  `ssh -o BatchMode=yes`, and a timeout that kills the whole process tree.
- **All output text is English**, regardless of locale, so string matching does not break on someone
  else's machine. Only your own manifest fields, like `description`, are yours to write.

Retry safety: `status` / `deps` / `list` are read-only; `sync` / `up` / `clone` / `push` are
idempotent; `commit` never creates an empty commit; `add` / `adopt` are no-ops when the entry exists;
`init` refuses to overwrite; `fmt` writes nothing that is already canonical; `foreach` is as safe as
the command you gave it.

### Error codes

Failures and skips carry a stable code — `code` in JSON, in parentheses in human output. What a code
means never changes. Most failures also carry a `hint`: one command you can run next.

| Code | Meaning | Retry? |
| --- | --- | --- |
| `E_DIRTY` | uncommitted changes; the operation was skipped | no |
| `E_DIVERGED` | ahead and behind at the same time | no |
| `E_DETACHED` | detached HEAD | no |
| `E_NO_UPSTREAM` | the branch has no upstream | no |
| `E_MISSING_DIR` | in the manifest, not on disk | no — `gits clone` first |
| `E_NOT_A_REPO` | the directory exists but is not a git repo | no |
| `E_NO_WRITE` | excluded from write commands by `no-write` | no |
| `E_AUTH` | authentication failed | **no — a hundred retries will not help** |
| `E_NETWORK` | DNS, connection or remote 5xx failure | **yes** |
| `E_TIMEOUT` | a git subprocess exceeded `--timeout` | yes, or raise the timeout |
| `E_HOOK_FAILED` | a git hook exited non-zero | no |
| `E_NO_CANONICAL` | `deps` found no canonical checkout; the verdict is incomplete | no |
| `E_MANIFEST` | the manifest is malformed | no |
| `E_NO_WORKSPACE` | no `gits.yaml` found | no |
| `E_NEEDS_YES` | confirmation needed in a non-interactive environment | no |
| `E_MAX_REPOS` | the plan exceeds `--max-repos` | no |

### Exit codes

| Code | Meaning |
| --- | --- |
| `0` | everything succeeded |
| `1` | at least one repo operation failed |
| `2` | usage error: no manifest, bad manifest, bad flags, or confirmation needed without `--yes` |
| `3` | nothing failed, but something needs attention — only with `--exit-code` |
| `130` | interrupted |

`1` and `3` are separate on purpose. Reporting commands return `0` even when they find problems,
unless you ask for `--exit-code`.

### A worked agent sequence

```sh
gits list --json                       # what exists here, where, and what is read-only
gits status --json                     # what is dirty, behind, or absent
# ... make changes ...
gits deps --json                       # which repos pin an outdated dependency
gits foreach -r drawer-tool -r game-server -y --json -- git submodule update --remote proto
gits status --json                     # confirm only the expected repos changed
gits commit -r game-server -m "chore: bump shared-proto" -y --json
gits push -y --max-repos 5 --json
```

`--max-repos` is worth making a habit in automation: the failure mode that hurts is usually "the
scope blew up", not "one repo was wrong".

## Global flags and environment

| Flag | Effect |
| --- | --- |
| `-w, --workspace <path>` | use this workspace instead of searching for one |
| `-g, --group` / `-r, --repo` / `--exclude` | filters, see above |
| `-y, --yes` | skip confirmation prompts |
| `-n, --dry-run` | print the plan, change nothing (every write command supports it) |
| `--json` | machine-readable output; implies `--plain` |
| `-v, --verbose` | show the git commands being run, on stderr |
| `--plain` | plain ASCII, no colour |
| `--exit-code` | exit `3` when something needs attention |
| `--max-repos <n>` | refuse to act on more than `n` repos |
| `--timeout <duration>` | per-git-subprocess timeout, default `120s` |
| `-j, --jobs <n>` | parallelism, default `min(8, CPUs)` |
| `--version`, `-h, --help` | the usual |

Read-only commands and `sync` / `clone` / `foreach` run in parallel, but output is always printed in
manifest order — never in completion order.

The workspace is located by `-w`, then `GITS_WORKSPACE`, then by searching upward from the current
directory for `gits.yaml`, the same way git finds `.git`.

| Variable | Effect |
| --- | --- |
| `GITS_WORKSPACE` | workspace directory |
| `GITS_YES` | `1` is equivalent to `-y`; convenient for a CI or agent environment |
| `NO_COLOR` | disable colour |

## What gits deliberately does not do

- **It does not replace git.** No wrappers for branch, merge, rebase, stash or tag.
- **It never switches branches for you.** Working on a feature branch is normal, not a mistake.
- **It does not manage submodule lifecycles.** It reads `.gitmodules` and gitlinks for dependency
  information; the only writes are the `submodule update` after a clone or fast-forward.
- **No force push, no automatic conflict resolution, no `--no-verify`.** Hooks always run; a failing
  hook is reported as `E_HOOK_FAILED`.
- **It does not build, test or deploy**, does not talk to GitHub or GitLab APIs, and is not a
  monorepo migration tool.

---

The full requirements specification lives in [`docs/spec.md`](docs/spec.md) (written in Chinese) and
is the authority on behaviour. [`CLAUDE.md`](CLAUDE.md) covers the code layout and development
workflow.
