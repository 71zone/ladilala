#!/usr/bin/env bash
# One entry point for a Ladilala release, run on the Mac. See docs/release-howto.md.
#   scripts/ship.sh build <version> [notes-en] [notes-vi]   gates, UI, build on winpc, signed dist-release/<version>/
#   scripts/ship.sh verify <version>                        check the signed folder, the key and the digests
#   scripts/ship.sh publish <version> [--dry-run]           upload to R2: files first, manifest last
#   scripts/ship.sh rollback <version> [--yes] [--dry-run]  point the live feed at an older, already uploaded version
#   scripts/ship.sh all <version> [notes-en] [notes-vi] [--yes] [--dry-run]   build, verify, ask, publish
#   scripts/ship.sh help
# Environment (all optional): WINPC (default 8bu@192.168.1.82), URL_BASE (default https://ladilala.71.zone),
#   R2_ENV (default ~/.ladilala-release/r2.env), R2_ENDPOINT, R2_BUCKET (default ladilala-releases),
#   KEY and WAILS3 (as in scripts/release.sh), CHANNEL, MIN_ENGINE, NEWS, FIXED (passed to scripts/release.sh).
set -euo pipefail
umask 077

root="$(cd "$(dirname "$0")/.." && pwd)"
dist="$root/dist-release"
pub="$root/app/updater.pub"
key="${KEY:-$HOME/.ladilala-release/updater.key}"
wails3="${WAILS3:-$HOME/.ladilala-release/bin/wails3}"
winpc="${WINPC:-8bu@192.168.1.82}"
url_base="${URL_BASE:-https://ladilala.71.zone}"
r2_env="${R2_ENV:-$HOME/.ladilala-release/r2.env}"
r2_bucket="${R2_BUCKET:-ladilala-releases}"
r2_endpoint="${R2_ENDPOINT:-https://d3fc0198c8197514b262b12e3c6639a4.r2.cloudflarestorage.com}"
version_re='^[0-9]+\.[0-9]+\.[0-9]+(-[a-z]+\.[0-9]+)?$'

YES=0
DRY=0
TMP="$(mktemp -d "${TMPDIR:-/tmp}/ship.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

