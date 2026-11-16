# Google Sheets as a data source: verification research

Date: 2026-11-17. Answers ADR 0016.

## Question

Does the read-only Sheets scope need Google app verification before Ladilala offers Google Sheets as a data source?
The owner says: "we already have a gws app to read gsheet, check again".

## Short answer

- Verification is NOT needed for Ladilala under option (a) (use the installed `gws`), (b) (user's own OAuth client), (d) (service account) or (e) (published CSV link).
- Verification IS needed under option (c) (Ladilala ships its own OAuth app for all users) with `spreadsheets.readonly`.
- The existing gws app is not a verified app. It is an unverified app that works for the owner only. It cannot serve all Ladilala users.

## Findings

### F1. What gws is
- `gws` 0.22.5 is "Google Workspace CLI". It prints: "This is not an officially supported Google product."
- It is in the `googleworkspace` GitHub org. The README says: "Expect breaking changes as we march toward v1.0."
- It builds its commands at run time from Google Discovery. Sheets helper: `gws sheets +read --spreadsheet ID --range "Sheet1!A1:D10" --format csv`. Formats: json, table, yaml, csv.
- Installed here at /opt/homebrew/bin/gws (npm package `@googleworkspace/cli`). Prebuilt binaries exist on GitHub Releases.
- Source: https://github.com/googleworkspace/cli (README v0.22.5, read 2026-11-17).

### F2. How gws signs in (checked on this Mac)
- Method: `oauth2`, Desktop-app client (`client_secret.json` has an `installed` block). The client belongs to the owner's own Google Cloud project `gws-8bu-cli` (the project id is the one in the local client file).
- Account: ho***@gmail.com (a consumer account, so the client cannot be "Internal").
- Granted scopes (8): drive, presentations, spreadsheets, userinfo.email, userinfo.profile, openid, email, profile. The scopes are read-write and wide. `drive` is a restricted scope.
- Files in ~/.config/gws (paths only): `client_secret.json`, `credentials.enc`, `token_cache.json`, `cache/`. Credentials are AES-256-GCM encrypted. The key is in the OS keyring.
- The README says: a Desktop client in "testing mode" needs you as a test user. gws warns about a 25-scope cap in testing mode.
- Consent mode of the client: I cannot read it from the CLI. [INFERENCE] The client is "In production, unverified". Reason: `credentials.enc` is dated 2026-10-21 and `token_valid` is true on 2026-11-17 (27 days). A Testing-mode refresh token dies after 7 days (F4). Only the owner can confirm this in the Cloud Console (Google Auth platform, Audience).
- I ran only `gws --help`, `gws auth status`, and help for Sheets. I read no spreadsheet and changed nothing.

### F3. Scope classes
Official Sheets scope table (https://developers.google.com/workspace/sheets/api/scopes, read 2026-11-17):

| Scope | Class |
|---|---|
| spreadsheets | Sensitive |
| spreadsheets.readonly | Sensitive |
| drive.file | Non-sensitive (recommended) |
| drive, drive.readonly | Restricted |

- So `spreadsheets.readonly` is sensitive, not restricted.
- Sensitive scope: Google review needed. No security assessment. Restricted scope: also a yearly paid security assessment by a Google-approved assessor (https://support.google.com/cloud/answer/13464321). We never need restricted.

### F4. Unverified app rules
Sources: https://support.google.com/cloud/answer/15549945 and https://support.google.com/cloud/answer/7454865 (read 2026-11-17).
- Testing status: up to 100 test users, each added by hand in the Cloud Console.
- Testing status: a test user sees a warning screen. Authorization, and the refresh token, expire 7 days after consent. The only exception is the name, email and profile scopes.
- In production but unverified, with a sensitive scope: the "Google hasn't verified this app" screen shows. A cap of 100 new users applies for the whole life of the project. It cannot be reset.
- Verification is not needed for personal use, a few known users, development or testing, service accounts that access only the app's own data, and Internal Workspace apps (https://developers.google.com/identity/protocols/oauth2/production-readiness/sensitive-scope-verification).
- A Desktop client with a user-supplied project is a normal case: the user is the developer of their own app. The user clicks through the warning. No verification is needed because the app is "personal use". [INFERENCE] from the exceptions list; Google does not name this case.
- Desktop apps use the system browser, a loopback redirect `http://127.0.0.1:port`, and PKCE. The client secret is not secret (https://developers.google.com/identity/protocols/oauth2/native-app).

### F5. Verification for a sensitive scope (option c)
Source: sensitive-scope-verification page above and https://support.google.com/cloud/answer/13464321.
- Needed: a public home page on a domain we own, verified in Google Search Console; a privacy policy on that domain, linked on the consent screen; branding check (automatic, minutes); a written reason why a narrower scope does not work; a demo video on YouTube (unlisted) with the full OAuth flow in English; Limited Use compliance.
- Cost: no fee for sensitive scopes. [INFERENCE] Domain and hosting cost is ours.
- Time: Google says "can take up to 10 days". Another Google page says "several months" can be needed (https://support.google.com/cloud/answer/7454865). Plan for 2 to 8 weeks. [INFERENCE]
- Risk: any change to scopes, redirect URLs or product name needs a new review. Ladilala is a rename candidate (see docs/naming.md), so freeze the name first.
- Appropriate use: Google reviews whether the use case is acceptable. A macro tool that reads rows for form filling is a productivity use. [INFERENCE] likely accepted, not guaranteed.

### F6. drive.file with the Picker
Source: https://developers.google.com/drive/picker/guides/desktop-mobile-picker (read 2026-11-17).
- Yes, the desktop Picker exists. The app opens the OAuth URL in the default browser with `scope=drive.file`, `prompt=consent`, `trigger_onepick=true`. The redirect brings back `picked_file_ids` and a `code`. A loopback URL is allowed as redirect (the Picker "imposes no additional restrictions").
- `drive.file` cannot be combined with any other scope there.
- The Sheets API accepts `drive.file`, so we can call `values.get` for the picked file.
- `drive.file` is non-sensitive. It needs brand verification only (home page, privacy policy, domain). [INFERENCE] Google says non-sensitive scopes "only require basic app verification". We would still need our own OAuth client and a domain.
- The user must pick again for each new spreadsheet. A file that is picked once stays granted to the app.
- A Go app can do it: open the URL, run a local HTTP listener, exchange the code with PKCE.

## Options

| Option | User effort | Cost | Security | Build in `core/data` | Verification? | Main risk |
|---|---|---|---|---|---|---|
| (a) `gws` bridge | Install gws, run `gws auth login` once. High for non-technical users (Cloud project needed, see F2). | 0 | Token stays in gws keyring. Ladilala holds no token. gws scope is wide (read-write). | `GWSSource` runs `gws sheets +read --format csv` and reads the CSV. Reuse `NewCSV` for the parse. | No | Unofficial, breaking changes, not on every PC, needs Node or a binary, Windows keyring behavior untested |
| (b) Bring your own client | Create project, enable Sheets API, make a Desktop client, paste client id. High. | 0 | Ladilala stores the refresh token (DPAPI on Windows). Scope `spreadsheets.readonly` only. | OAuth loopback+PKCE flow, token store, `SheetsSource` over `values.get` (REST reader can be reused for paging). | No | Setup is hard. Testing mode kills the token every 7 days; the user must publish the app to production. |
| (c) Ladilala OAuth app | One click and one consent. Lowest. | Domain, hosting, review time, video work | Ladilala stores token (DPAPI). Embedded client id is public. | Same code as (b), with a built-in client id. Plus website and privacy policy. | YES, sensitive scope | Weeks of delay, re-review after changes, 100-user cap until approved |
| (c2) Own app with `drive.file` and Picker | One click, pick the file in the browser. | Domain, hosting | Narrow: only picked files. | Same as (c) plus the Picker URL and a file list store. | Brand verification only (no sensitive review) | Pick step for each sheet. Picker flow is newer and may change. |
| (d) Service account | Make a project and a key, share the sheet to the account email. High. | 0 | A key file on disk. Treat as a secret (DPAPI). | JWT sign, token exchange, `SheetsSource`. | No | The user shares private data with a robot account. Hard for non-technical users. |
| (e) Published CSV or xlsx export URL | Paste a link. Sheet must be public or "anyone with the link". | 0 | No auth. The data is public to anyone with the link. | `NewREST` or `NewCSV` over HTTP GET. `.../export?format=xlsx` feeds `NewXLSX`. | No | Private sheets do not work. Link leaks data. |

## Recommendation

1. Now (smallest work, no verification): ship (e) first. It needs no OAuth and reuses `NewCSV` and `NewXLSX`. Add (a) as an optional "advanced" bridge for users who already have `gws`. Both need no Google review.
2. Next: build (c2), the `drive.file` Picker flow, on a Ladilala OAuth client. It gives private-sheet access with one click and avoids the sensitive-scope review. It needs only a domain and a privacy policy.
3. Do not start with (c) with `spreadsheets.readonly`. The extra review is not worth it when `drive.file` covers the need.
4. Do not offer (b) as the main path. Testing-mode tokens expire in 7 days and project setup is too hard for the Vietnamese-first audience. Keep it as a power-user fallback only if (a) is rejected.
5. The existing gws app does not remove the need for verification for public release. It works for the owner because the owner is the developer of that client.

Answer to ADR 0016: Ladilala needs Google verification only if it ships its own OAuth app with `spreadsheets.readonly` (option c). It needs brand verification only if it ships its own app with `drive.file` (c2). It needs none for (a), (b), (d), (e).

## What to build in `core/data`

The Source contract is in `core/data/source.go` (see docs/data-providers.md). Google Sheets stays outside the package core; each provider is a new constructor.
- `NewGoogleSheetCSV(url)`: fetch a published CSV or xlsx export link, then hand the stream to `NewCSV` or `NewXLSX`. Same logical rows, same NFC rule. (option e)
- `NewGWS(spreadsheetID, range)`: run `gws sheets +read ... --format csv` with a timeout, feed `NewCSV`. Detect a missing `gws` and return a clear error. Never put tokens in error text. (option a)
- `NewSheetsAPI(token, id, range)`: `values.get` with `valueRenderOption=FORMATTED_VALUE` so numbers look like in Sheets, as in the Excel reader. All values stay text. (options b, c, c2)
- Token store and loopback+PKCE flow in a separate package, DPAPI for the refresh token on Windows. Not in `core/data`.
- Tests: a fake `gws` script, an `httptest` server for the CSV link and the Sheets API, and the cross-format row equality test.
- Add the new rows to the format table in docs/data-providers.md and add the errors (sheet not shared, sign-in expired).

## Open questions

1. What is the publishing status of the `gws-8bu-cli` consent screen (Testing or In production)? Only the owner can see it. See F2.
2. Is the Ladilala name final? A rename after verification forces a new review (F5).
3. Do we own a domain for the home page and privacy policy? Needed for (c) and (c2).
4. Is a Picker step for every spreadsheet acceptable to users? If not, (c) with `spreadsheets.readonly` is the only one-consent route for all sheets.
5. Does `gws` work with the Windows keyring when called from a Wails app? Not tested on winpc.
6. Do we accept `gws` as a hard dependency for non-technical users? Probably not; keep it optional.
