package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FPCBuild is one pinned fingerprint-chromium release. Only builds in
// FPCBuilds are ever offered: the zip is checked against SHA256 before it is
// unpacked, so a swapped asset on the release page cannot reach the disk.
type FPCBuild struct {
	Tag           string // release tag and four-part Chromium version
	URL           string
	Size          int64  // zip size in bytes
	SHA256        string // lower-case hex SHA-256 of the zip
	ChromiumMajor int
}

// FPCBuilds is the pin table. 150.0.7871.186 is not here on purpose: it
// crashes on the first canvas read (200 of 200 launches, docs/m0-engine-matrix.md).
// To add a tag, follow docs/engines-fpc.md.
var FPCBuilds = []FPCBuild{
	{
		Tag:           "148.0.7778.215",
		URL:           "https://github.com/adryfish/fingerprint-chromium/releases/download/148.0.7778.215/ungoogled-chromium_148.0.7778.215-1.1_windows_x64.zip",
		Size:          189767686,
		SHA256:        "9ef3f471b7a6641b4224532522b29141ce3746e27d55788d88e2fd951f362579",
		ChromiumMajor: 148,
	},
}

// FPCDiskBytes is the size of the unpacked 148 build, about 425 MB.
const FPCDiskBytes int64 = 425 * 1024 * 1024

// FPCMetaFile is the metadata file inside an installed build folder. It has
// the same name as the Chrome for Testing one.
const FPCMetaFile = "ladilala-engine.json"

// Typed fingerprint-chromium failures. Match them with errors.Is.
var (
	// ErrFPCHashMismatch means the downloaded zip is not the pinned file. The
	// download is deleted and nothing is installed.
	ErrFPCHashMismatch = errors.New("driver: fingerprint-chromium download does not match the pinned SHA-256")
	// ErrFPCDownload means the zip could not be downloaded (offline, server error).
	ErrFPCDownload = errors.New("driver: fingerprint-chromium download failed")
	// ErrFPCNoBinary means the unpacked zip holds no browser binary.
	ErrFPCNoBinary = errors.New("driver: fingerprint-chromium zip has no browser binary")
	// ErrFPCCanvasBroken means the engine hangs or crashes when a page reads
	// the canvas or WebGL. Such a build must not run profiles.
	ErrFPCCanvasBroken = errors.New("This engine build crashed on canvas read. Choose another engine.")
)

// FPCPinned returns the build the app offers.
func FPCPinned() FPCBuild { return FPCBuilds[0] }