die() { echo "ship: $*" >&2; exit 1; }
step() { printf '\n==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
usage() { sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'; }

need() { local c; for c in "$@"; do command -v "$c" >/dev/null 2>&1 || die "command not found: $c"; done; }

check_version() {
  [[ "$1" =~ $version_re ]] || die "bad version '$1': use X.Y.Z or X.Y.Z-name.N (for example 1.0.2 or 1.1.0-beta.1)"
}

confirm() {
  [ "$YES" = 1 ] && return 0
  [ -t 0 ] || die "confirmation needed but no terminal: run it in a terminal or pass --yes"
  local a; read -r -p "$1 [y/N] " a
  [ "$a" = y ] || [ "$a" = Y ] || [ "$a" = yes ] || die "cancelled"
}

# Hard time limit for a command. macOS has no timeout(1), so perl sets an alarm and replaces itself with the command.
hard_timeout() { local secs="$1"; shift; perl -e 'alarm shift @ARGV; exec @ARGV or die "exec failed: $!\n"' "$secs" "$@"; }

# Runs a command and hides the linker version warnings that cgo builds print on macOS. The exit code is the command's.
quiet_ld() { { "$@" 2>&1; } | { grep -v '^ld: warning: object file' || true; }; }

sha256_of() { shasum -a 256 "$1" | awk '{print $1}'; }
sha512_b64() { openssl dgst -sha512 -binary "$1" | base64 | tr -d '\n'; }
md5_of() { openssl dgst -md5 "$1" | awk '{print $NF}'; }
size_of() { wc -c <"$1" | tr -d ' '; }

hdr_len() { tr -d '\r' <"$1" | awk -F': *' 'tolower($1)=="content-length"{v=$2} END{print v}'; }

# ---------------------------------------------------------------- manifest helpers

# Prints one tab separated row per artifact: name, size, digest (base64 sha512), url. Fails on a bad manifest.
manifest_rows() {
  python3 - "$1" "$2" "$url_base" <<'PY'
import json, sys
path, ver, base = sys.argv[1:4]
m = json.load(open(path))
if m.get("schemaVersion") != 1: sys.exit("manifest: schemaVersion is not 1")
if m.get("version") != ver: sys.exit("manifest: version is %r, expected %r" % (m.get("version"), ver))
arts = m.get("artifacts") or []
if not arts: sys.exit("manifest: no artifacts")
prefix = base + "/" + ver + "/"
for a in arts:
    if a.get("digestAlgo") != "sha512": sys.exit("manifest: digestAlgo is not sha512")
    if a.get("platform") != "windows" or a.get("arch") != "amd64": sys.exit("manifest: artifact is not windows amd64")
    url = a.get("url", "")
    if not url.startswith(prefix): sys.exit("manifest: url %r does not start with %s" % (url, prefix))
    name = url[len(prefix):]
    if not name or "/" in name: sys.exit("manifest: bad artifact name in %r" % url)
    print("\t".join([name, str(a["size"]), a["digest"], url]))
PY
}

json_version() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$1"; }

# Prints 1 when version $1 is newer than $2 (semver, a pre-release is older than its release), else 0.
semver_gt() {
  python3 - "$1" "$2" <<'PY'
import sys
def key(v):
    core, _, pre = v.partition("-")
    c = tuple(int(x) for x in core.split("."))
    p = tuple((0, int(x), "") if x.isdigit() else (1, 0, x) for x in pre.split(".")) if pre else None
    return c, p
a, b = key(sys.argv[1]), key(sys.argv[2])
if a[0] != b[0]: print(int(a[0] > b[0]))
elif a[1] is None or b[1] is None: print(int(a[1] is None and b[1] is not None))
else: print(int(a[1] > b[1]))
PY
}

# Version served by the live feed, or empty when it cannot be read.
live_version() {
  if curl -fsS -m 20 -H 'Cache-Control: no-cache' -o "$TMP/live.json" "$url_base/manifest.json" 2>/dev/null; then
    json_version "$TMP/live.json" 2>/dev/null || true
  fi
}

# ---------------------------------------------------------------- verify

# verify_release <version>: sets ROWS (artifact rows) and checks the folder end to end.
verify_release() {
  local v="$1" d="$dist/$1" name size digest url fp_key fp_pub src f
  [ -d "$d" ] || die "$d does not exist: run 'scripts/ship.sh build $v' first"
  need openssl python3 shasum
  for f in "$d/manifest.json" "$d/SHA256SUMS.txt" "$pub" "$wails3"; do [ -f "$f" ] || die "missing file: $f"; done

  step "Verify $v: manifest, signature and digests"
  "$wails3" updater verify -manifest "$d/manifest.json" -publickey "$pub" -dir "$d" || die "wails3 updater verify failed for $v"
  ROWS="$(manifest_rows "$d/manifest.json" "$v")" || die "manifest check failed for $v"
  while IFS=$'\t' read -r name size digest url; do
    [ -f "$d/$name" ] || die "artifact $name is listed in the manifest but missing in $d"
    [ "$(size_of "$d/$name")" = "$size" ] || die "$name: size differs from the manifest ($size)"
    [ "$(sha512_b64 "$d/$name")" = "$digest" ] || die "$name: SHA-512 differs from the manifest"
    info "$name: size $size and SHA-512 match the manifest"
  done <<<"$ROWS"
  [ -f "$d/ladilala-$v-windows-amd64-setup.exe" ] || die "setup file missing: $d/ladilala-$v-windows-amd64-setup.exe"
  (cd "$d" && shasum -a 256 -c SHA256SUMS.txt >/dev/null 2>&1) || die "SHA256SUMS.txt does not match the files in $d"
  info "SHA256SUMS.txt matches the files"

  # The key that signs must be the key pinned in the app. The public half is derived from the private key.
  src="the private key"
  fp_key="$(openssl pkey -in "$key" -pubout 2>/dev/null | shasum -a 256 | awk '{print $1}')" || fp_key=""
  if [ -z "$fp_key" ] || [ "$fp_key" = "$(printf '' | shasum -a 256 | awk '{print $1}')" ]; then
    [ -f "$key.pub" ] || die "cannot derive the public key from $key and $key.pub is missing"
    fp_key="$(sha256_of "$key.pub")"; src="$key.pub"
  fi
  fp_pub="$(sha256_of "$pub")"
  info "public key SHA-256 from $src: $fp_key"
  info "public key SHA-256 of app/updater.pub: $fp_pub"
  [ "$fp_key" = "$fp_pub" ] || die "the signing key does not match app/updater.pub: installed apps would reject this release"
  info "verify OK"
}

# ---------------------------------------------------------------- R2

r2_manual_help() {
  cat >&2 <<MSG
ship: $r2_env not found. No automatic upload is possible.

Manual publish for $1 (Cloudflare dashboard, R2, bucket $r2_bucket):
  1. Create the folder $1/ and upload the .exe files of dist-release/$1/ into it.
  2. Open each file at $url_base/$1/<file> and check the size.
  3. Upload dist-release/$1/manifest.json to the bucket root as manifest.json. Do this last.

One-time setup for automatic upload:
  1. Dashboard > R2 > Manage API tokens > Create API token.
  2. Permission: Object Read and Write. Scope: the bucket $r2_bucket only.
  3. Save the Access Key ID and the Secret Access Key (shown once) in $r2_env:
       R2_ACCESS_KEY_ID=<access key id>
       R2_SECRET_ACCESS_KEY=<secret access key>
  4. chmod 600 $r2_env
MSG
  exit 2
}

# Reads the two R2 variables from r2.env without sourcing it and writes a curl config that holds the credentials.
load_r2() {
  local mode id secret
  [ -f "$r2_env" ] || return 1
  mode="$(stat -f %Lp "$r2_env" 2>/dev/null || stat -c %a "$r2_env")"
  [ $(( 8#$mode & 077 )) -eq 0 ] || die "$r2_env is readable by group or others (mode $mode): run chmod 600 $r2_env"
  r2_var() { sed -n -E "s/^[[:space:]]*(export[[:space:]]+)?$1=(.*)$/\\2/p" "$r2_env" | tail -n 1 | tr -d '\r' \
    | sed -E "s/^[\"']//; s/[\"'][[:space:]]*\$//; s/[[:space:]]+\$//"; }
  id="$(r2_var R2_ACCESS_KEY_ID)"; secret="$(r2_var R2_SECRET_ACCESS_KEY)"
  [ -n "$id" ] || die "R2_ACCESS_KEY_ID is missing in $r2_env"
  [ -n "$secret" ] || die "R2_SECRET_ACCESS_KEY is missing in $r2_env"
  [[ "$id" =~ ^[A-Za-z0-9/+=_-]+$ && "$secret" =~ ^[A-Za-z0-9/+=_-]+$ ]] || die "$r2_env: a value has characters that a token never has"
  CURL_CFG="$TMP/curl.cfg"
  printf 'user = "%s:%s"\n' "$id" "$secret" >"$CURL_CFG"
  chmod 600 "$CURL_CFG"
  unset id secret
}

s3_url() { printf '%s/%s/%s' "$r2_endpoint" "$r2_bucket" "$1"; }
s3_curl() { curl -sS --aws-sigv4 "aws:amz:auto:s3" -K "$CURL_CFG" -H "x-amz-content-sha256: UNSIGNED-PAYLOAD" "$@"; }

# Prints the MD5 ETag of an object, or nothing when the object does not exist.
s3_etag() {
  local code
  code="$(s3_curl -I --retry 3 -o "$TMP/h" -w '%{http_code}' "$(s3_url "$1")")" || die "R2 request failed for $1 (network or endpoint)"
  case "$code" in
    200) tr -d '\r' <"$TMP/h" | awk -F': *' 'tolower($1)=="etag"{gsub(/"/,"",$2); print $2}' ;;
    404) ;;
    401|403) die "R2 refused the credentials (HTTP $code): check the token scope in $r2_env" ;;
    *) die "R2 HEAD $1 returned HTTP $code" ;;
  esac
}

# s3_put <key> <file> <content-type> <cache-control>
s3_put() {
  local code=""
  code="$(s3_curl --fail --retry 3 -o /dev/null -w '%{http_code}' -T "$2" -H "Content-Type: $3" -H "Cache-Control: $4" "$(s3_url "$1")")" \
    || die "upload of $1 failed (HTTP ${code:-none})"
}

ctype_of() { case "$1" in *.json) echo application/json ;; *) echo application/octet-stream ;; esac; }

