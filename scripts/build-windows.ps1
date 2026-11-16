# Builds the Ladilala desktop app on Windows: UI, exe, optional installer, optional signing.
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-windows.ps1 -Version 1.0.0 [-Installer] [-Thumbprint <sha1>]
# Needs: Go 1.26 or newer, bun (not needed with -SkipUi when ui\dist is already built). The installer needs makensis (NSIS 3) on PATH or at -Makensis.
# No cgo, no Wails CLI and no Node are needed.
param(
  [string]$Version = '1.0.0',
  [switch]$Installer,
  [string]$Thumbprint = '',
  [string]$Makensis = 'makensis',
  [string]$OutDir = '',
  [switch]$SkipUi
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not $OutDir) { $OutDir = Join-Path $root 'app\bin' }
New-Item -ItemType Directory -Force $OutDir | Out-Null
$sw = [Diagnostics.Stopwatch]::StartNew()

function Sign([string]$file) {
  if (-not $Thumbprint) { return }
  $cert = Get-ChildItem Cert:\CurrentUser\My | Where-Object Thumbprint -eq $Thumbprint | Select-Object -First 1
  if (-not $cert) { throw "certificate $Thumbprint not found in Cert:\CurrentUser\My" }
  try {
    $null = Set-AuthenticodeSignature -FilePath $file -Certificate $cert -HashAlgorithm SHA256 -TimestampServer 'http://timestamp.digicert.com'
  } catch {
    Write-Warning "timestamp server failed, signing without a timestamp: $_"
    $null = Set-AuthenticodeSignature -FilePath $file -Certificate $cert -HashAlgorithm SHA256
  }
  # A self-signed certificate reports UnknownError (root not trusted) even when the signature is intact.
  $sig = Get-AuthenticodeSignature $file
  if (-not $sig.SignerCertificate) { throw "signing $file failed: $($sig.StatusMessage)" }
  Write-Host ("signed {0} : {1}, timestamp: {2}" -f $file, $sig.Status, [bool]$sig.TimeStamperCertificate)
}

if (-not $SkipUi) {
  Push-Location (Join-Path $root 'ui')
  try {
    bun install --frozen-lockfile
    bun run build
    if ($LASTEXITCODE -ne 0) { throw 'UI build failed' }
  } finally { Pop-Location }
}

$dist = Join-Path $root 'app\dist'
if (Test-Path $dist) { Remove-Item -Recurse -Force $dist }
Copy-Item -Recurse (Join-Path $root 'ui\dist') $dist

$exe = Join-Path $OutDir 'ladilala.exe'
$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
Push-Location $root
try {
  go build -trimpath -buildvcs=false -tags production -ldflags "-s -w -H windowsgui -X main.version=$Version" -o $exe ./app
  if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
} finally { Pop-Location }
Sign $exe
Write-Host ("exe: {0} ({1:N1} MB)" -f $exe, ((Get-Item $exe).Length / 1MB))

if ($Installer) {
  $nsis = Join-Path $root 'app\build\windows\nsis'
  $boot = Join-Path $nsis 'MicrosoftEdgeWebview2Setup.exe'
  if (-not (Test-Path $boot)) {
    # Microsoft's online bootstrapper; the installer runs it only when WebView2 is missing.
    Invoke-WebRequest -UseBasicParsing 'https://go.microsoft.com/fwlink/p/?LinkId=2124703' -OutFile $boot
  }
  Push-Location $nsis
  try {
    # NSIS builds VIProductVersion as "<define>.0", so a pre-release suffix such as -beta.1 must not reach it.
    $numeric = ($Version -split '-')[0]
    & $Makensis "-DINFO_PRODUCTVERSION=$numeric" "-DARG_WAILS_AMD64_BINARY=$exe" project.nsi
    if ($LASTEXITCODE -ne 0) { throw 'makensis failed' }
  } finally { Pop-Location }
  $setup = Join-Path $root 'app\bin\ladilala-amd64-installer.exe'
  if (-not (Test-Path $setup)) { $setup = Get-ChildItem -Recurse (Join-Path $root 'app') -Filter 'ladilala-amd64-installer.exe' | Select-Object -First 1 -ExpandProperty FullName }
  Sign $setup
  Write-Host ("installer: {0} ({1:N1} MB)" -f $setup, ((Get-Item $setup).Length / 1MB))
}
Write-Host ("build time: {0:N1} s" -f $sw.Elapsed.TotalSeconds)
