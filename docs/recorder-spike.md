# Recorder, overlay and player spike (R2)

Measured on the Mac (Chrome, macOS) and on winpc (Windows, Chrome 157 and Edge 157), 16 Nov 2026. Everything runs on the stock engine of `docs/stock-engine.md`: stock Chrome or Edge, own raw CDP client over a pipe, no `Runtime.enable`.

## Verdict on R2: feasible, with limits
- Overlay, recording and replay work on stock Chrome and Edge without `Runtime.enable`. Record then replay for 3 data rows passed on the Mac, on Windows Chrome and on Windows Edge (every form, iframe and shadow value checked on the server).
- The overlay is NOT invisible. A page that looks can see one extra element in `<html>`. Everything else we measured is invisible (table below).
- The detector pages gave the same result with the overlay on as without it.
- Limits: scripted input only (no human recorded yet), no closed shadow roots, no canvas position, no file input, date picker, drag and drop or double click. See "Gaps".

## What was built
| Path | What |
|---|---|
| `core/recorder/locator.js` | One library for the recorder (build locators) and the player (resolve a target). Runs in isolated worlds only. |
| `core/recorder/recorder.js`, `recorder.go` | Overlay, event capture, binding, step conversion, `Macro()`. |
| `core/recorder/params.go` | `Parameterize(macro, {column: recordedLiteral})`. |
| `core/player/` | `Run(ctx, macro, row)`, typed errors, `Preflight`, `Locate`. |
| `core/recorder/rectest/`, `site.go`, `testdata/` | Scripted user (real CDP input), test pages on two origins, launch helper. |
| `cmd/recspike` | `record`, `replay`, `detect` harness. |
| `core/driver` (small additions) | `Launcher.StockHook`, `Page.Call`, `Page.Subscribe`, `Page.ParentSessionID`, `StockSession.Pages()`. Emulation errors "top-level targets" are skipped for out-of-process frames (see Failures). |

## How the overlay is injected
1. `Launcher.StockHook` runs for every tab and out-of-process iframe, after the injector and before the target resumes.
2. The hook sends `Page.addScriptToEvaluateOnNewDocument` with `worldName: "ladilala"` and `runImmediately`. The script (locator library plus recorder) runs in an isolated world of every document, including same-origin iframes and later navigations.
3. The overlay is one element with a random tag (`ladi-xxxxxx`), no attributes, appended to `<html>`, with a closed shadow root. The toolbar (Recording, Pause, Stop) is in the top frame only. The hover highlight is a `position: fixed` box inside the shadow root, so hovering changes no page-visible DOM.
4. Cross-origin iframes get their own hook run, so they get the recorder and the highlight (measured: a `localhost` iframe inside a `127.0.0.1` page recorded correctly).

## How events leave the page
- Tried first: `Runtime.addBinding` with `executionContextName`. No `bindingCalled` event arrived without `Runtime.enable` (measured). Not usable.
- Works: `Page.createIsolatedWorld` with the same world name returns the context the script already runs in (its globals were present). On `Page.frameNavigated` (and for initial frames) Go calls it, then `Runtime.addBinding` with `executionContextId`, then `__ladiFlush()`. `bindingCalled` arrives without `Runtime.enable`.
- Until the binding exists, events wait in a queue in the isolated world. The binding exists in that context only: `typeof ladiEmit` is `undefined` in the main world and a page call throws `TypeError`.
- The polling fallback was not needed and not built.
- Sent methods in a full recording: `Page.*`, `Runtime.evaluate`, `Runtime.addBinding`, `Runtime.runIfWaitingForDebugger`, `DOM.getFrameOwner`, `DOM.describeNode`, `Input.*`, `Emulation.*`, `Target.setAutoAttach`. Never `Runtime.enable`, `Console.enable`, `Debugger.enable` (asserted in every E2E).
- Cross-origin frame path: the embedding tab runs `DOM.getFrameOwner` for the frame ID (equal to the frame target ID) and `DOM.describeNode`. Both worked without `DOM.enable`. The path becomes `iframe#id`, `iframe[name=..]` or `iframe[src=..]`.
- A navigation is a `navigate` step only when no page action caused it. `Page.frameRequestedNavigation` tells: `anchorClick`, form submits and script navigations are suppressed (replaying the click repeats them). A first measurement with a time window alone wrongly dropped a typed URL that came 1 s after a click.

