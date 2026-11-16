# Tier 3 engine plan: build the spoofing engine first

Date: 2026-11-17. Status: proposal, waits for user decision. Not legal advice.
Tags: [MEASURED] repo doc or winpc probe. [SOURCE] cited page, read 2026-11-17. [INFERENCE] my estimate, not tested. Source list at the end.

## 1. Summary

- Tier 1 (stock Chrome, own CDP client, own injector) passes the bot checks. Detectors still flag audio noise and client-rect noise. WebRTC shows the real IP. Fonts, deep GPU values and worker `userAgentData` stay open [MEASURED, S16].
- Recommended engine: own patches on ungoogled-chromium-windows (option a). Phase 1 is the small set that fixes what detectors flag (option c).
- Do not use fingerprint-chromium as the base (option b). Its 148 patch source is public, but 148 is 9 majors behind 157. It has one maintainer and open canvas crash bugs.
- winpc can run development builds. It cannot run release builds with ThinLTO. A first full build takes about 6 to 8 hours on its NVMe disk [INFERENCE]. Drive D: is a hard disk, not an SSD [MEASURED].
- Milestone 1 (M1) costs 0 USD and 7 working days. It has three kill gates (section 5). Recommendation: GO on M1 only.

## 2. What tier 1 does and what only an engine does

| Surface | Tier 1 today (`core/inject`, CDP) | Detectors see it today [MEASURED, S16] | Only an engine fixes it? |
|---|---|---|---|
| UA string, UA-CH | CDP override on page, iframe, dedicated worker. Injector adds worker `userAgent` | No flag. Worker `userAgentData` is not covered | Small patch. Try CDP on worker sessions first [INFERENCE] |
| Timezone, locale, languages | CDP plus injector | No flag | No |
| Screen, taskbar delta | CDP metrics plus injector | No flag. A cross-OS profile gives yellow `platformMismatch` | No. Cross-OS also needs fonts and rendering |
| hardwareConcurrency, deviceMemory | CDP plus injector, page and worker | No flag. 0 of 20 first-script leaks | No |
| WebGL vendor, renderer, 13 parameters | Injector table | sannysoft passes. Extensions, precision, other parameters unchecked | Deep parameters: yes |
| WebGL pixels | Injector flips 1 in 16 bytes of `readPixels` | Unknown. Pixels still come from the real GPU | Noise only. No engine can fake GPU raster |
| Canvas 2D read-back | Injector copies to a scratch canvas | No CreepJS lie. A read in the first 300 ms is garbage in bare Chrome too | Yes, for a path with no wrappers |
| Audio | JS noise on `AudioBuffer`, `AnalyserNode` | CreepJS: "sample noise detected" | Yes (DSP layer) |
| Client rects | JS jitter of 1/64 px | CreepJS: "unknown rotate dimensions" | Yes (layout layer) |
| Fonts | No font code | Not measured. All profiles on a host share its real font list | Yes |
| WebRTC | Nothing | CreepJS shows the real srflx IP | No. Use flag `--force-webrtc-ip-handling-policy=disable_non_proxied_udp`. Test on stock Chrome [INFERENCE] |
| Plugins, mediaDevices, voices, battery | Nothing | Not measured | No. Inject |
| CDP leak, webdriver, headless | Raw CDP, no `Runtime.enable` | All green | No |