// FPCMeta is what the installer records next to an unpacked build.
type FPCMeta struct {
	Tag           string `json:"tag"`
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"sizeBytes"`
	ChromiumMajor int    `json:"chromiumMajor"`
	InstalledAt   string `json:"installedAt"`
}

// FPCTagDir is the folder of one tag under the engine folder.
func FPCTagDir(dir, tag string) string { return filepath.Join(dir, tag) }

// FPCInstalled reports the metadata and binary of tag under dir. A folder
// without a metadata file is a half-done unpack and counts as not installed.
func FPCInstalled(dir, tag string) (FPCMeta, Install, bool) {
	tagDir := FPCTagDir(dir, tag)
	b, err := os.ReadFile(filepath.Join(tagDir, FPCMetaFile))
	if err != nil {
		return FPCMeta{}, Install{}, false
	}
	var m FPCMeta
	if json.Unmarshal(b, &m) != nil || m.Tag != tag {
		return FPCMeta{}, Install{}, false
	}
	in, err := fpcBinary(tagDir)
	if err != nil {
		return FPCMeta{}, Install{}, false
	}
	return m, in, true
}

// fpcBinary finds the browser binary under an unpacked build folder.
func fpcBinary(dir string) (Install, error) {
	installs, err := FingerprintChromium{SearchDirs: []string{dir}}.Detect()
	if err != nil {
		return Install{}, err
	}
	if len(installs) == 0 {
		return Install{}, fmt.Errorf("%w: %s", ErrFPCNoBinary, dir)
	}
	in := installs[0]
	in.FPC = true
	return in, nil
}

// RemoveFPC deletes an installed build and any leftover download of it.
func RemoveFPC(dir, tag string) error {
	if !validFPCTag(tag) {
		return fmt.Errorf("driver: invalid fingerprint-chromium tag %q", tag)
	}
	err := os.RemoveAll(FPCTagDir(dir, tag))
	_ = os.RemoveAll(FPCTagDir(dir, tag) + ".staging")
	_ = os.Remove(fpcPartPath(dir, tag))
	return err
}

func validFPCTag(tag string) bool {
	if tag == "" || len(tag) > 32 || strings.HasPrefix(tag, ".") || strings.Contains(tag, "..") {
		return false
	}
	for _, r := range tag {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func fpcPartPath(dir, tag string) string {
	return filepath.Join(dir, "downloads", tag+".zip.part")
}

// FPCPartial returns the path of the partial download of tag and whether it
// exists. A cancelled install keeps it so the next one continues.
func FPCPartial(dir, tag string) (string, bool) {
	p := fpcPartPath(dir, tag)
	st, err := os.Stat(p)
	return p, err == nil && st.Mode().IsRegular()
}

// Install stages reported by FPCInstallOptions.Stage.
const (
	FPCStageDownload = "download"
	FPCStageVerify   = "verify"
	FPCStageUnpack   = "unpack"
)

// FPCInstallOptions drives InstallFPC.
type FPCInstallOptions struct {
	Dir    string // <data>/engines/fingerprint-chromium
	Build  FPCBuild
	Client *http.Client
	// Attempts is the number of download attempts (default 3). RetryDelay is
	// the pause after attempt n, n times RetryDelay (default 1 s).
	Attempts   int
	RetryDelay time.Duration
	Hooks      DownloadHooks
	// Attempt is called when download attempt n starts. It may be nil.
	Attempt func(n int)
	// Stage is called when a stage starts. It may be nil.
	Stage func(stage string)
	// Unpacked is called after the unpack with the file count and byte size.
	Unpacked func(files int, bytes int64)
}

// InstallFPC downloads the pinned zip, checks its size and SHA-256, unpacks it
// into <Dir>/<tag>.staging, writes the metadata file and renames the folder to
// <Dir>/<tag>. The final folder appears only when everything worked.
//
// A cancelled install keeps its partial download so the next one resumes. Any
// other failure (offline, hash mismatch, bad zip) deletes the partial download
// and the staging folder, and installs nothing. An existing folder of the
// same tag is replaced only after the new one is complete.
func InstallFPC(ctx context.Context, o FPCInstallOptions) (FPCMeta, error) {
	b := o.Build
	if !validFPCTag(b.Tag) || b.SHA256 == "" || b.Size <= 0 {
		return FPCMeta{}, fmt.Errorf("driver: fingerprint-chromium build %q is not pinned completely", b.Tag)
	}
	if o.Attempts <= 0 {
		o.Attempts = 3
	}
	if o.RetryDelay <= 0 {
		o.RetryDelay = time.Second
	}
	stage := func(s string) {
		if o.Stage != nil {
			o.Stage(s)
		}
	}
	part := fpcPartPath(o.Dir, b.Tag)
	staging := FPCTagDir(o.Dir, b.Tag) + ".staging"
	fail := func(err error) (FPCMeta, error) {
		_ = os.RemoveAll(staging)
		if ctx.Err() == nil {
			_ = os.Remove(part)
		}
		return FPCMeta{}, err
	}
	if err := os.MkdirAll(filepath.Dir(part), 0o700); err != nil {
		return FPCMeta{}, err
	}

	// download, resumable.
	stage(FPCStageDownload)
	if st, err := os.Stat(part); err == nil && st.Size() > b.Size {
		_ = os.Remove(part)
	}
	if st, err := os.Stat(part); err != nil || st.Size() != b.Size {
		var last error
		for attempt := 1; attempt <= o.Attempts; attempt++ {
			if o.Attempt != nil {
				o.Attempt(attempt)
			}
			last = DownloadFile(ctx, o.Client, b.URL, part, o.Hooks)
			if last == nil {
				break
			}
			if ctx.Err() != nil {
				return fail(ctx.Err())
			}
			if IsDiskFull(last) {
				return fail(last)
			}
			if attempt < o.Attempts {
				select {
				case <-ctx.Done():
					return fail(ctx.Err())
				case <-time.After(time.Duration(attempt) * o.RetryDelay):
				}
			}
		}
		if last != nil {
			return fail(fmt.Errorf("%w: %w", ErrFPCDownload, last))
		}
	}

	// verify.
	stage(FPCStageVerify)
	sum, err := FileSHA256(ctx, part)
	if err != nil {
		return fail(err)
	}
	if !strings.EqualFold(sum, b.SHA256) {
		return fail(fmt.Errorf("%w: expected %s, got %s", ErrFPCHashMismatch, b.SHA256, sum))
	}

	// unpack into staging, then rename.
	stage(FPCStageUnpack)
	_ = os.RemoveAll(staging)
	files, bytes, err := Unzip(ctx, part, staging)
	if err != nil {
		return fail(err)
	}
	if _, err := fpcBinary(staging); err != nil {
		return fail(err)
	}
	if o.Unpacked != nil {
		o.Unpacked(files, bytes)
	}
	meta := FPCMeta{Tag: b.Tag, URL: b.URL, SHA256: sum, SizeBytes: b.Size, ChromiumMajor: b.ChromiumMajor, InstalledAt: time.Now().UTC().Format(time.RFC3339Nano)}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(staging, FPCMetaFile), raw, 0o600); err != nil {
		return fail(err)
	}
	final := FPCTagDir(o.Dir, b.Tag)
	_ = os.RemoveAll(final)
	if err := os.Rename(staging, final); err != nil {
		return fail(err)
	}
	_ = os.Remove(part)
	return meta, nil
}
