---
name: step-runner
description: >-
  Automates the milestone lifecycle for the Phi Agent Code harness. Use this skill
  whenever the user asks to start a step, advance to the next milestone, finish or merge
  a completed step, or inspect the roadmap status.
---

# Step Runner: Milestone Workflow Manager

This skill coordinates the 27 evolutionary milestones of `phi-agent-code` by wrapping the deterministic repository manager [`scripts/step.ps1`](scripts/step.ps1).

## Capabilities

1. **Check Status**: Inspect roadmap progress and identify the active or next milestone.
2. **Start Milestone**: Run pre-flight health checks, enforce sequential integrity (Step `N-1` must be merged), and checkout an isolated milestone branch (`step/XX-*`).
3. **Finish Milestone**: Run health checks, merge the completed milestone into the base branch (`master`), and preserve the milestone branch snapshot.

---

## Standard Procedures

### 1. Checking Roadmap Progress

When the user asks *"What step are we on?"*, *"Show progress"*, or *"Status"*:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\step.ps1 status
```

To inspect or focus on a specific step (e.g., Step 2):

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\step.ps1 step 2 status
```

### 2. Starting a Step or Advancing to Next

When the user asks *"Start step N"*, *"Next step"*, or *"Let's do Step 2"*:

1. If the step number isn't specified, run `status` first to identify the next pending milestone.
2. Run the start command:
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\step.ps1 start <N>
```
3. Read the corresponding guide at `docs/steps/<N-padded>-<slug>.md` (e.g. `docs/steps/02-permission-gate.md`) to guide implementation.

### 3. Completing / Merging a Step

When the user asks *"Finish step"*, *"Done with step"*, or *"Merge to master"*:

1. Verify that the working tree is clean or commit changes:
```bash
git status
```
2. Run the finish command:
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\step.ps1 finish
```
3. Report the successful merge into `master` and the next recommended milestone.

---

## Safety Guarantees & Invariants

- **Clean Working Tree**: `step.ps1` halts if uncommitted changes exist.
- **Sequential Integrity**: Step `N` cannot start until Step `N-1` is merged into the base branch.
- **Health Verification**: `go vet ./...` and `go build ./...` must pass before branch switching and merging.

