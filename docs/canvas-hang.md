# Canvas hang and first-read leak in fingerprint-chromium

Measured on winpc (NVIDIA GPU, Windows) with `winspike canvasmatrix` (20 fresh launches per config, new data dir and seed each). Raw logs are JSON lines; harness in `cmd/winspike/canvas.go`, probes in `core/driver/canvasprobe.go`.
Columns: hang = `toDataURL` unanswered after 3 s; leak = first read equals the unspoofed image (from launches with `--disable-spoofing=canvas`); unstable = first read differs from the second.

## Results (engine 150.0.7871.186 unless noted)

| Config | hang | leak | unstable |
|---|---|---|---|
| baseline (http page, read right after load) | 3 | 9 | 9 |
| --disable-gpu | 2 | 0 | 0 |
| --disable-gpu-compositing | 6 | 0 | 0 |
| --use-angle=swiftshader | 1 | 0 | 19 (own real hash, see below) |
| --use-angle=d3d11 | 3 | 6 | 6 |
| --use-angle=gl | 8 | 0 | 12 |
| page about:blank / data: URL / initial tab, no wait | 0 | 20 | 20 |
| http, wait 300 ms / 1 s / 3 s | 7 / 1 / 1 | 0 | 0 |
| about:blank, wait 1 s | 14 | 0 | 0 |
| initial tab, wait 1 s | 11 | 0 | 0 |
| new tab read at once (no wait / wait 1 s) | 4 / 4 | 8 / 0 | 8 / 0 |
| control `--disable-spoofing=canvas` | 0 | (real) | 0 |
| 148.0.7778.215 baseline / wait 1 s / --disable-gpu | 0 / 0 / 0 | 2 / 0 / 0 | 3 / 0 / 0 |
| 144.0.7559.132 baseline / wait 1 s / --disable-gpu | 0 / 0 / 0 | 7 / 0 / 0 | 9 / 0 / 0 |
| stock ungoogled-chromium 150 (same base, no patches) baseline / wait 1 s | 0 / 0 | n/a | 13 / 0 |

Hang totals: 150 patched 0-70% by config; 148 and 144: 0 of 100 launches each; stock 0 of 80.
Relaunch with the same seed gave the same first hash in every leak-free config (relaunchdiff 0).

## Root cause (best evidence, not proven)
- **Hang:** introduced by the 150 patch set. Stock 150 (same Chromium base) never hangs, 148 and 144 never hang, `--disable-spoofing=canvas` never hangs. It is not GPU-related (it also occurs with `--disable-gpu`) and not page-related; it is worse on idle blank pages after a wait. Treat as a bug in the 150 canvas patch.
- **First-read leak:** a read made right after launch can run before the engine's canvas spoof is active and returns the real image. Reads at 1 s or later never leaked (0 of 80 launches on 150, 148, 144), including in a second tab opened after the wait. A leaked first read is always followed by a different later read, so an unstable read is a reliable detector.
- The real image itself has two values on this machine (2c46... on very early reads, c74d... later); stock Chromium shows the same switch, so part of the "instability" is base behaviour, not the patches. `--use-angle=swiftshader` has its own real hash (3268...).

## Fix (core/driver/canvasverify.go)
- `Launcher.LaunchVerified` waits `SettleDelay` (1 s), then `VerifySpoof` reads the canvas 3 times (3 s limit each). The reads must be identical and not a known real hash. A hang or leak closes the browser and relaunches, at most `MaxVerifyAttempts` (4) launches. It never returns an unverified session.
- Cost: about 2.3 s per launch on 148/144.
- Engine choice: **pin fingerprint-chromium 148 or 144** until the 150 hang is fixed. On 150 the retry works but 52% of launches hang (22 of 42 attempts), so a launch takes about 10 s on average and would fail outright about 6% of the time if hangs were independent (inferred: 0.52^4).

## Acceptance (`TestE2ECanvasVerified`, 20 consecutive fresh launches, LADILALA_E2E=1)
| Engine | attempts | raw hangs | raw leaks | unrecovered | later-read leaks |
|---|---|---|---|---|---|
| 148 | 20 | 0 | 0 | 0 | 0 |
| 144 | 20 | 0 | 0 | 0 | 0 |
| 150 (two runs) | 42, 45 | 22, 25 | 0 | 0, 0 | 0 |

`TestE2EFingerprintChromium` on 150 failed twice: it failed after about 3 minutes in the second profile launch; the log was not inspected, so the cause is inferred to be retries running out at the high hang rate. It passes on 148 and 144.

## Unverified
- Leak rate with the settle delay is 0 of 80 launches; the true rate is not shown to be 0. A page that reads canvas inside the first second of a navigation in a new renderer was only tested once (new tab, 20 launches).
- Hang behaviour on other GPUs or drivers, and on other 150 builds.
- WebGL, audio and font spoofing were not checked; `--disable-gpu` was not adopted because it also changes the WebGL surface.
- The 150 first-read leak under real user navigation timing.
