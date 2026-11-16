#!/usr/bin/env bash
# Builds the signed release folder for one version on the Mac. It does NOT upload anything.
#   scripts/release.sh <version> <app.exe> <setup.exe> [notes-en] [notes-vi]
# <app.exe>   the plain ladilala.exe built with scripts/build-windows.ps1 (the in-app updater swaps this file)
# <setup.exe> the NSIS installer from the same build (for first installs, not used by the updater)
# notes-en, notes-vi: a text string or the path of a text file (optional)
# Environment: CHANNEL (default stable), MIN_ENGINE (lowest stock Chrome major, default 0 = none),
#   URL_BASE (default https://ladilala.71.zone), KEY (default ~/.ladilala-release/updater.key),
#   WAILS3 (default ~/.ladilala-release/bin/wails3, see docs/release-process.md), NEWS and FIXED (items split by "|").
set -euo pipefail

if [ $# -lt 3 ]; then sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'; exit 2; fi
version="$1"; app_exe="$2"; setup_exe="$3"; notes_en="${4:-}"; notes_vi="${5:-}"
channel="${CHANNEL:-stable}"
min_engine="${MIN_ENGINE:-0}"
url_base="${URL_BASE:-https://ladilala.71.zone}"
key="${KEY:-$HOME/.ladilala-release/updater.key}"
wails3="${WAILS3:-$HOME/.ladilala-release/bin/wails3}"
root="$(cd "$(dirname "$0")/.." && pwd)"
pub="$root/app/updater.pub"

[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || { echo "version must be semver, got: $version" >&2; exit 1; }
[[ "$min_engine" =~ ^[0-9]+$ ]] || { echo "MIN_ENGINE must be a number" >&2; exit 1; }
for f in "$app_exe" "$setup_exe" "$key" "$wails3" "$pub"; do [ -f "$f" ] || { echo "missing file: $f" >&2; exit 1; }; done

out="$root/dist-release/$version"
if [ -e "$out" ]; then echo "$out exists. A version number is never reused: pick a new version or delete the folder by hand." >&2; exit 1; fi
mkdir -p "$out"
exe_name="ladilala-$version-windows-amd64.exe"
setup_name="ladilala-$version-windows-amd64-setup.exe"
cp "$app_exe" "$out/$exe_name"
cp "$setup_exe" "$out/$setup_name"

text() { if [ -f "$1" ]; then cat "$1"; else printf '%s' "$1"; fi; }
notes=""
[ -n "$notes_en" ] && notes="$(text "$notes_en")"
[ -n "$notes_vi" ] && notes="${notes:+$notes

---

}$(text "$notes_vi")"

# Only the plain exe is an update artifact. The filename carries windows and amd64 so the updater matches it.
"$wails3" updater manifest -version "$version" -channel "$channel" -name "Ladilala $version" \
  -notes "$notes" -key "$key" -url-prefix "$url_base/$version" -output "$out/manifest.json" "$out/$exe_name"

# The Wails CLI writes no metadata. The app reads minEngine, news and fixed from it (releaseOf in app/appupdate_service.go).
python3 - "$out/manifest.json" "$min_engine" "${NEWS:-}" "${FIXED:-}" <<'PY'
import json, sys
p, me, news, fixed = sys.argv[1:5]
m = json.load(open(p))
split = lambda s: [x.strip() for x in s.split("|") if x.strip()]
m["metadata"] = {"minEngine": int(me), "news": split(news), "fixed": split(fixed)}
json.dump(m, open(p, "w"), indent=2); open(p, "a").write("\n")
PY

"$wails3" updater verify -manifest "$out/manifest.json" -publickey "$pub" -dir "$out"
( cd "$out" && shasum -a 256 "$exe_name" "$setup_name" manifest.json > SHA256SUMS.txt )

cat <<MSG

Release folder: $out
  $exe_name          (update artifact, listed in the manifest)
  $setup_name   (first install, linked from the website)
  manifest.json                              (publish LAST, as the object key manifest.json at the bucket root)
  SHA256SUMS.txt                             (do not upload, for your records)

Upload order (bucket ladilala-releases, nothing is uploaded by this script):
  1. $version/$exe_name
  2. $version/$setup_name
  3. manifest.json   <- from $version/manifest.json, last and in one put
Rollback: upload the manifest.json of the older version again. See docs/release-process.md.
MSG