## Recorded step types
| Event | Step | Notes |
|---|---|---|
| address bar or first load | `navigate` | Origin added to `allowedOrigins`. |
| click (not on a text field, checkbox, radio, select) | `click` | `commitPoint` on a form submit button. |
| typing | `fill` | One step per field. Debounce 700 ms, flushed on change, blur, Tab, Enter, click. Value is the final text. |
| password field | `fill` with `{{password}}` | Value never leaves the page; variable `password` is declared. Checked: the typed password is not in the macro JSON. |
| select | `select` | Option value, else option text. |
| checkbox, radio | `check` | `"true"` or `"false"`. A label click records no `click` (by design, not exercised). |
| Enter, Tab, Shift+Tab | `press` | Carries a target; `commitPoint` for Enter in a form. Enter on a button or link is the click. |
| submit event | none | Covered by the click or Enter. |
| scroll | none | Ignored, as asked. |
Locator order: role+name, label, `#id` (not for generated ids), `data-testid`, `[name]`, short CSS path, XPath. A locator is kept only when it matches exactly one element at record time. Frames: `framePath`. Open shadow roots: `shadowPath`. The scripted session gave 27 steps and the exact expected action list: 0 missed, 0 extra, on all three browsers.

## Gaps
| Gap | What happens |
|---|---|
| `dblclick` has no action | Closed in Round 2. |
| Canvas | Round 2: detected and warned, no step recorded (not supported). |
| Date input | Closed in Round 2. |
| File input | Round 2: detected and warned, no step recorded (not supported). |
| Drag and drop, multi-select | Flagged on `dragstart` and on a multiple select. Not run. |
| Closed shadow root | Event target is the host, so locators address the host. Not run. |
| `{{` in typed text or a locator | Closed in Round 2 (`\{{` escape). |
| Nested out-of-process frame | The owner is assumed to sit in the embedding tab's main frame. |
| Player: `uploadFile`, `manual` | Return `ErrUnsupported`. |
| Player: `select` and `check` | `select` sets the value and fires `input` and `change` from a script (not trusted events). `check` uses a real click. |

## What a page can and cannot detect (main-world probes, same injector, overlay vs control browser)
| Probe | Result |
|---|---|
| Window and document own properties | identical (PASS) |
| `Function.prototype.toString` of 10 natives | identical, all `[native code]` (PASS) |
| `ladiEmit`, `__ladiRec`, `__ladiLoc`, `__ladiFlush` in main world | all `undefined` (PASS) |
| Page calls the binding | `TypeError` (PASS) |
| `getEventListeners` in a page | `undefined`; DevTools `getEventListeners(window)` lists the same `click:1` as the control, isolated-world listeners are absent (PASS) |
| `Runtime.enable` in sent methods | 0 (PASS) |
| Host element | SEEN: one extra child of `<html>`; `querySelectorAll('*')` 43 to 44; `document.all` 43 to 44; `outerHTML` +27 chars |
| `MutationObserver` on `document` armed before our script | SEEN: it receives the host element |
| Page `click` listener | SEEN: clicks on the toolbar arrive with `target` = the host (every click aimed at the toolbar: 4 of 4 on the Mac, 3 of 3 on Windows) |
The host cannot be hidden from DOM queries: it must be in the document to draw. A random tag name and no attributes make it unmatchable by selector, not invisible. Not tried: highlight through the `Overlay` CDP domain (draws outside the DOM) and a toolbar in the app window instead of the page. Both would remove the host.

## Detector results with the overlay on
Pages: bot.sannysoft.com and bot-detector.rebrowser.net, overlay on vs `-overlay=false` control, same browser, same injector.
| Browser | sannysoft | rebrowser |
|---|---|---|
| Mac Chrome | 0 failed, 31 passed | same as control: nothing yellow or red |
| Win Chrome 157 | 0 failed, 31 passed | same as control: `headless` yellow, the rest green or not triggered |
| Win Edge 157 | 0 failed, 31 passed | same as control: `useragent` red (not Google Chrome, as in `stock-engine.md`), headless yellow |
`runtimeEnableLeak`, `cdpBindingGlobals`, `nativeTampering`, `workerConsistency`, `navigatorWebdriver` stayed green. Rebrowser ratings: -1 green, 0 not triggered, 0.5 yellow, 1 red. CreepJS and BrowserScan were not rerun.

