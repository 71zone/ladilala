# Macro schema (v1)

A macro is one JSON document, implemented by `core/macro`. `macro.Load` migrates it to the current version, decodes it strictly (unknown fields are errors) and validates it. `macro.Marshal` validates, then writes indented JSON. Samples live in `core/macro/testdata/`.

```jsonc
{
  "schemaVersion": 1,            // required; Migrate upgrades older versions
  "id": "invoice-submit",        // [A-Za-z0-9_-]+
  "name": "Submit invoice",
  "allowedOrigins": ["https://app.example.com", "https://*.example.com"],
  "variables": [ { "name": "user", "type": "string", "required": true, "default": "x" } ],
  "steps":  [ /* Step */ ],
  "guards": [ /* Guard */ ]
}
```

`allowedOrigins` is required: bare `scheme://host[:port]` (http/https), with an optional leading `*.` that matches subdomains only. Static `navigate` URLs must fall inside it; templated ones are checked by the engine at run time (`Macro.AllowsURL`).

Variables: `type` is `string | number | bool | date`; a `date` is ISO-8601 (`2026-01-31` or RFC 3339). A `default` must match the type. Names are identifiers. The optional `kind` (`text email phone number url date password`) is the page field kind the recorder saw. It does not change a run; the data template guide and the editor show it.

## Step

| field | notes |
|---|---|
| `id` | unique, `[A-Za-z0-9_-]+`; used in error paths (`steps[submit].value`) |
| `action` | `navigate click dblclick fill select press check uploadFile wait assert scroll manual` |
| `target` | `{locators, framePath, shadowPath}` |
| `value` | `{expr, transforms}` |
| `commitPoint` | irreversible effect; only on `click`, `press`, `fill` |
| `expect` | post-conditions, see below |
| `timeout` | Go duration string (`"500ms"`, `"30s"`), max 10m |
| `retries` | 0–20 |
| `onFail` | `stop` (default) `skip` `pause` |
| `disabled` | `true` keeps the step in the macro and the player skips it. Default `false` |

Required fields per action:

| action | requires | `value` means |
|---|---|---|
| navigate | value (no target) | URL |
| click | target | – |
| dblclick | target | – |
| fill, select | target, value | text / option; for a date-like input (`date time datetime-local month week`) an ISO value, see below |
| uploadFile | target, value | file path |
| press | value | key chord |
| check | target | optional `"true"`/`"false"` |
| wait | target or `timeout > 0` | – |
| assert | at least one `expect` | – |
| scroll | target or value | `top`, `bottom` or pixel delta |
| manual | value | instruction shown to the human |

**dblclick.** One real double click: the player sends press and release twice with click counts 1 and 2, so the page sees `mousedown, mouseup, click, mousedown, mouseup, click, dblclick` (`detail` 1, 1, 1, 2, 2, 2, 2). Not allowed as a commit point.

**Date-like inputs.** `fill` on an `input` of type `date`, `time`, `datetime-local`, `month` or `week` takes an ISO value: `2026-01-15`, `09:05` or `09:05:30`, `2026-01-15T09:05`, `2026-01`, `2026-W03`. The player sets it through the native value setter, then fires `input` and `change`, and reads it back. A value in another format, or outside the field's `min` or `max`, fails with `ErrBadValue` (no retry) that names the field and the format.

**Target.** `locators` are tried in order; the first matching exactly one element wins. Kinds: `role` (`value` = ARIA role, `name` = accessible name; `name` is only valid here), `label`, `text`, `css`, `xpath`, `testid`. `framePath` lists iframe CSS selectors and `shadowPath` shadow-host CSS selectors, both outermost first.

**Expectation.** `urlMatches` (`value` = regexp), `elementPresent`/`elementAbsent` (`target`), `textContains`/`textEquals`/`valueEquals` (`target` + `value`).

## Guard

Evaluated between steps; fires when its condition holds.

| field | notes |
|---|---|
| `kind` | `urlMatches` (`pattern` regexp), `elementPresent` / `elementAbsent` (`locator`) |
| `action` | `needsLogin` (session expired, ask the user to sign in), `pause`, `fail` |

## Value expressions

`{{name}}` substitutes a declared variable (also in locator `value`/`name`). `macro.Validate` is strict: every reference must be declared, and an undeclared one is a problem. The service is not strict while editing: `SaveMacro` and `ValidateMacro` first call `macro.DeclareUsed`, which adds a text, optional variable for each undeclared reference in first-use order. Import uses the strict check. Inside the braces, `|` pipes the variable through transforms: `{{ name | trim | upper }}`. `value.transforms` apply to the whole rendered string afterwards. The transform set is closed:

| transform | effect |
|---|---|
| `trim` | strip surrounding whitespace |
| `upper`, `lower` | case conversion |
| `digits` | keep only `0-9` |
| `urlencode` | query-escape |
| `date:<layout>` | reformat an ISO-8601 date; tokens `YYYY YY MM DD HH mm ss`, other characters are literal (`date:DD/MM/YYYY`) |

**Literal braces.** A backslash directly before `{{` makes it literal text: `\{{name}}` renders `{{name}}`. This holds in `value.expr` and in locator `value` and `name`. Any other backslash is literal. `macro.EscapeLiteral` escapes a recorded string; the recorder uses it.

## Validation

`macro.Validate` reports every problem at once as `*ValidationError{Problems []Problem{Path, Msg}}`. Paths use the step id: `steps[login].target.locators[0].kind`, `steps[submit].commitPoint`, `guards[1].locator`, `allowedOrigins[2]`. A step with a missing or duplicate id is addressed by index (`steps[2].id`).

## Versioning

`macro.Migrate(raw) ([]byte, error)` runs before decoding. Only v1 exists (`dblclick` and the `\{{` escape were added to it without a version change; older v1 macros load unchanged): it checks the version and returns the bytes untouched; a missing or newer `schemaVersion` is an error. A future v2 adds one rewrite step (v1 → v2) there.
