export ORCA_WORKER_ID=<worker_id> ORCA_SIGNAL_FILE=/Users/8bu/Projects/ladilala/.orca/signals/<worker_id>.jsonl PATH="$HOME/.agents/skills/orca/scripts:$PATH"

# Brief: <pkg>

## Goal
<one paragraph: what this package does and why. Link the vault plan: the OKF bundle at the vault folder NoteBook/Ladilala, start at index.md; decisions are in decisions/, research in research/>

## Target
Files you may edit: <dirs>. Do not touch anything else.

## Change
<numbered steps, concrete APIs>

## Contract
<shared types or interfaces other packages depend on>

## Acceptance
<observable pass tests, exact commands>

## Rules for every worker (copy unchanged)
1. Do NOT use the `task` tool or any subagent.
2. If the work has independent slices, run `/orca` yourself. Spawn your own workers, each in a NEW herdr tab. Give them the same rules.
3. **Use `eval` as much as possible to save quota.**
   - Do multi-step work in ONE cell: `await tool.<name>(args)` for session tools, `sh("cmd")` for shell.
   - Run independent calls with `Promise.all`.
   - Run gofmt, build, vet and test (macOS and `GOOS=windows`) in one cell. Return only failures.
   - Read files with grep or a line range inside a cell. Print or return only what you need.
   - Use a direct tool call only for a single step where a cell adds nothing.
4. Run only the checks listed under Acceptance. No repo-wide audits.
5. Emit milestones: `orca-signal progress note=<short>`. When done and checks pass: `orca-signal done`. If blocked: `orca-signal blocked reason=<short>`.
   **Then ping the orchestrator as your very last action, exactly once** (it does not watch your signal file):
   `herdr agent prompt orca-main "orca-wake: <worker_id> <done|blocked> <one-line result, max 200 chars, no quotes>"`
6. Do not commit. No stubs, no TODOs. Report files created, public API, and anything unverified.
