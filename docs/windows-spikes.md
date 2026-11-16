# Windows spikes (R3, R4, R8)

Run on winpc (Windows, Go 1.27.3) with `LADILALA_E2E=1 go test ./core/driver/ -v`.
Method: repo tarball and a .ps1 script copied with scp, run over ssh; output written to a log and filtered. herdr was not used (higher token cost).

| Spike | Result | Test |
|---|---|---|
| R3 Edge via Launcher | PASS | TestE2EEdge |
| R4 Job Object kills browser tree | PASS | TestE2EJobKillsBrowserOnAppDeath |
| R8 fingerprint-chromium | PASS, with a flake | TestE2EFingerprintChromium |

## R3
Edge 157.0.4341.60: Preflight passed; launch in pipe mode with its own data dir; UA contains `Edg/157`.

## R4
`cmd/winspike` launched Chrome; the test first saw more than one process with the data dir in its command line (browser plus helpers), ran `taskkill /f` on winspike, and found zero such processes within 10 s. `job_windows.go` needed no change. Not covered: a helper spawned before Assign, and Edge or fingerprint-chromium under the same kill.

## R8
Build: adryfish/fingerprint-chromium release 150.0.7871.186 (`--version` and file version both 150.0.7871.186).
Accepted (launched, Preflight passed): `--fingerprint`, `--fingerprint-platform`, `--fingerprint-platform-version`, `--fingerprint-brand`, `--fingerprint-brand-version`, `--fingerprint-hardware-concurrency`, `--timezone`, `--lang`, `--accept-lang`, the WebRTC flags.
Verified effect: hardwareConcurrency followed the flag (16 vs 6). Seeds 111111 and 222222 gave different canvas hashes; the same seed gave the same hash across relaunch.
Not verified per flag: `--timezone`, `--accept-lang`, brand and platform-version values, `--disable-spoofing`, WebRTC policy, proxy. The UA string stayed `Chrome/150.0.0.0` and was not compared with the brand-version flag.

Findings that affect the app:
- **Canvas hang.** About 40% of fingerprint-chromium launches never answer a canvas `toDataURL` read: every read in that browser times out, other CDP calls work. Stock Chrome and Edge never showed it (5 of 5 and 1 of 1). The test closes and relaunches (it needed up to 3 tries). Cause unknown.
- **Unspoofed first read.** The first canvas read in a fresh browser sometimes returned a different, unspoofed image (hash 2c46…); later reads were stable per seed. The test accepts a value only after two equal reads.
- Cases not run: a page with a real origin besides 127.0.0.1, GPU-off, other engine versions.
