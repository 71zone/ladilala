# Release how-to

Use `scripts/ship.sh` on the release Mac. Details of the pipeline: `docs/release-process.md`.

## One-time setup

1. Signing key. The private key is `~/.ladilala-release/updater.key` (folder mode 700, file mode 600). Keep a second copy offline (password manager or encrypted drive). If the key is lost, no installed copy can accept an update again.
2. Upload token. In the Cloudflare dashboard open R2, then Manage API tokens, then Create API token.
   - Permission: Object Read and Write.
   - Scope: the bucket `ladilala-releases` only.
   - Save the Access Key ID and the Secret Access Key in `~/.ladilala-release/r2.env`. The secret is shown one time.
     ```
     R2_ACCESS_KEY_ID=<access key id>
     R2_SECRET_ACCESS_KEY=<secret access key>
     ```
   - Run `chmod 600 ~/.ladilala-release/r2.env`. The script refuses a file that group or others can read.
3. Tools on the Mac: go, bun, python3, curl, openssl, and `wails3` in `~/.ladilala-release/bin`. winpc needs Go in `C:\tools\go\bin` and NSIS in `%USERPROFILE%\tools\nsis`.

## Normal release

1. `scripts/ship.sh build 1.0.2 "English notes" "Ghi chu tieng Viet"`
   It runs the gates, builds the UI, builds the exe and the setup on winpc, and writes the signed `dist-release/1.0.2/`.
   If winpc is off, the script wakes it and waits up to 120 seconds. It never shuts winpc down.
2. `scripts/ship.sh verify 1.0.2`
3. `scripts/ship.sh publish 1.0.2 --dry-run`, then `scripts/ship.sh publish 1.0.2`
   The script uploads the files first and checks each file at `https://ladilala.71.zone/1.0.2/<file>`. It uploads `manifest.json` last. Then it compares the live manifest with the local file.

`scripts/ship.sh all 1.0.2 "notes" "ghi chu"` runs build, verify, a question, and publish. Add `--yes` to skip the question.
A version string is `X.Y.Z` or `X.Y.Z-name.N`. A version number is never reused. A failed publish can run again: files that are already in the bucket with the same bytes are skipped.
Optional variables for `build`: `MIN_ENGINE`, `NEWS` and `FIXED` (items split by `|`), `CHANNEL`.

## Roll back

`scripts/ship.sh rollback 1.0.1 --yes`
This uploads the manifest of 1.0.1 as the live feed again. The files of 1.0.1 must still be in the bucket. The script checks them first and asks for confirmation (`--yes` skips it).
A rollback points the feed at an older version. Users who already have a newer version get no downgrade, because the updater offers only newer versions. To fix those users, ship a new higher version.

## Verify

- `scripts/ship.sh verify <version>` checks the signature, the SHA-512 of each file, `SHA256SUMS.txt`, and the key. It prints the SHA-256 of the public key and compares it with `app/updater.pub`.
- Check the live feed by hand: `curl -s https://ladilala.71.zone/manifest.json`.

## Troubleshooting

- winpc does not answer: the script sends Wake-on-LAN with `wake-winbox`. If port 22 stays closed after 120 seconds, start winpc by hand and run the command again.
- ssh to winpc fails: run `ssh 8bu@192.168.1.82` and fix the key login. Set `WINPC` if the address changed.
- The build on winpc fails: read the lines above the error. The build runs only in `%USERPROFILE%\ladilala-release`.
- `publish` stops with "exists with different bytes": the version was uploaded before with other bytes. Build a new version number.
- `publish` stops with "live feed serves a newer version": use `rollback` if you want an older version live.
- The live manifest check fails: the edge cache can hold an old copy for up to one minute. Wait, then run `publish` again. It skips what is already uploaded.
- The update check fails on Windows: open the manifest URL in a browser and check that it is valid JSON. Check that each artifact URL gives HTTP 200. Settings can use the feed override only with https or loopback.
- "digest mismatch" in the app log: the file served differs from the manifest. Run `scripts/ship.sh publish <version>` again to compare the served bytes. Never edit an uploaded exe.
- "invalid signature" in the app: the release was signed with a key that is not in `app/updater.pub`. `verify` shows this before you publish.
