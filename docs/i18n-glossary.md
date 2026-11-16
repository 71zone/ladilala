# i18n glossary (VI default, EN second)

Setup: `ui/src/i18n.ts` (vue-i18n, Composition API, `legacy: false`, locale `vi`, fallback `en`, stored in `localStorage["ladilala.locale"]`, sets `<html lang>`), `ui/src/composables/useLocale.ts` (switch), `ui/src/format.ts` (Intl numbers, sizes, durations, relative times, dates, country names), `ui/src/lazy.ts` (messages kept in reactive state, resolved at render so they follow the language). Messages: `ui/src/locales/{vi,en}.json`. Check: `bun run i18n:check` (key parity, undefined keys, hard-coded English in templates and `notify()`).

## Key naming convention
- `screen.element`: top-level group per screen: `nav, profiles, editor, proxies, macros, mapping, runs, engines, settings, appUpdate, palette, simulate, toasts, errors, checks, common` (plus `status, proxyStatus, noise, language, macroData`).
- camelCase leaves; suffixes by role: `.title`, `.desc`, `.help`, `.sub`, `.action`, `.placeholder`, `.aria`, `.tip`, `.empty`; destructive confirms use `xTitle`, `xDesc`, `xAction`.
- Enumerations map to a sub-group keyed by the value, so code builds `t('runs.state.' + state)`; never concatenate sentences.
- Named placeholders only (`{name}`, `{n}`). Plurals use `|` with `t(key, {n}, n)`: EN two forms, VI one form (no plural in Vietnamese).
- Numbers, sizes, speeds, durations, dates, relative times and country names come from `format.ts` (Intl), never from the message text. Vietnamese output: `1.234,5`, `42,6 GB`, `7,4 MB/giây`, `14 thg 11, 2026`, `Hôm qua`.
- Mock data (profile names, hosts, UA strings, paths, step targets, fake shop page) stays English. Mock text that must translate is stored as `@key` and resolved with `txt()` (macroData.*).
- Production note: for the Wails build add `@intlify/unplugin-vue-i18n` to precompile messages if a CSP forbids runtime compilation.

## Terms
Kept English? y = same word in VI. Review = needs the user's call.

| Term | VI | EN | Kept English? | Note |
|---|---|---|---|---|
| profile | profile | profile | y | Common word in the VN anti-detect community. Review. |
| proxy | proxy | proxy | y | Same. |
| macro | macro | macro | y | Same. |
| run | lượt chạy | run | n | Verb: Chạy. |
| row | dòng | row | n | Data row. |
| ledger | sổ theo dõi | ledger | n | Record that stops double submits. Review. |
| commit point | điểm chốt | commit point | n | Step that cannot be undone. Review. |
| uncertain | chưa rõ | uncertain | n | Row state after a commit point crash. |
| takeover / hand back | tiếp quản / trả lại | takeover / hand back | n | User drives the browser by hand. Review. |
| engine | engine | engine | y | The browser binary. Review ("trình duyệt lõi" is longer). |
| fingerprint | fingerprint | fingerprint | y | Review. |
| noise / real / off | nhiễu / thật / tắt | noise / real / off | n | Fingerprint modes. |
| canvas, WebGL, WebRTC, AudioContext | same | same | y | API names. |
| cookie | cookie | cookie | y | |
| session | phiên | session | n | |
| update channel | kênh cập nhật | update channel | n | Stable, Beta, Nightly stay English. |
| roll back | khôi phục (bản cũ) | roll back | n | |
| pin | ghim | pin | n | Pin engine version per group. |
| guard | điều kiện | guard | n | Skip rule on a step. Review. |
| expect | kỳ vọng | expect | n | |
| locator | bộ định vị | locator | n | |
| binding / mapping | liên kết / ánh xạ | binding / mapping | n | |
| dependency | phụ thuộc | dependency | n | |
| diagnostics / test launch | chẩn đoán / chạy thử | diagnostics / test launch | n | Doctor stays "Doctor". |
| fail closed | ngắt an toàn | fail closed | n | No traffic without the proxy. |
| mirror | mirror | mirror | y | Download mirror. |
| seed | seed | seed | y | |
| Start / Stop / Pause / Resume | Bắt đầu / Dừng / Tạm dừng / Tiếp tục | same | n | |
| Save / Cancel / Delete / Remove | Lưu / Hủy / Xóa / Gỡ | same | n | Delete = data, Remove = detach. |
| Update / Check / Restore / Settings | Cập nhật / Kiểm tra / Khôi phục / Cài đặt | same | n | |
