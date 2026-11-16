# Rename report: WinMacro to Ladilala

Decision: ADR 0015 (decided 2026-11-16). Domains are NOT bought.

## What changed
- Go module is now `github.com/8bu/ladilala`. All imports updated.
- Env vars: `LADILALA_E2E`, `LADILALA_FPC_DIR`, placeholder `__LADILALA_CFG__`.
- UI: sidebar, `ui/index.html` title, about and update sheets, `ladilala.locale` key, package `ladilala-ui-prototype` (and bun.lock), en.json and vi.json strings.
- Data folder `%APPDATA%\Ladilala`, temp prefixes, fixtures.
- Repo docs and the vault. The `winmacro` front matter block is now `ladilala`; `scripts/validate-okf.py` accepts the new key.
- Vault: ADR 0015 decided, naming-and-brand Round 5 and Status, log.md entry, root and sub indexes.

## Checks
- gofmt clean, go build, go vet, Windows go vet, go test ./core/...: pass.
- ui build and i18n:check pass; no "gradient" in ui/src.
- validate-okf.py: 0 errors.

## Residual allowed hits
- `file:///Users/8bu/Projects/winmacro/...` source URIs in the vault.
- Directory paths: repo folder, vault folder `NoteBook/WinMacro`, `.orca/playbooks/winmacro-task.md`.
- winpc folder `%USERPROFILE%\winmacro`, `/tmp/winmacro.tgz` in the winpc runbook.
- History: old log.md entry, archive/full-assessment.md, docs/naming.md, docs/doc-gap-review.md, round history in naming-and-brand, ADR 0015 scope note.
- `V/WinMacro.md` and `WinMacro/00 Index.md` names in old-structure descriptions.

## Needs the orchestrator
- Rename repo folder and vault folder (then fix file:// URIs, validator DEFAULT_BUNDLE path, .orca references).
- Rename the winpc test folder.
- Trademark clearance, domain purchase, tagline.
