# Slice C: build and own the engine ourselves (16 Nov 2026)
Sources: GitHub API (gh), raw repo files, chromiumdash, gitiles (Chromium main), GitHub billing docs. Patch line counts are my own count of +/- lines in each repo's .patch files. No binaries run, no ChatGPT/Gemini consult used. "Est." = estimate, not measured.

## 1. Base: ungoogled-chromium-windows (UCW)
- BSD-3-Clause, 2648 stars, 20 contributors, pushed 13 Nov. Upstream ungoogled-chromium: 28k stars. Both active.
- CI (reusable-build.yml): 16 chained jobs on the standard `windows-2022` runner (free for public repos), `build.py --ci -j 2`. Each job builds until a timeout (exit code 124), uploads the build tree as an artifact (7z, retention 4 days), next job downloads and resumes. This gets past the 6 h job limit with no paid runner.
- Cost of that: slow. Run build-x64 started 13 Nov 17:51Z, ended 15 Nov 09:31Z, about 40 h wall (includes queue time). No sccache or ccache found in the stage script (grep); the resume artifact replaces it.
- Artifacts (157.0.8103.50): x64 zip 204 MB, installer 131 MB. Releases carry no signature file in the asset list: I saw no Authenticode step and no checksum assets. Treat as unsigned (not verified beyond the asset list).
- Tracking speed (UCW release time vs chromiumdash stable release time; dates may be a day off):
| UCW tag | UCW release | Upstream stable | Lag |
|---|---|---|---|
| 157.0.8103.50 | 12 Nov | 9 Nov | 2.4 d |
| 157.0.8103.38 | 7 Nov | 3 Nov | 4.1 d |
| 156.0.8078.79 | 1 Nov | 29 Oct | 3.0 d |
| 156.0.8078.73 | 30 Oct | 27 Oct | 3.1 d |
| 156.0.8078.47 | 25 Oct | 20 Oct | 4.9 d |
| 155.0.8059.84 | 19 Oct | 15 Oct | 3.9 d |
UCW also shipped 155.79, 155.39, 154.97, 154.92 in the same window. Typical lag 2-5 days. It builds 157 stable; 158 is already listed upstream.
- Contributor check: none found beyond GitHub accounts and PR review; I did not find a signing-key policy. Unverified.
- Alternatives: stock Chromium + depot_tools needs about 100-150 GB disk, 16+ cores, 3-6 h clean on a big box (Google's docs; est.), and no privacy patches we would then maintain ourselves. Helium-windows (GPL-3.0, 1.5k stars): copyleft, skip. Thorium-Win (BSD-3, 2.5k stars, pushed 9 Nov): perf flags, not a privacy base, tracking speed not checked.

## 2. Patch sources (all counts mine, all unrun)
| Source | Licence | Patches | Lines (+/-) | Files touched | Notes |
|---|---|---|---|---|---|
| Clearcote (clearcotelabs/clearcote-browser) | BSD-3 | 62 (series of 62, README says quilt, applied on top of UCW) | +14.9k/-0.7k | 169 | 1 contributor, open base is 151 (UPSTREAM_REVISION). Seed: `--fingerprint=<n>`, mixed per eTLD+1 in `components/ungoogled/farble_seed.cc`. |
| Fury core (furyteamtop/fury-antidetect-browser/core/patches) | BSD-3 patches, AGPL app | 44 | +6.2k/-0.1k | 146 | Persona model; app is AGPL. |
| Apostate (heretic-tech/apostate) | GPL-3.0 | 137 | +26.5k/-2.0k | 263 | Copyleft: would make our engine GPL. |
| fingerprint-chromium (adryfish) | BSD-3 | repo shows only LICENSE and READMEs now (no patch dir) | n/a | n/a | Patch source not public here: not usable as a starting point. Our canvas hang is in its binary. |
- Clearcote per-surface patch sizes (+/-): seed core 291, switches 153, canvas 457/142 over 14 files (html_canvas_element, base_rendering_context_2d, text_metrics, canvas_path), audio 61/6 over 2 files (audio_context, offline_audio_context), hardware concurrency and deviceMemory 162 over 5 files, fonts 542 in `font_cache.cc`, WebGL GPU strings 1164/44 over 10 files, screen 276 over 3 files, UA and client hints 473/8 over 9 files (user_agent_utils, client_hints, user_agent_metadata), timezone 37 in `timezone_controller.cc`, WebRTC 93 over 4 files.
- Seeds in Clearcote: command-line switch forwarded browser to renderers (000-fingerprint-switches patches `render_process_host_impl.cc`). Fury and Apostate: not read in detail.
- Rebase cost: no source documents it per major. Clearcote's own build notes show ThinLTO needs 31 GB RAM, 120 GB disk, about 12 h on 8 cores.
- Cleanest vendored start: Clearcote's patch files (BSD-3, flat quilt series, ungoogled-compatible, each patch separable) as READ-ONLY reference, then write our own small set. Adopting 15k lines from a 1-contributor repo repeats the objection the user raised.

## 3. Smallest viable patch set
- Built-in Chromium switches: I searched Chromium main `third_party/blink/common/features.cc` (107 KB) and `runtime_enabled_features.json5`: no `FingerprintingCanvasImageDataNoise`, `fingerprinting-canvas-image-data-noise`, `fingerprinting-client-rects-noise` or `measuretext-noise` entries. Only canvas items found are Canvas2D hibernation and auto-flush params. GitHub code search was rate limited, so this is "not found in 2 files", NOT proof they do not exist. Unverified. Do not plan on them.
- Minimal change points (lines are est., modelled on Clearcote's file lists above):
| Surface | Files | Est. lines | Rebase pain |
|---|---|---|---|
| Seed plumbing (switch to renderer) | content_switches, render_process_host_impl.cc, small new component | 150-250 | low |
| Canvas readback noise | HTMLCanvasElement toDataURL/toBlob, BaseRenderingContext2D getImageData, OffscreenCanvas | 200-400 | high (canvas code moves each major) |
| WebGL vendor/renderer + param table | WebGLRenderingContextBase and WebGL2 (getParameter), debug-renderer-info | 150-300 (table kept as data) | medium |
| Audio | AudioContext, OfflineAudioContext, AnalyserNode | 60-120 | low |
| Fonts | FontCache (allow-list per profile) | 150-500 | medium |
| Client rects | Element getBoundingClientRect/getClientRects | 50-100 | low |
| hardwareConcurrency, deviceMemory, screen | navigator_concurrent_hardware.cc, navigator_device_memory.cc, screen.cc | 100-200 | low |
| UA client hints brands | user_agent_utils.cc, user_agent_metadata.cc | 150-300 | medium (brand code changes) |
Total est. 1,000-2,200 lines, 25-40 files, versus 6-26k lines in the three public sets. Timezone, WebRTC, language: already handled by CDP emulation or switches in our core (see windows-spikes), not needed in the patch set.

## 4. Operating model (all est.)
- Cadence: every Chromium stable (about 4 weeks) plus security point releases (UCW ships every 2-5 days). Rebase security releases: patches stay on the same major, so re-apply and rebuild only.
- Build: GitHub public repo, copy UCW's 16-stage `windows-2022` workflow (free), or a paid larger runner: Windows 64-core $0.322/min, 32-core about $0.16/min (docs row truncated, so approximate). A 64-core ThinLTO-off build at 2-3 h est. = about $40-60 per build. Self-hosted: 32 GB RAM, 120+ GB disk, 16+ cores.
- Regression gate: canvas toDataURL read repeated 50 launches on Windows (our R8 hang test), CDP pipe smoke, fingerprint coherence (UA-CH vs navigator vs WebGL), same-seed repeatability. Needs a Windows runner with a real desktop session: self-hosted or winpc.
- Signing: Azure Trusted Signing ("Artifact Signing"): about $10/month Basic, eligibility limited by country and business history (unverified, from memory; my fetch of the pricing page returned nothing). EV cert: roughly $250-500 per year (unverified). Unsigned binaries trigger SmartScreen, but the engine is launched by our signed app from a download dir, which weakens that concern.
- Distribution: manifest.json with sha256 per file and ed25519 signature; Engines screen verifies before first launch; keep previous 2 builds for rollback.
- Effort, person-days (est.): pipeline fork and CI 3-5; first working build with seed plumbing + canvas + WebGL + UA-CH 10-15; regression gate 3-5; steady state 3-6 per month (rebase per major is the bulk; points are re-runs).

## 5. Comparison
| Option | Cost | Control | Risk |
|---|---|---|---|
| Own patched build on UCW | 15-25 days up front, 3-6 days/month, $50-300/month CI | full | rebase and Windows build ops |
| Commercial engine licence | price unknown; CloakBrowser, Clearcote 157, BotBrowser are paid; round-1 notes dispute terms | none | vendor lock-in, closed binaries |
| Stock Chrome plus injection | lowest | JS-level only, detectable; covered by another worker (dependency noted) | no canvas/WebGL engine-level coherence |

## Recommendation
- GO, scoped: UCW base plus our own 1-2k-line patch set (Clearcote files as a licensed reference only). Do not adopt any 14-26k-line set whole.
- Cheapest first milestone (1 week): fork UCW at the 157 tag; add the seed switch plus canvas toDataURL/getImageData noise plus hardwareConcurrency/UA-CH (about 500 lines); build on one rented 64-core Windows runner; run our 50-launch canvas hang test and `chromedp` pipe smoke on winpc. Proves hang absence, CDP, size, time.
- Three biggest risks: (1) rebase of canvas/WebGL code every major eats the 3-6 days/month budget; (2) a Windows-only hang or crash we cannot diagnose without symbols (the R8 hang came from a patched build); (3) no signing or supply-chain trust story, since UCW itself ships unsigned.