## Measurements
| Measure | Mac Chrome | Win Chrome | Win Edge |
|---|---|---|---|
| Hook time per target (ms) | 0.6 to 1.7 | 0.5 to 5.3 | 1.5 to 8.1 |
| Overlay mounted, ms after navigation start, median of 8 documents | 8 | 12 | 22 |
| First document (cold) | 22 | 157 | 137 |
| Page `Date.now()` to Go receive (ms) | 0 to 3 | -1 to 1 | -1 to 1 |
| Replay of 27 steps, 3 rows (s) | 5.1 | 5.7 to 6.3 | 5.5 to 5.8 |
Latency is below the clock granularity (1 ms). Replay time is mostly the 400 ms wait after each click or key press that checks whether a navigation starts. On the slow detector pages the overlay mounted 1.4 s after navigation start because `<html>` appeared late; on Windows it was never later than the page itself.
Locator stability, page redeployed with wrapper elements, a banner in front and generated ids (3 recorded targets): the whole locator list resolved 3 of 3. By kind: role 3 of 3, label 2 of 2, `[name]` 2 of 2, `#id` and short path 0 of 3, XPath 0 of 3. The delayed field (700 ms) was filled because the player waits for the locator.

## Failures met
- `Emulation.setDeviceMetricsOverride` fails on an out-of-process frame ("top-level targets"). `LaunchStock` would count it as a setup error and `Verify` would fail on any page with a cross-site iframe. Fixed in the attach path.
- A hook that calls `Page.createIsolatedWorld` on a paused out-of-process frame blocks until the frame resumes (40 s hang). The first bind now runs in the background.
- Type-ahead on a closed `<select>` records and replays correctly on Mac and Windows; opening the native popup was not tried.

## Next steps
1. Record by hand with `recspike record` on a real site; this spike used a scripted user only.
2. Decide the host question: accept one visible element, or draw the highlight with the `Overlay` domain and keep the toolbar in the app.
3. Add `dblclick` and `uploadFile` capture to the schema, and an escape for `{{`.
4. Replace the fixed 400 ms navigation wait with a signal from the page.
5. Test popups, dynamic iframes, closed shadow roots, contenteditable and a managed machine with Chrome policy.

## Run it
`LADILALA_E2E=1 go test -count=1 -v -run 'TestE2ERecord|TestE2EPlayer' ./core/recorder/ ./core/player/` (add `LADILALA_E2E_BROWSER=edge` for Edge). Detectors: `go run ./cmd/recspike detect -engine chrome` (and `-overlay=false`).

## Round 2: gaps closed
Measured on the Mac (Chrome) and on winpc (Chrome 157 and Edge 157), 16 Nov 2026.

### What changed
| Area | Change |
|---|---|
| Schema | New action `dblclick`. The `\{{` escape. Older v1 macros load unchanged; unknown fields still fail. `uploadFile` is unchanged (the player still returns `ErrUnsupported`). |
| Double click | The recorder records the pair as one `dblclick` step (it removes the first click it had already sent). The player sends press and release twice with click counts 1 and 2. |
| Date inputs | The recorder reads the field value when the user leaves it or clicks on (not on every `change`; Chrome fires one per digit of the year) and ignores Tab between segments. The player sets the ISO value with the native setter in the isolated world, then fires `input` and `change` (bubbles, composed), then reads it back. |
| Typed error | `ErrBadValue` (`*BadValueError`: step, field, type, value, expected format, reason). Raised for a non-ISO value or a value below min or above max. Not retried. The field keeps its old value. |
| Canvas, file input | Not recorded. The recorder warns while recording and returns `[]Warning{Kind, StepIndex, Locator, Message}`. Kinds: `canvas_unsupported`, `file_input_unsupported`. `StepIndex` is the number of steps recorded at that moment. |
| Warning UI | The hover box turns amber on a canvas, a file input, its label or a drop zone. The toolbar shows a note for 5 s ("Canvas is not supported ...", "File upload is not supported ..."). Frames pass the note to the top frame through Go. |
| `{{` escape | The recorder escapes `{{` in typed text and locators instead of dropping the step. |
| Navigation wait | The fixed 400 ms wait is gone. See below. |

### What the page sees
Double click, recording and replay (real `Input.dispatchMouseEvent`): `mousedown:1 mouseup:1 click:1 mousedown:2 mouseup:2 click:2 dblclick:2`. Both sides gave this exact list.
A date step fires `input` and `change` from the isolated world (not trusted events). A React-style controlled input (own `value` property with a tracker, state restored after each event) took all 5 types. A script that sets `el.value` from the page world would be ignored by it; the isolated world setter is not.

