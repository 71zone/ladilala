# Data providers (core/data)

The run engine reads rows from a `data.Source`. The contract is in `core/data/source.go`.
Use `data.OpenFile(path)` for a file. It picks the reader from the extension.
Google Sheets is not part of this package. The interface stays open for it.

## Formats

| Format | Constructor | Memory |
|---|---|---|
| CSV, TSV, TXT | `NewCSV` | Streams. Heap does not grow with file size. |
| JSON | `NewJSON` | Whole file in memory. Limit 256 MB. |
| Excel xlsx | `NewXLSX` | Streams the sheet. Zip is opened once. |
| REST | `NewREST` | One page at a time. |

All readers give the same logical rows for the same data. Tests prove this.
Row indexes are 0-based. Error messages use 1-based row numbers (data rows, header excluded).
Text is converted to Unicode NFC, so one Vietnamese letter has one form.

### CSV
- Encoding is detected from the first 64 KB: UTF-8, UTF-8 with BOM, UTF-16 (LE or BE, with or without BOM), Windows-1258, Windows-1252.
- A file that is not valid UTF-8 is Windows-1258 when it has Vietnamese bytes (Đ đ Ơ ơ Ư ư or a tone mark after a letter). Otherwise it is Windows-1252.
- A file whose first 64 KB are plain ASCII but later has 1258 bytes is read as UTF-8. Set `CSVOptions.Encoding` for that case.
- Delimiter: comma, semicolon or tab. The one with the most consistent column count in the first 50 rows wins. Set `CSVOptions.Delimiter` to force one.
- Quoted fields and quoted line breaks work. Header names are trimmed.
- Every value stays text. `070000` keeps its zero.
- Count needs one extra pass over the file. It is cached.

### JSON
- An array of objects, or `{"rows": [...]}`.
- Columns are the union of all keys, in order of first appearance. A missing key gives an empty cell.
- Numbers keep the text from the file. `null` is an empty cell. Booleans are `true` or `false`.
- Object or array values: `NestedReject` (default) gives `NestedValueError` for that row. `NestedJSON` keeps compact JSON text.

### Excel
- Pick the sheet with `Sheet` (name) or `SheetIndex` (0-based). A wrong name lists the real sheet names.
- Row 1 is the header. Empty rows are skipped.
- Number formats stay as text the user sees in Excel: `1,500,000 ₫` and `5%` read like that. Numbers have no trailing `.0`. Text cells are not changed, so `070000` keeps its zero.
- Date cells (built-in formats 14 to 17, 27 to 31, 36, 50 to 58, and custom formats with date tokens and no time tokens) read as ISO `yyyy-mm-dd`. A cell formatted `dd/mm/yyyy` still reads `2026-01-05`, because a web date input needs ISO. Set `XLSXOptions.DateFormat` to `DateDisplay` to get the text as shown in Excel instead. The default is `DateISO`.
- Date-time cells (format 22 and custom formats with both kinds of token) read as `yyyy-mm-ddThh:mm`, the form of `datetime-local`. Seconds are added only when the format shows them or the value has them.
- Time-only cells (formats 18 to 21 and custom time formats) read as `hh:mm`, or `hh:mm:ss` when the format shows seconds. A time stored as text like `1899-12-30T09:15:00Z` reads the same way. Elapsed formats such as `[h]:mm` stay as shown.
- `DateDisplay` changes date-only cells. Date-time and time-only cells keep the ISO forms in both modes.
- The workbook 1900 or 1904 date system is read from the file.
- Formulas read the cached value. When Excel saved none, the reader calculates the formula. If that fails, the cell reads empty and `XLSXSource.Warnings()` returns a `FormulaWarning` with the cell, the formula and the error.
- The sheet is held in memory while rows are read, because cell formats are looked up by cell address.
- Example: `birth_date` reads `1985-01-01` and a time cell reads `09:15`.
- Empty cells at the end of a row are fine. A value past the last header cell is a ragged row.

### REST
- GET only. Headers, bearer or basic auth.
- `Path` is JSONPath-lite: `$.data.items`, `data.items[0].rows`, or empty when the body is the array.
- Paging: `PagingPage` (`page`, `per_page`), `PagingOffset` (`offset`, `limit`), `PagingLink` (next link from `NextPath` in the body, or the `Link` header). Parameter names can be changed.
- Paging stops on an empty page, a short page (when `PageSize` is set), or no next link.
- `MinInterval` sets the pause between requests. `Timeout` is per request (30 s). `MaxRows` defaults to 1,000,000.
- `Count` returns -1. Columns come from the first page.
- Limits: one response up to 64 MB.

## Errors

| Error | Meaning | Message |
|---|---|---|
| `RaggedRowError` | Row cell count differs from the header | `row 3 has 4 cells but the header has 3` |
| `NestedValueError` | JSON object or array in a cell | `row 2, column "b" holds an object or an array; flat values only` |
| `NotObjectError` | JSON row is not an object | `row 5 is not a JSON object` |
| `RowValidationError` | Required column is empty | `row 4: column "email" for variable "Email" is empty` |
| `ErrEmpty` | No header row | `the source is empty: no header row` |

Row errors implement `RowError`. After one, `Next` can be called again and gives the next row.
Any other error ends the cursor. Cancelling the context makes `Next` return `ctx.Err()`.
`Open(from, to)` skips earlier rows without reporting their row errors.

## Mapping and validation
- `Mapping{Vars, Overrides, Required}.Resolve(columns)` matches variables to columns by name, ignoring case and outer spaces.
- `Overrides` gives the column for a variable.
- The report lists `Missing` variables, `Extra` columns and `Duplicates` (same name ignoring case). With a duplicate, the first column wins.
- `RequiredValidator` and `Validated(rows, validator)` turn an empty required value into a `RowValidationError`.

## Security
- Secrets (bearer token, basic password, header values) are never put in error text.
- Errors show scheme, host and path only. The query string is dropped, because it may hold a key.
- Plain HTTP is refused except for loopback hosts. Set `AllowHTTP` to override.
- A redirect to another host is refused. A next link to another host is ignored. Headers stay on one host.
- Store the secrets in the secret store (ADR 0013), not in the macro or the data config.

## Memory measurement
Test `TestCSV200kStreams` reads a 200,000-row CSV (21.9 MB, Vietnamese text, quoted fields).
It forces a GC every 10,000 rows and compares live heap with the heap before the read.
Measured growth: 0.02 MB. The reader holds one row at a time.
Run it with `go test -count=1 -v -run CSV200k ./core/data/`.