# upload_one <key> <file> <cache-control> <immutable 1|0>
upload_one() {
  local etag
  etag="$(s3_etag "$1")"
  if [ -n "$etag" ] && [ "$etag" = "$(md5_of "$2")" ]; then info "$1: already in the bucket with the same bytes, skipped"; return 0; fi
  if [ -n "$etag" ] && [ "$4" = 1 ]; then
    die "$1 exists in the bucket with different bytes. A version number is never reused: build a new version"
  fi
  info "$1: uploading $(size_of "$2") bytes"
  s3_put "$1" "$2" "$(ctype_of "$1")" "$3"
}

# check_public <version> <name> <size> <sha512 base64>: the file must be served with that length and digest.
check_public() {
  local url="$url_base/$1/$2" i code len got why="no answer"
  for i in 1 2 3 4 5 6; do
    code="$(curl -sSI -m 30 -o "$TMP/h" -w '%{http_code}' "$url" 2>/dev/null)" || code=000
    if [ "$code" = 200 ]; then
      len="$(hdr_len "$TMP/h")"
      if [ "$len" = "$3" ]; then
        code="$(curl -sS -m 900 -o "$TMP/dl" -w '%{http_code}' "$url" 2>/dev/null)" || code=000
        if [ "$code" = 200 ]; then
          got="$(sha512_b64 "$TMP/dl")"; rm -f "$TMP/dl"
          [ "$got" = "$4" ] && { info "$url: HTTP 200, length $len, SHA-512 matches"; return 0; }
          why="SHA-512 differs from the expected value"
        else why="GET returned HTTP $code"; fi
      else why="Content-Length is '${len:-none}', expected $3"; fi
    else why="HTTP $code"; fi
    [ "$i" = 6 ] || sleep 5
  done
  die "$url is not served correctly: $why"
}

