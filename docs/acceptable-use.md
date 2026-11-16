# Acceptable use: options

Decision (2026-11-17): the user chose **Option A**, clear terms of use, no technical limit in the app. The text below stays as the record of the options. Option B guards stay a possible later addition. Not legal advice. A lawyer must write the final policy before a public release.

The problem: Ladilala hides the real device and runs many accounts. A shop owner and a fraud farm use the same features. Many sites ban automation or many accounts. The vault rule is no CAPTCHA solving. Open: what else does the product refuse, and who enforces it?

## What similar products do

Pages read on 2026-11-16. Pages change. Check again before you copy wording.

| Product | Position | Source |
|---|---|---|
| GoLogin | Separate Acceptable Use Policy (updated 2026-10-13). Bans fraud, ban evasion, fake engagement, spam, scraping against owner rules, illegal content. Use must follow the law and each site's terms. Allows automation by API, MCP, Selenium, Puppeteer, Playwright | https://gologin.com/acceptable-use-policy/ |
| AdsPower | Terms of Use with a "Prohibited Conduct" list: unlawful acts, harassment, IP infringement, forged identities, fraud, scams, phishing, helping fraud groups | https://www.adspower.com/terms-of-use |
| Multilogin | Terms of Service. No unlawful use. UAE law governs. May refuse or suspend service for any reason | https://multilogin.com/terms-of-service/ |
| Octo Browser | EULA bans unlawful or fraudulent use, collecting other people's personal data, harmful proxy use. Terms allow termination on breach | https://octobrowser.net/eula/ and https://octobrowser.net/terms/ |

Common pattern: terms of use plus account action. [INFERENCE] I read the legal pages only. I did not test the products.

## Option A: clear terms of use

Ship a plain acceptable-use policy. The user accepts it at install.

- Banned: fraud, ban evasion, fake engagement, spam, personal-data theft, CAPTCHA solving, breaking the law.
- The user owns the risk of each site's terms.
- No technical limit in the app.

Consequences: cheapest and the market norm. It gives a legal basis to refuse support. It does not stop abuse. Counsel must write it, and a legal entity must publish it.

## Option B: terms plus built-in guards

Option A, plus limits that make bulk abuse costly.

- `allowedOrigins` review screen on every imported macro (the schema already has the field).
- Default delay between rows and a rate limit per profile and per site.
- Warning when many profiles run one macro on one site.
- A confirm before the first commit point of a run. No CAPTCHA or 2FA bypass: the app pauses and asks the person.

Consequences: shows good faith and fits ADR 0014. It costs build time. A tight limit annoys honest power users. A determined user can change a setting, so guards are speed bumps. With no telemetry we cannot see abuse. Telemetry is a separate open decision.

## Option C: signed macro registry only

The app runs only macros that we or a trusted publisher signed.

Consequences: strongest control, but it removes the main use (record your own task). It needs reviewers, a registry server and a take-down process. [INFERENCE] Few users would accept it. It can work as a "verified" badge on top of A or B.

Build cost: A low, B medium, C high. Stops determined abuse: A no, B partly, C mostly. Fits the recorder goal: A and B yes, C no. [INFERENCE] B fits best. The user decides.

## What the installer must say (all options)

- What the product does, in one sentence.
- The acceptable-use summary and a link to the full text (A and B).
- "Sites may ban accounts that use automation. You carry this risk."
- "No tool can promise that a site will not detect it."
- What is downloaded (WebView2, engines) and from where.

## What the About screen must show

- Version, build, channel, engine versions.
- Links to acceptable use, terms, privacy and `THIRD_PARTY_NOTICES.txt` (`docs/licences.md`).
- How to report abuse, with a real contact address.
- The no-CAPTCHA-solving statement.

Needed before the text is final: company name, jurisdiction, name clearance (ADR 0015), telemetry stance, EULA and privacy policy.

## Checked against

The URLs above (2026-11-16). `docs/threat-model.md` (T-23, T-24). `core/macro/macro.go` (`allowedOrigins`). Vault `product/vision-and-scope.md` and `product/risks.md`.
