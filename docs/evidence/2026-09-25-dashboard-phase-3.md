# Evidence — dashboard phase 3 (Architecture)

Date: 2026-09-25. Commit `bbc758a` on `main`.

Records what the Architecture page was observed to do against the live store.
Screenshots live in `.playwright-mcp/` (ignored by git).

## What was built

| Piece | Commit |
|---|---|
| `/arch` index and `/arch/{hash}` architecture page | `bbc758a` |
| `internal/profile/captures.go` — reader for `agents.json`, `skills.json`, `instructions.json` | `bbc758a` |
| `profile.Agent` gained `Temperature` and `Options` | `bbc758a` |
| `web.WithPaths` so the handler can reach the capture files | `bbc758a` |

## The capture files are not the component JSON

The fingerprint reduces each agent to a component: `model` is the string
`"opencode-go/deepseek-v4.1-flash"` and the prompt survives only as
`prompt_sha256`. The capture file beside it keeps `model` as an object
(`{"providerID":…,"modelID":…}`), the full `prompt` text, and the whole
`permission` list.

The reader therefore parses the capture shape, not the component shape. Getting
this wrong would have produced an agent list with no models, silently. This was
found by inspecting the live captures rather than assuming the two agreed.

## Live smoke test

Store `~/.local/share/ocbench/ocbench.db`, profile
`ac83af1eea74426a37d8e8fc7d8018e3dbee604a9969a906c2485d2d6f85f545`, served on
`127.0.0.1:8787`.

`/arch` listed all 5 profiles with architecture summary, OpenCode version,
component count, run count and creation date. Before this commit `/profiles`
answered 404: the sidebar linked to a route that did not exist, because only
`/profiles/{hash}` was registered.

`/arch/{hash}` rendered seven sections — Changes, Subagent tree, Agents,
Instructions, Skills, MCP servers and plugins, Fingerprint — with:

- **Subagent tree**: the 7 real permission edges,
  `build → architect, deep-review, explore, general, review, verifier` and
  `plan → explore`, derived from the `permissions` component's `by_agent` rules
  and the set of configured subagents.
- **Agents**: 9 agents with mode, model, variant, steps and tool counts, plus a
  collapsed block per agent holding tools on/off, task rules, permission rules
  and the captured prompt (2315 characters for `build`).
- **Instructions**: the captured `global:AGENTS.md` text.
- **Comparison** (`?against=ec99cadb…`): 12 meaningful changes —
  `agent build — tools +lsp -remote_agent -remote_exec -remote_status`,
  `permissions — permission rules`, `skill brainstorming — removed`. This is
  roadmap item #18, "render component diffs as meaning rather than hashes",
  working on live data.

A clean load reports **zero console messages**: the strict CSP still holds with
`<details>`, `<pre>` and the tree markup.

Screenshots: `profiles-index.png`, `arch-compare.png`, `arch-tree.png`.

## Route correction

design.md §15.1 names this page Architecture at `/arch` and `/arch/{hash}`. The
implementation had inherited `/profiles/{hash}` from the earlier dashboard
commit. The routes now follow the spec, the sidebar label is "Architecture", and
the Overview leaderboard links were updated. `/profiles` deliberately 404s
rather than lingering as an alias; it was never published, and a test pins it.

## A number that looks like a bug and is not

Every agent on the live page shows `—` for temperature and no model options. That
is correct: for this profile both the components and the capture files carry no
`temperature` and an empty `options` object. An earlier query appeared to show
`temperature: 0.2` for `architect`, but it was not scoped to the profile under
test — the value belongs to a different profile's capture. The seeded test covers
the populated path, which live data does not exercise.

## Test coverage

`internal/profile/captures_test.go` — reads all three files, sorts agents, skills
and instructions, decodes the object model form, and pins two decisions: a
missing directory is *unavailable*, not an error, while a malformed file *is* an
error, because silently showing an empty prompt would misrepresent the profile.

`internal/web/server_test.go` — the architecture page renders agents, the tree,
the captured prompt and instruction text, model options, MCP and plugins; a
profile with no captures still renders and says so while showing the instruction
hash; `?against=` produces a comparison and its absence does not; the index lists
profiles and `/profiles` is gone.

## Gates

`go test ./... -count=1`, `go vet ./...`, `gofmt -l cmd internal web` (empty) and
`make cross` (linux amd64 + arm64, CGO_ENABLED=0) all pass at `bbc758a`.

## Not yet verified

- Skill bodies are deliberately not rendered: 39 skills carry ~326 KB of library
  content that is near-identical across profiles, so the page shows name,
  description, location and content hash. design.md §15.3 lists "skills" without
  asking for their text, while asking explicitly for agents' instruction text.
- The page has not been viewed below ~1100px wide.
- `/arch` is not suite-scoped, although §15.1 says the scope switcher applies to
  Architecture. The switcher is implemented on Overview only.
