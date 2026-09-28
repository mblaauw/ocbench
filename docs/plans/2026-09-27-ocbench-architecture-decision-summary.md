# Architecture Decision Summary Implementation Plan

## Goal

Turn Architecture from a raw configuration dump into a cohort-aware decision
surface for comparing OpenCode workflow architectures, while preserving the
full captured configuration as expandable evidence.

## Decisions taken

- The prototype supplies the summary hierarchy, not synthetic metrics or
  pixel-perfect behaviour. Live data keeps honest labels: `messages / run`, not
  unavailable `calls / run`.
- A controlled cohort is selected by `?cohort=<experiment-id>`. Observed
  metrics and agent usage are calculated only from that experiment's runs for
  the displayed profile. Without a cohort, the page is configuration evidence
  only and states that it is not a comparable efficiency result.
- The primary permission list is compact in the summary: action counts and
  non-allow exceptions. The full captured rules remain in the lower agent
  detail section.
- Diff annotations belong on the primary, subagent, skill and MCP cards as well
  as in the component-changes table. A diff table remains the complete audit
  record.
- Profile-switch chips are ordinary server-rendered links. No SPA state is
  needed.

## Constraints

- No raw events, prompts, sessions or artifacts may be rendered.
- Cohort metrics must not rank or compare wall-clock duration across runners.
- Tests precede each behaviour change; all repository gates pass before commit.
- Update README/design/decisions/roadmap with decisions that ship.

## Tasks

1. Add an experiment-scoped profile read model so score, pass rate, cost and
   median tokens are derived from the selected cohort's runs only.
2. Add Architecture cohort selection and profile-switch chips; preserve the
   selected cohort across links and comparisons.
3. Compact the primary card's permissions and make raw rules details-only.
4. Derive per-card diff annotations from existing component changes for primary
   agent, subagents, skills and MCP servers.
5. Replace the top strip's broad profile score framing with observed cohort
   evidence: cost, tokens, validated pass rate, messages per run and subagent
   token share.
6. Verify responsive, no-JavaScript navigation and truthful empty states.