### Navigation wait
After a click or key press the player sends one `Runtime.evaluate` of `0`, then looks at `Page.frameRequestedNavigation`. None within 40 ms: go on. One: wait for `Page.lifecycleEvent` `load`, `Page.navigatedWithinDocument` or `Page.frameStoppedLoading` of that frame (cap 30 s for the tab, 5 s for an iframe). The player turns on `Page.setLifecycleEventsEnabled` for this. No `Runtime.enable`.
| Replay of the 27-step session, 3 rows (Mac) | Before | After |
|---|---|---|
| Time per row, Mac | 5.1 s (5.14, 5.09, 5.11) | 1.4 s (1.46, 1.43, 1.45) |
| Time per row, Windows Chrome | 5.7 to 6.3 s (round 1) | 1.9 to 2.4 s |
| Time per row, Windows Edge | 5.5 to 5.8 s (round 1) | 1.9 to 2.1 s |

### Results (Mac Chrome)
| Test | Result |
|---|---|
| Existing 27-step record, replay for 3 rows, detector probes, locator stability | pass; `Runtime.enable` 0 |
| Double click: record gives `navigate,click,dblclick`; replay event list equals recording | pass |
| Date: 5 types x 3 rows x (plain, controlled, same-origin iframe, open shadow root); server and page state checked | pass, 3 of 3 rows |
| Date: 7 rejections (below min, above max, early time, bad format, impossible day, week 54, `9:5`) | pass, all `ErrBadValue` with field and format |
| Date: record real segment edits, parameterize, replay another value | pass |
| Canvas in page, iframe, shadow root, plus a drag: warnings, no step | pass |
| File input, label of a hidden input, drop zone: warnings, no step | pass |

Windows (winpc, `LADILALA_E2E=1 ... -run 'TestE2ERecord|TestE2EPlayer'`): final run passed on Chrome (recorder 5 of 5, player 4 of 4 tests) and on Edge (same). Earlier runs on Windows showed failures that were test-harness effects, not product errors: the first mouse press in a freshly launched window can be swallowed (browser started from an SSH session, no active window), which dropped a typed field once in about 3 runs and left a first button press unseen. The scripted user now retries the first field and the replay tests start with a harmless click. Real users on a desktop session were not tested for this.

### Still fails or untested
- Canvas and file upload are not supported by design. A canvas click or file pick is missing from the macro; the warning list is the only trace.
- Date typing by key is not recorded as keys: only the final value. Typing was unreliable over CDP (digits were dropped); the test uses ArrowUp and Tab. The calendar popup was not driven.
- A date `fill` in a cross-origin frame was not run. Min and max are the only constraints checked; `step` mismatch is not.
- Drop of real files on a zone is only detected on `drop` with files; no test sends one.
- The warning highlight and the toolbar note were not checked by a screenshot.
- A replay in a window the OS has not activated may lose its first mouse press on Windows; the player does not retry a click.
- `dblclick` on a text field is not recorded (the field path takes the clicks).

## Round 3: search box Enter

Bug: on a Google search the recorded macro had the fill step but no Enter press, and a navigate step to the results URL.
Evidence (winbox macro "Find google", 4 steps, copy of the SQLite file):

```
s002 click  css #sI1XGe
s003 fill   role combobox "Tim kiem" | textarea[name="q"]   value {{email}}
s004 navigate https://www.google.com/search?q=Xin+chao&sca_esv=...   (no press step)
```

Root cause: the Google query box is a `textarea` with role combobox. The keydown rule returned early for every
textarea (a textarea Enter is a new line), so no press was sent. Then the page changed location by script, the
fill was not yet flushed, and no press had set `lastAction`, so `onNavigated` recorded the results URL as navigate.
Not the cause: preventDefault alone, and the 3 s suppression window (the nav came within it).

Fix:
- `recorder.js`: Enter in a textarea, contenteditable or a combobox without a form sends a press marked `maybe`
  (flushes the fill first, Shift+Enter is a new line).
- `recorder.go`: a `maybe` press is held. A main frame navigation within 3 s of an Enter press, with or without a
  `frameRequestedNavigation` event, is not a step: it turns the held press into a step with `commitPoint`, or sets
  `commitPoint` on a recorded Enter press. A later step drops the held press. A fill that repeats the previous fill of the
  same field replaces it. A typed URL after the window is still a navigate step.
- `MapVariable` and `Parameterize` rewrite only fill and select steps, so URLs are never rewritten.

Tests: `TestE2ERecordSearchScript`, `TestE2ERecordSearchGetRedirect` (302 hop),
`TestE2ERecordTextareaNewline`, and `TestE2EPlayerSearchEnter` (record, then replay 3 rows, each row searches its own text).
Before the fix the script test recorded `navigate,navigate,navigate`. Real google.com was not driven (no winpc use, consent page).
