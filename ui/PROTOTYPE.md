# Ladilala control UI: throwaway prototype

Mock data, in-memory state, no backend. Delete this directory once the real UI is chosen.
Run: `cd ui && bun install && bun run proto`, open http://192.168.1.34:5190/ . Build check: `bun run build`. Styling notes: STYLING.md.

## What changed (rev 1 to 5)
- Profiles is the split layout only. UI rebuilt on shadcn-vue + Tailwind v4 in a native macOS language (source-list sidebar, toolbar, grouped inset forms, Finder-style tables), flat palette, no gradients. See STYLING.md.
- Engines screen: three tiers (stock Chrome and Edge, Chrome for Testing, own Chromium shown as not available), install and update flow, Doctor. Ctrl+K opens the quick switcher.
- Engines, Settings and App updates run on real Go services inside the Wails window (`src/bridge/engines.ts`, `settings.ts`, `appupdate.ts`). In the browser prototype the same bridge files answer from a mock, so the screens work here too.

## Simulate menu (button, bottom right)
Switch on a problem (network down, checksum mismatch, policy blocked, disk full, orphan process, stale lock, engine hang), then:
- Engines tab: click Install on Chrome for Testing (tier 2). It fails at the matching stage with a plain reason and a one-click fix; an engine that already works stays active.
- Doctor tab: Run diagnostics (6 checks), Test launch, Common problems. The Dependencies tab is gone: a missing browser is fixed by installing Chrome for Testing.
Fixes clear the simulated problem, so you can retry.

## Questions for you
1. Does it read as a native Mac app now? What still feels like a web page?
2. Engines flow: is Update, stages, failure, one-click fix clear to a non-technical user?
3. Doctor: are diagnostics, Test launch and Common problems useful, or too much?
4. Mapping drag feel and run view clarity.

## App update channel (Ladilala itself, mocked)
- Settings, App updates: version, update feed URL, channel, auto-check, install on quit, skip, release notes. No roll back and no pause: the Wails updater has neither. With no feed URL the group says updates are off. A sidebar footer item and the What's new sheet drive download, verify, prepare, restart. Real app: Wails v3 updater.
- Simulate menu, "App update" section: available, beta, network fail, signature fail, low disk, needs newer engine, ready, none, no feed set. The sheet shows the engine need but does not block the update. The simulate menu is hidden inside the Wails window.

## Language (Vietnamese default, English)
- Switch in three places: sidebar footer (VI | EN), Settings, first group (Tiếng Việt | English), and Ctrl+K "Đổi ngôn ngữ / Change language". It applies instantly, persists in `localStorage["ladilala.locale"]` and sets `<html lang>`.
- Texts live in `src/locales/{vi,en}.json` (vue-i18n); dates, numbers, sizes and country names use Intl (`src/format.ts`). `bun run i18n:check` fails on missing keys or hard-coded English. Terms and key naming: `../docs/i18n-glossary.md`.