- Detectors tested so far: sannysoft, rebrowser, CreepJS (headless, stealth, lies), BrowserScan bot check. Fingerprint pages for canvas, audio and fonts are not recorded. M0 (section 5) fills this gap.
- An engine patch does not prove stealth. Users report BrowserScan and pixelscan flags on fingerprint-chromium canvas noise (issues #30, #87). Issue #98 shows that noise keyed to buffer coordinates breaks sub-rectangle reads [SOURCE, S12].

## 3. Options

| | (a) UCW plus own patches | (b) Fork fingerprint-chromium patches | (c) Vanilla Chromium, minimal set | (d) Tier 1 plus more injection |
|---|---|---|---|---|
| Scope | Full set: seed, canvas, WebGL, audio, rects, fonts, UA-CH, hardware. 1,000 to 2,200 lines (ADR 0005) | 18 patches, +3,400 lines at tag 148. The GPU patch is 2,294 of them | Seed, canvas, audio, rects. About 450 to 800 lines | Worker UA-CH test, WebRTC flag, plugins, mediaDevices, voices, GPU table |
| Weeks to v1 | 6 to 9 [INFERENCE]. ADR 0005: 15 to 25 days | 4 to 7 [INFERENCE] | 3 to 4 [INFERENCE] | 1 to 2 |
| Ship in 1 week | M1 build on tag 157.0.8103.78 (seed, canvas, audio, rects) | Rebuild of tag 148 as is. Not 157 | Same as M1 | Everything in scope |
| Cost per month | 0 USD (winpc plus free runner). 25 to 85 USD with on-demand cloud [INFERENCE] | Same as (a) | Same as (a) | 0 USD |
| Rebase per Chromium major | 3 to 6 days (ADR 0005) | First rebase 148 to 157: 2 to 4 weeks [INFERENCE]. Then as (a) | 1 to 3 days [INFERENCE] | 0.5 day of tests |
| Licence | Chromium and ungoogled-chromium: BSD-3. Our patches: our choice | Repo says BSD-3. Two Bromite patches are GPL-3.0 upstream [S14] | As (a) | Own code. Apache-2.0 ideas, `core/inject/NOTICE` |
| Update and signing | Zip plus Ed25519 manifest on R2. No Authenticode (ADR 0020). Our app downloads it, so no SmartScreen prompt (measured for CfT) | Same | Same | Vendor-signed Chrome. Auto-update |
| Main risk | Rebase of canvas and layout code. Crash without symbols | Inherited bugs #94, #95, #96, #98, #104, #109 | No UCW de-Google patches. Needs `depot_tools`, 100 GB, fetch over 1 hour | Audio and rect lies stay. Fonts never |

- (a) and (c) share base, toolchain and winpc setup. (c) is phase 1 of (a).
- Why 148 and 144 had no hang: no repo doc says. The best evidence is that the 150 patch set added it, because stock 150 never hangs [MEASURED, S16]. Issues #94, #95 and #104 show renderer crashes on pixel read-back in 150 only. Tag 148 holds the same unparenthesised `writable_addr` macro that PR #109 fixes, so that bug alone does not explain it. The 150 source is withheld until the next release, so nobody can diff 148 to 150 [SOURCE, S12].
- ADR 0006 says the patch source is not public. Correction: tags up to 148 hold it. The user rejected the prebuilt engine. A fork of its source is a different choice, so ask the user. The prebuilt 148 stays useful as a detector control in M1. It does not ship.
- fingerprint-chromium released 144 on 27 Feb, 148 on 21 Jun and 150 on 2 Oct 2026 [SOURCE, S12]. One maintainer needs 2 to 4 months between releases. Plan for the same pressure.

## 4. Hardware and build cost

winpc [MEASURED, read-only probe 2026-11-17, S17]:
- CPU i5-10400F, 6 cores, 12 threads. RAM 32 GB (4 x 8 GB DDR4-2667), 22 GB free at probe time. Windows 11 Pro build 26300. Defender real-time on.
- C: NVMe Kingston SNVS500G, 465 GB, 99.7 GB free. D: Seagate ST1000DM010 hard disk, 931 GB, 344 GB free. Page file 2 GB.
- Tools: git 2.45.1, Python 3.10 only, 7-Zip, CMake, Go 1.27.3. No `depot_tools`, ninja or gn. Visual Studio 2022 17.6.4 and SDK 10.0.22000 only.
- Chromium 157 needs Visual Studio 2026 (18.0 or newer), SDK 10.0.28000.2270 and Debugging Tools 10.0.26100.3323 [SOURCE, S1]. UCW needs Python 3.11 or newer [SOURCE, S13].

Build time:
- Google measures a clean `chrome` build at 71 to 75 CPU hours in Nov 2026 (2026 mean 88). In 2018 it was 30 [SOURCE, S2]. Anchor: Ryzen 9 3950X, 53 minutes at 33.3x parallelism, component build, Optane disk, Defender off, 2020 [SOURCE, S3]. That is 29 CPU hours. Google's 2020 figure was 35.
- Scaled to 2026: 59 to 73 CPU hours for the same settings. PassMark: 3950X 38,385, i5-10400F 12,093. winpc is 3.17 times slower [SOURCE, S6]. Result: 5.6 to 6.9 hours.
- Plan for 6 to 8 hours on NVMe with Defender excluded and the PC idle. Plan for 9 to 14 hours on the D: hard disk with the user active [INFERENCE]. Cross-check: a Ryzen 7 2700X needed over 4 hours for a debug `chrome` and Blink tests build in 2024 [SOURCE, S5].
- Incremental build after one `.cc` change: 1 to 5 minutes. After a header change: 10 to 40 minutes [INFERENCE]. M1 measures both.
- UCW builds official with ThinLTO and PGO. Its CI needs about 40 hours of wall time on 4 vCPU [MEASURED, S13]. Do not copy those flags on winpc.
- Dev settings: `is_debug=false`, `is_component_build=true`, `is_official_build=false`, `chrome_pgo_phase=0`, `symbol_level=0`, `blink_symbol_level=0`, `v8_symbol_level=0`. Build target `chrome` only. Use `-j 8` at low priority. Jumbo builds do not exist: Chromium turned them off in 2019 [SOURCE, S3, S4].

Disk and RAM:
- UCW flow (tarball, own clang): about 15 GB source and tools plus 25 to 35 GB output, so about 50 GB [INFERENCE]. The `depot_tools` flow needs 100 GB or more [SOURCE, S1]. ThinLTO needs 120 GB and 31 GB of RAM [MEASURED, S16].
- Build on C: (NVMe). Visual Studio 2026 adds 15 to 25 GB [INFERENCE]. About 25 to 35 GB stays free. Stop if free space drops below 25 GB. Use D: for archives only.
- 32 GB RAM is enough for this component build at `-j 8` when nobody uses the PC [INFERENCE]. It is not enough for ThinLTO. Chromium says 64 GB is not excessive [SOURCE, S1]. The 2 GB page file adds risk.

Rent (read 2026-11-17, USD unless noted, no VAT):

| Service | Machine | Price | Note |
|---|---|---|---|
| GitHub standard Windows runner, public repo | 4 vCPU, 16 GB, 14 GB SSD | 0 | 6 hour job limit. UCW chains 16 jobs. Its x64 run took 39.7 hours (13 to 15 Nov) [S8, S13] |
| GitHub larger Windows runner | 64 vCPU, 256 GB, 2,040 GB | 0.322 per minute (19.32 per hour) | Team plan (4 per user per month) or Enterprise only. Not free for public repos [S7, S8] |
| Azure F64s v2, Windows, East US | 64 vCPU, 128 GB | 5.66 per hour. Spot 1.27. Linux 2.71 | Pay as you go [S9] |
| AWS c7a.16xlarge, Linux | 64 vCPU, 128 GB | 3.28 per hour. Spot 0.92 | Windows surcharge not read [S10] |
| Hetzner AX102 dedicated | Ryzen 9 7950X3D, 32 threads, 128 GB, 2 x 1.92 TB NVMe | EUR 159 per month or EUR 0.415 per hour. EUR 129 setup. No minimum term | Windows Server add-on about EUR 55.90 per month [INFERENCE from table layout] [S11] |

- One non-official build on 64 vCPU takes 1.2 to 1.6 hours [INFERENCE]. It costs 7 to 9 USD on Azure and 23 to 31 USD on a GitHub larger runner. One official build (ThinLTO, PGO) takes 4 to 5 times as long [INFERENCE]. It costs about 30 USD on Azure and about 100 USD on GitHub, and it nears the 6 hour job limit.
- A Linux host can cross-build `chrome` for Windows. Crashpad cannot report crashes, and the Windows SDK must come from a Windows machine [SOURCE, S15].
- Cheapest viable path: winpc for development, the free GitHub runner for release builds, a rented machine only on rebase days [INFERENCE]. No rental, purchase or sign-up happened. No money is approved.

## 5. Milestone 1 (7 working days)

Scope: UCW tag 157.0.8103.78-1.1 plus about 500 to 800 lines. A `--ladilala-seed` switch reaches every renderer. Seeded read-back for canvas (2D, Offscreen, `readPixels`), audio and client rects. Add an `OwnChromium` `BrowserEngine` (about 100 lines of Go) and a `winspike` mode for it. The injector hand-off uses the existing `ModeReal` per surface, so the injector needs no change. Noise is a hash of seed and absolute coordinates. It never calls a random function per read.

Plan: day 1 toolchain and unpatched baseline build. Days 2 to 4 patches. Day 5 tests. Days 6 and 7 soak and report. One extra day is likely [INFERENCE].

| ID | Test | Pass |
|---|---|---|
| M0 | Before any patch: detector matrix for bare Chrome, tier 1 and prebuilt fingerprint-chromium 148. Include fingerprint pages (CreepJS, BrowserScan, pixelscan, browserleaks) | Table saved in the repo |
| A | Baseline and patched builds on winpc | Record wall time, peak RAM, output size. 3 incremental builds each for a `.cc` change and a header change |
| B | CDP pipe: launch with `--remote-debugging-pipe` and an own data dir. The E2E test asserts no `Runtime.enable`. `Browser.getVersion` shows our version | 50 of 50 launches work. Median CDP answer at most 500 ms (stock Chrome 258 ms, CfT 293 ms) |
| C | Canvas: 50 fresh launches, new data dir and seed each. One read at 0 s, three reads at 1 s or later. Add the loop from issue #94 (300 `getImageData`, 100 `readPixels`) | 0 hangs, 0 `Target.targetCrashed`, 0 leaks after 1 s. Same seed gives the same hash after relaunch. Different seeds give at least 49 distinct hashes |
| C+ | Soak: 300 launches on the final build (about 15 minutes at 2.3 s each) | 0 hangs. 0 of 50 only proves under 6 percent (rule of three). 0 of 300 proves under 1 percent |
| D | `stockdetect` on the engine (seed 7, Windows profile) | sannysoft 0 failed. rebrowser all green. CreepJS headless and stealth 0 percent. CreepJS lies: none. BrowserScan Normal |
| E | Same seed gives the same canvas, audio and rect values in page, iframe, dedicated, shared and service worker. A sub-rectangle `getImageData` equals the crop of the full read | 0 differences |
| F | Unzip with our code on winpc, Defender on | No detection, no SmartScreen prompt |

Kill gates:
- G1: the baseline full build takes over 16 hours or runs out of memory. Stop. Choose a capped rental or option (d).
- G2: any hang or crash in test C. Stop. Find the root cause before more patches. Keep PDBs for this build [INFERENCE].
- G3: new CreepJS lies appear, or the audio and rect lies stay. Stop. The engine does not beat tier 1 on its own target.

Done means: A to F pass on one pinned build, the patch series builds from a clean checkout, and the numbers are in `docs/`. M1 ships nothing.

## 6. Risks and unknowns

- Rebase: Chrome ships every 4 weeks. The claimed UA must match the real engine. A lag of more than 2 majors shows in feature tests [INFERENCE]. Budget 3 to 6 days per major, about one fifth of one person.
- Technical limits: `symbol_level=0` gives no crash stacks (issue #94 has only a minidump offset). Seeded, stable values beat random noise, but proof needs M0 and test D. An engine perturbs pixels. It cannot make a GTX 1650 render like another GPU.
- winpc is the user's daily PC and it sleeps. A build blocks it for 6 to 14 hours. One worker uses it at a time (runbook).
- UCW dependency: 20 contributors, releases 2 to 5 days after stable, no signature file [SOURCE, S13]. Patched Chromium likely draws Defender false positives [INFERENCE]. Test F checks once.
- Licence: Bromite files are GPL-3.0 [S14]. Write our noise code from scratch. UCW flags enable proprietary codecs (H.264, AAC). BSD-3 does not cover patent licences. Ask counsel before release.
- Unknowns: real incremental times, fonts patch size on Windows (150 to 540 lines), whether CDP fixes worker `userAgentData`, whether Windows Update interrupts a build.

## 7. Recommendation

Go on M1 with option (a), scoped as (c). Keep tier 1 as the shipping engine and the test control until M1 passes. Do not adopt (b) as a base. Read its 148 tag as a design reference where the licence is clean.

Can a solo 6-core PC sustain this? For development, yes, if the tree sits on NVMe and builds run at night. For release builds, no (RAM). A full rebuild every major is an overnight job. The cheapest path is winpc plus the free GitHub runner, and a rental of about 10 to 30 USD only on a rebase day.

First three actions:

1. Prepare winpc (needs approval, section 8). Install the toolchain. Clone UCW at 157.0.8103.78-1.1 to `C:\ucw`. Run the unpatched build with `NINJA_SUMMARIZE_BUILD=1`. This answers G1.
2. While it builds, run M0 and the tier 1 quick wins: WebRTC flag, `Network.setUserAgentOverride` with metadata on worker sessions, plugins and mediaDevices in the injector.
3. Write the M1 patch design and create the public patch repo with a `series` file. Use fingerprint-chromium patches 000, 003, 012, 013 and 014 at tag 148 as read-only references.

## 8. What the user must approve

1. M1 go decision, and option (a) with (c) first.
2. Installs on winpc with admin rights: Visual Studio 2026 with C++ and ATL/MFC, Windows SDK 10.0.28000.2270 with Debugging Tools, Python 3.11 or newer.
3. A Defender exclusion for `C:\ucw`. A system-managed page file.
4. winpc blocked for 6 to 14 hours at night, with no sleep.
5. A public GitHub repo for the patches. Free CI needs it.
6. Money: none today. Later, a one-off cap (suggest 30 USD) for a cloud build, only after G1 fails or at M2. It needs a card and an account. I created none.
7. SSD: none needed if C: is used. A new SSD is a purchase. No price was checked.
8. Question: is a fork of fingerprint-chromium source acceptable in any form? ADR 0006 rejected the product engine.

## Sources (read 2026-11-17)

S1 https://raw.githubusercontent.com/chromium/chromium/157.0.8103.78/docs/windows_build_instructions.md. S2 https://commondatastorage.googleapis.com/chromium-browser-clang/build-time.html
S3 https://textslashplain.com/2020/02/02/my-new-chromium-build-pc. S4 https://randomascii.wordpress.com/2020/03/30/big-project-build-times-chromium/
S5 https://blog.amoseui.com/chromium-faster-build/
S6 https://www.cpubenchmark.net/cpu.php?cpu=AMD+Ryzen+9+3950X&id=3598 and, for the i5-10400F, https://www.cpubenchmark.net/cpu.php?cpu=Intel+Core+i7-10700T+%40+2.00GHz&id=3792
S7 https://docs.github.com/en/billing/reference/actions-runner-pricing
S8 https://docs.github.com/en/actions/reference/runners/larger-runners, https://docs.github.com/en/actions/reference/runners/github-hosted-runners, https://docs.github.com/en/actions/reference/limits, https://github.com/pricing
S9 https://prices.azure.com/api/retail/prices (armSkuName Standard_F64s_v2, eastus). S10 https://instances.vantage.sh/aws/ec2/c7a.16xlarge. S11 https://www.hetzner.com/dedicated-rootserver/matrix-ax/
S12 https://github.com/adryfish/fingerprint-chromium (README, tags 148.0.7778.215 and 144.0.7559.132, issues #30, #87, #94, #95, #96, #98, #104, #109)
S13 https://github.com/ungoogled-software/ungoogled-chromium-windows (`flags.windows.gn`, README, Actions runs 13 to 15 Nov 2026)
S14 https://github.com/bromite/bromite (GPL-3.0). S15 https://raw.githubusercontent.com/chromium/chromium/main/docs/win_cross.md
S16 Repo and vault: `docs/stock-engine.md`, `docs/canvas-hang.md`, `docs/engine-research-2/*.md`, ADR 0003 to 0007, 0017, 0020, `core/inject`, `core/profile`, `core/driver`
S17 winpc probe over SSH, read only, 2026-11-17. The two probe scripts were deleted afterwards.