# check_live_manifest <file>: the feed must serve exactly this file.
check_live_manifest() {
  local want got i
  want="$(sha256_of "$1")"
  for i in 1 2 3 4 5 6; do
    if curl -fsS -m 30 -H 'Cache-Control: no-cache' -o "$TMP/live.json" "$url_base/manifest.json" 2>/dev/null; then
      got="$(sha256_of "$TMP/live.json")"
      [ "$got" = "$want" ] && { info "live $url_base/manifest.json matches the local file (SHA-256 $want)"; return 0; }
    fi
    [ "$i" = 6 ] || sleep 5
  done
  die "the live feed does not serve the new manifest (local SHA-256 $want, live ${got:-unreadable}): the edge cache may be old, check again in one minute"
}

public_state() { # dry run: report what the public URL says now
  local code
  code="$(curl -sSI -m 15 -o "$TMP/h" -w '%{http_code}' "$1" 2>/dev/null)" || code=000
  if [ "$code" = 200 ]; then echo "already served, length $(hdr_len "$TMP/h")"; else echo "not served (HTTP $code)"; fi
}

# ---------------------------------------------------------------- publish, rollback

publish_cmd() {
  local v="$1" d="$dist/$1" name size digest url files live cc_files="public, max-age=31536000, immutable" cc_feed="public, max-age=60"
  need curl python3 openssl shasum
  verify_release "$v"

  files=""
  while IFS=$'\t' read -r name size digest url; do files="$files$name"$'\n'; done <<<"$ROWS"
  for f in "$d"/*.exe; do
    name="$(basename "$f")"
    case "$files" in *"$name"$'\n'*) ;; *) files="$files$name"$'\n' ;; esac
  done

  step "Publish $v to R2 bucket $r2_bucket"
  live="$(live_version)"
  if [ -n "$live" ]; then
    info "live feed serves $live now"
    [ "$(semver_gt "$live" "$v")" = 0 ] || die "the live feed serves $live, newer than $v: use 'scripts/ship.sh rollback $v' to go back on purpose"
  else
    info "live feed: not readable now (first release or network)"
  fi

  if [ "$DRY" = 1 ]; then
    info "dry run: nothing is sent"
    while IFS= read -r name; do
      [ -n "$name" ] || continue
      info "1. would PUT $v/$name ($(ctype_of "$name"), $(size_of "$d/$name") bytes); now: $(public_state "$url_base/$v/$name")"
    done <<<"$files"
    info "2. would GET each file at $url_base/$v/<file> and compare Content-Length and SHA-512"
    info "3. would PUT manifest.json ($(ctype_of manifest.json), $(size_of "$d/manifest.json") bytes) at the bucket root, last"
    info "4. would GET $url_base/manifest.json and compare its SHA-256 with the local file"
    if [ -f "$r2_env" ]; then info "credentials file: found ($r2_env)"; else info "credentials file: missing ($r2_env), a real run would stop with the setup steps"; fi
    return 0
  fi

  load_r2 || r2_manual_help "$v"
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    upload_one "$v/$name" "$d/$name" "$cc_files" 1
  done <<<"$files"

  step "Check the uploaded files at $url_base/$v/"
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    digest="$(printf '%s' "$ROWS" | awk -F'\t' -v n="$name" '$1==n{print $3}')"
    [ -n "$digest" ] || digest="$(sha512_b64 "$d/$name")"
    check_public "$v" "$name" "$(size_of "$d/$name")" "$digest"
  done <<<"$files"

  step "Upload manifest.json (the live feed, last)"
  upload_one manifest.json "$d/manifest.json" "$cc_feed" 0
  check_live_manifest "$d/manifest.json"
  info "published $v"
}

rollback_cmd() {
  local v="$1" d="$dist/$1" name size digest url live
  need curl python3 openssl shasum
  verify_release "$v"

  step "Rollback: point the feed at $v"
  info "This points the feed at an OLDER version. Users already on a newer version get no downgrade (the updater only offers newer versions)."
  info "Ship a new, higher version to fix them."
  while IFS=$'\t' read -r name size digest url; do check_public "$v" "$name" "$size" "$digest"; done <<<"$ROWS"
  live="$(live_version)"
  info "live feed serves ${live:-an unreadable manifest} now"
  if [ -f "$TMP/live.json" ] && [ "$(sha256_of "$TMP/live.json")" = "$(sha256_of "$d/manifest.json")" ]; then
    info "the live feed already serves this exact manifest: nothing to do"; return 0
  fi

  if [ "$DRY" = 1 ]; then
    info "dry run: would PUT manifest.json ($(size_of "$d/manifest.json") bytes) at the bucket root and compare the live file"
    return 0
  fi
  load_r2 || r2_manual_help "$v"
  confirm "Point the live feed at $v (live: ${live:-unknown})?"
  upload_one manifest.json "$d/manifest.json" "public, max-age=60" 0
  check_live_manifest "$d/manifest.json"
  info "feed now serves $v"
}

# ---------------------------------------------------------------- build

port_open() { nc -z -G 3 "${winpc#*@}" 22 >/dev/null 2>&1; }

rssh() { ssh -o BatchMode=yes -o ConnectTimeout=10 -o ServerAliveInterval=15 "$winpc" "$@"; }

# Runs a remote command, hides the harmless PowerShell profile noise on winpc, returns the remote exit code.
rssh_filtered() {
  local rc=0
  rssh "$@" >"$TMP/rssh.out" 2>&1 || rc=$?
  tr -d '\r' <"$TMP/rssh.out" | grep -v -E 'PSReadLine|virtual terminal processing|Microsoft\.PowerShell_profile|^At D:|CategoryInfo|FullyQualifiedErrorId : System\.ArgumentException|^\+ (~|Set-PSReadLine)|^ *$' || true
  return "$rc"
}

ensure_winpc() {
  local waited=0 i out
  step "winpc: check that it answers on port 22"
  if ! port_open; then
    command -v wake-winbox >/dev/null 2>&1 || die "winpc does not answer on port 22 and wake-winbox is not installed"
    info "winpc is off or asleep: sending Wake-on-LAN with wake-winbox (up to 120 s)"
    while ! port_open; do
      [ $waited -lt 120 ] || die "winpc did not answer on port 22 within 120 s"
      [ $((waited % 30)) -ne 0 ] || wake-winbox >/dev/null 2>&1 || true
      sleep 5; waited=$((waited + 5))
    done
    info "winpc woke up after about $waited s; waiting 10 s for the login service"
    sleep 10
  fi
  for i in 1 2 3 4 5 6; do
    out="$(rssh_filtered 'Write-Output SHIP_PING')" || true
    case "$out" in *SHIP_PING*) info "ssh to winpc works (winpc stays on after the build)"; return 0 ;; esac
    sleep 5
  done
  die "ssh to $winpc fails: check the key login (ssh $winpc)"
}

write_remote_script() {
  cat >"$1" <<'PS1'
param([Parameter(Mandatory = $true)][string]$Version)
$ErrorActionPreference = 'Stop'
trap { Write-Output ("SHIP_REMOTE_FAIL: " + $_); exit 1 }
$rel = Join-Path $env:USERPROFILE 'ladilala-release'
$tar = Join-Path $rel 'src-in.tar'
$new = Join-Path $rel 'src-new'
$src = Join-Path $rel 'src'
$out = Join-Path $rel 'out'
$go = 'C:\tools\go\bin\go.exe'
$nsis = Join-Path $env:USERPROFILE 'tools\nsis\makensis.exe'
foreach ($f in $tar, $go, $nsis) { if (-not (Test-Path $f)) { throw "missing $f" } }
# Fresh source tree: extract next to the old one, keep the NSIS WebView2 bootstrapper, then swap.
if (Test-Path $new) { Remove-Item -Recurse -Force $new }
New-Item -ItemType Directory -Force $new, $out | Out-Null
& (Join-Path $env:SystemRoot 'System32\tar.exe') -xf $tar -C $new
if ($LASTEXITCODE -ne 0) { throw 'tar extract failed' }
$boot = 'app\build\windows\nsis\MicrosoftEdgeWebview2Setup.exe'
if (Test-Path (Join-Path $src $boot)) { Copy-Item (Join-Path $src $boot) (Join-Path $new $boot) -Force }
if (Test-Path $src) { Remove-Item -Recurse -Force $src }
Rename-Item $new 'src'
Remove-Item -Force $tar
$env:PATH = 'C:\tools\go\bin;' + $env:PATH
& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $src 'scripts\build-windows.ps1') -Version $Version -Installer -SkipUi -Makensis $nsis
if ($LASTEXITCODE -ne 0) { throw "build-windows.ps1 failed ($LASTEXITCODE)" }
$bin = Join-Path $src 'app\bin'
$exe = Join-Path $out "ladilala-$Version.exe"
$setup = Join-Path $out "ladilala-$Version-setup.exe"
Copy-Item (Join-Path $bin 'ladilala.exe') $exe -Force
Copy-Item (Join-Path $bin 'ladilala-amd64-installer.exe') $setup -Force
foreach ($f in $exe, $setup) {
  Write-Output ("SHIP_SHA256 {0} {1}" -f (Split-Path -Leaf $f), (Get-FileHash -Algorithm SHA256 $f).Hash.ToLower())
}
Write-Output 'SHIP_REMOTE_OK'
PS1
}

build_cmd() {
  local v="$1" notes_en="${2:-}" notes_vi="${3:-}" out="$dist/$1" in="$dist/_in" rsrc="ladilala-release" f want got
  need go gofmt bun bunx tar scp ssh perl python3 shasum openssl nc
  [ -x "$root/scripts/release.sh" ] || die "scripts/release.sh is missing or not executable"
  [ ! -e "$out" ] || die "$out exists. A version number is never reused: pick a new version or delete the folder by hand"
  [ -f "$key" ] || die "signing key missing: $key"
  [ -f "$wails3" ] || die "wails3 missing: $wails3 (see docs/release-process.md)"

  step "Gate 1 of 4: gofmt"
  f="$(cd "$root" && gofmt -l app core cmd)" || die "gofmt failed to run"
  [ -z "$f" ] || die "gofmt -l lists files that need formatting: $(echo "$f" | tr '\n' ' ')"
  info "gofmt clean"

  step "Gate 2 of 4: UI type check and i18n"
  (cd "$root/ui" && bunx vue-tsc --noEmit && bun run i18n:check) || die "UI gate failed (vue-tsc or i18n:check)"

  step "Build the UI and copy it to app/dist"
  (cd "$root/ui" && bun run build) || die "UI build failed"
  [ -f "$root/ui/dist/index.html" ] || die "ui/dist/index.html is missing after the build"
  rm -rf "$root/app/dist"
  cp -R "$root/ui/dist" "$root/app/dist"
  info "app/dist updated from ui/dist"

  step "Gate 3 of 4: go build and Windows vet"
  (cd "$root" && quiet_ld go build ./...) || die "go build failed"
  (cd "$root" && quiet_ld hard_timeout 600 env GOOS=windows GOARCH=amd64 go vet ./...) || die "GOOS=windows go vet failed or timed out"

  step "Gate 4 of 4: go test (limit 10 minutes)"
  (cd "$root" && quiet_ld hard_timeout 600 go test -count=1 -timeout 9m ./app/... ./core/...) || die "go test failed or timed out"

  ensure_winpc

  step "Sync the source to winpc ($rsrc)"
  # COPYFILE_DISABLE stops macOS tar from adding ._* files, which the build would embed as bad migration names.
  (cd "$root" && COPYFILE_DISABLE=1 tar -cf "$TMP/src-in.tar" --exclude='._*' --exclude='*.exe' --exclude='app/bin' --exclude='app/dist' \
    go.mod go.sum app cmd core scripts ui/dist) || die "tar failed"
  info "archive $(size_of "$TMP/src-in.tar") bytes"
  scp -q -o BatchMode=yes "$TMP/src-in.tar" "$winpc:$rsrc/src-in.tar" || die "scp of the source archive failed"
  write_remote_script "$TMP/ship-remote.ps1"
  scp -q -o BatchMode=yes "$TMP/ship-remote.ps1" "$winpc:$rsrc/ship-remote.ps1" || die "scp of the remote build script failed"

  step "Build exe and NSIS setup on winpc (version $v)"
  rssh_filtered "powershell -NoProfile -ExecutionPolicy Bypass -File \$env:USERPROFILE\\$rsrc\\ship-remote.ps1 -Version $v" \
    | tee "$TMP/remote.log" | sed 's/^/    /' || true
  grep -q '^SHIP_REMOTE_OK' "$TMP/remote.log" || die "the build on winpc failed (see the lines above)"

  step "Copy the build back to dist-release/_in"
  mkdir -p "$in"
  for f in "ladilala-$v.exe" "ladilala-$v-setup.exe"; do
    scp -q -o BatchMode=yes "$winpc:$rsrc/out/$f" "$in/$f" || die "scp of $f failed"
    want="$(awk -v n="$f" '$1=="SHIP_SHA256" && $2==n{print $3}' "$TMP/remote.log")"
    got="$(sha256_of "$in/$f")"
    [ -n "$want" ] && [ "$want" = "$got" ] || die "$f: SHA-256 after copy ($got) differs from winpc (${want:-none})"
    info "$f: $(size_of "$in/$f") bytes, SHA-256 $got"
  done

  step "Sign and assemble dist-release/$v with scripts/release.sh"
  "$root/scripts/release.sh" "$v" "$in/ladilala-$v.exe" "$in/ladilala-$v-setup.exe" "$notes_en" "$notes_vi" \
    || { rm -rf "$out"; die "scripts/release.sh failed; the partial folder $out was removed"; }
  info "next: scripts/ship.sh verify $v, then scripts/ship.sh publish $v"
}

all_cmd() {
  local v="$1"
  if [ -e "$dist/$v" ]; then
    step "Build skipped: $dist/$v already exists (resuming)"
  else
    build_cmd "$@"
  fi
  verify_release "$v"
  step "Ready to publish $v"
  if [ "$DRY" = 0 ]; then confirm "Upload $v to R2 and switch the live feed?"; fi
  publish_cmd "$v"
}

# ---------------------------------------------------------------- main

cmd="${1:-help}"
[ $# -eq 0 ] || shift
p1=""; p2=""; p3=""; np=0
for a in "$@"; do
  case "$a" in
    --yes|-y) YES=1 ;;
    --dry-run) DRY=1 ;;
    -h|--help) cmd=help ;;
    --*) die "unknown option: $a (try: scripts/ship.sh help)" ;;
    *) np=$((np + 1)); case $np in 1) p1="$a" ;; 2) p2="$a" ;; 3) p3="$a" ;; *) die "too many arguments" ;; esac ;;
  esac
done

case "$cmd" in
  help) usage ;;
  build)    [ $np -ge 1 ] || die "usage: scripts/ship.sh build <version> [notes-en] [notes-vi]"; check_version "$p1"; build_cmd "$p1" "$p2" "$p3" ;;
  verify)   [ $np -eq 1 ] || die "usage: scripts/ship.sh verify <version>"; check_version "$p1"; verify_release "$p1" ;;
  publish)  [ $np -eq 1 ] || die "usage: scripts/ship.sh publish <version> [--dry-run]"; check_version "$p1"; publish_cmd "$p1" ;;
  rollback) [ $np -eq 1 ] || die "usage: scripts/ship.sh rollback <version> [--yes] [--dry-run]"; check_version "$p1"; rollback_cmd "$p1" ;;
  all)      [ $np -ge 1 ] || die "usage: scripts/ship.sh all <version> [notes-en] [notes-vi] [--yes]"; check_version "$p1"; all_cmd "$p1" "$p2" "$p3" ;;
  *) die "unknown command: $cmd (try: scripts/ship.sh help)" ;;
esac
