# Example overlays

`ocbench experiment run` compares two or more **arms**, and an arm is your live
OpenCode configuration plus an *overlay*: a small config file, or a directory of
config, merged above your own settings for the duration of that arm. Nothing in
your real configuration is edited, and the overlay is only active for the
benchmark child process.

Each example here is a self-contained, valid OpenCode configuration fragment:

| Path | What it changes |
|---|---|
| `lean/lean.json` | The primary agent works alone — its `task` tool is off, so it cannot delegate. |
| `delegated/delegated.json` | The same primary agent *plus* a read-only `reviewer` subagent it may call. |

Comparing the two is the canonical "does the extra agentic architecture earn
its keep?" experiment: the delegated arm may score higher, but the cohort
standing also shows what that delegation cost in dollars and tokens.

## Try it

From the repository root, against one task and three repeats per arm:

```sh
ocbench experiment run core code-review \
  --profile lean=examples/overlays/lean/lean.json \
  --profile delegated=examples/overlays/delegated/delegated.json \
  --baseline lean --repeat 3
```

Then open the dashboard (`ocbench serve`) and open the cohort on the landing
page: `/` lists it, with cost and median tokens per solved task, gated on three
validated runs per task per arm.

## Writing your own overlay

- **A file** becomes `OPENCODE_CONFIG=<path>` and is merged above your global
  config. Only the keys you set change; everything else you configured is kept.
  Good for flipping a setting, adding one agent, or changing permissions.
- **A directory** becomes `OPENCODE_CONFIG_DIR=<path>` and is merged above
  `.opencode`. Good for shipping a bundle of agents/commands together.
- **An empty path** (`--profile lean=`) is the unmodified configuration — use it
  for the "before" arm when you only want to vary one side.

A good first comparison keeps everything fixed except the one thing you are
curious about, so the cohort attributes the difference to that change. Read
`docs/design.md` §12 for the overlay semantics and precedence order.
