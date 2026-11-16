package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/8bu/ladilala/core/driver"
)

// Stage keys of an install job, in order.
var jobStages = []string{"check", "download", "verify", "unpack", "selftest", "switch"}

// Job states.
const (
	jobRunning   = "running"
	jobPaused    = "paused"
	jobDone      = "done"
	jobFailed    = "failed"
	jobCancelled = "cancelled"
)

const (
	downloadAttempts    = 3
	engineProgressEvery = 250 * time.Millisecond
)

// JobStage is one step of a job. Code and Params let the UI write the detail
// line in the user's language.
type JobStage struct {
	Key    string         `json:"key"`
	State  string         `json:"state"` // pending | active | done | fail
	Code   string         `json:"code,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

// JobDTO is the state of one install job. The same value arrives as the
// event "engine:job" after every change, at most four times a second while
// bytes flow.
type JobDTO struct {
	ID          string     `json:"id"`
	EngineID    string     `json:"engineId"`
	Kind        string     `json:"kind"` // install | update | redownload
	Version     string     `json:"version"`
	State       string     `json:"state"`
	Stage       string     `json:"stage"`
	Stages      []JobStage `json:"stages"`
	BytesDone   int64      `json:"bytesDone"`
	BytesTotal  int64      `json:"bytesTotal"`
	BytesPerSec int64      `json:"bytesPerSec"`
	Attempt     int        `json:"attempt"`
	PartialKept bool       `json:"partialKept"`
	Error       *APIError  `json:"error,omitempty"`
	StartedAt   string     `json:"startedAt"`
}

type engineJob struct {
	svc    *EngineService
	cancel context.CancelFunc

	mu      sync.Mutex
	dto     JobDTO
	resume  chan struct{} // closed while the job is not paused
	lastEmt time.Time
	done    chan struct{}
	// userCancel is true when the user pressed Cancel, false when the app is closing.
	userCancel bool
}

func (j *engineJob) snapshot() JobDTO {
	j.mu.Lock()
	defer j.mu.Unlock()
	d := j.dto
	d.Stages = append([]JobStage(nil), d.Stages...)
	return d
}

func (j *engineJob) running() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.dto.State == jobRunning || j.dto.State == jobPaused
}

// update changes the DTO under the lock and emits it. force skips the
// progress throttle.
func (j *engineJob) update(force bool, fn func(*JobDTO)) {
	j.mu.Lock()
	fn(&j.dto)
	if !force && time.Since(j.lastEmt) < engineProgressEvery {
		j.mu.Unlock()
		return
	}
	j.lastEmt = time.Now()
	d := j.dto
	d.Stages = append([]JobStage(nil), d.Stages...)
	j.mu.Unlock()
	if j.svc.emit != nil {
		j.svc.emit("engine:job", d)
	}
}

func (j *engineJob) stage(key, state, code string, params map[string]any) {
	j.update(true, func(d *JobDTO) {
		for i := range d.Stages {
			if d.Stages[i].Key == key {
				d.Stages[i].State, d.Stages[i].Code, d.Stages[i].Params = state, code, params
			}
		}
		if state == "active" {
			d.Stage = key
		}
	})
}

// ---- API --------------------------------------------------------------------------

// InstallRequest starts a Chrome for Testing install.
type InstallRequest struct {
	EngineID string `json:"engineId"`
	// Version is a four-part version from CheckEngineUpdates. Empty means the
	// newest version of the chosen channel.
	Version string `json:"version"`
}

// InstallEngine downloads, verifies and installs Chrome for Testing in the
// background and returns the job at once. Watch the event "engine:job".
func (s *EngineService) InstallEngine(_ context.Context, req InstallRequest) (JobDTO, error) {
	return s.startJob(req, "install")
}

// UpdateEngine installs the newest version next to the active one. The active
// version stays until the new one passes its self-test.
func (s *EngineService) UpdateEngine(_ context.Context, id string) (JobDTO, error) {
	return s.startJob(InstallRequest{EngineID: id}, "update")
}

// RedownloadEngine deletes one installed version and installs it again. The
// SHA-256 recorded on its first download must match; a mismatch fails the job.
func (s *EngineService) RedownloadEngine(_ context.Context, id, version string) (JobDTO, error) {
	if !validVersion(version) {
		return JobDTO{}, apiErr("invalid", "version is not valid", "field", "version")
	}
	return s.startJob(InstallRequest{EngineID: id, Version: version}, "redownload")
}

func (s *EngineService) startJob(req InstallRequest, kind string) (JobDTO, error) {
	switch req.EngineID {
	case engineCfT:
	case enginePatched:
		return JobDTO{}, apiErr("engine.unavailable", "the own Chromium is not available")
	case engineChrome, engineEdge:
		return JobDTO{}, apiErr("engine.system", "this engine is managed by Windows", "id", req.EngineID)
	default:
		return JobDTO{}, apiErr("notFound", "unknown engine", "id", req.EngineID)
	}
	if req.Version != "" && !validVersion(req.Version) {
		return JobDTO{}, apiErr("invalid", "version is not valid", "field", "version")
	}
	if s.platform == "" {
		return JobDTO{}, apiErr("engine.platform", "Chrome for Testing has no build for this system")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return JobDTO{}, apiErr("conflict", "the app is closing")
	}
	if s.runningJobLocked(engineCfT) != "" {
		return JobDTO{}, apiErr("conflict", "an install is already running", "reason", "jobRunning")
	}
	s.seq++
	ctx, cancel := context.WithCancel(context.Background())
	j := &engineJob{svc: s, cancel: cancel, resume: make(chan struct{}), done: make(chan struct{})}
	close(j.resume)
	j.dto = JobDTO{
		ID: fmt.Sprintf("engjob-%d-%d", time.Now().UnixNano(), s.seq), EngineID: req.EngineID, Kind: kind, Version: req.Version,
		State: jobRunning, StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, k := range jobStages {
		j.dto.Stages = append(j.dto.Stages, JobStage{Key: k, State: "pending"})
	}
	s.jobs[j.dto.ID] = j
	go s.runJob(ctx, j)
	return j.snapshot(), nil
}

func (s *EngineService) job(id string) (*engineJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[id]
	if j == nil {
		return nil, apiErr("notFound", "unknown job", "id", id)
	}
	return j, nil
}

// GetJob returns the current state of a job.
func (s *EngineService) GetJob(_ context.Context, id string) (JobDTO, error) {
	j, err := s.job(id)
	if err != nil {
		return JobDTO{}, err
	}
	return j.snapshot(), nil
}

// ListJobs returns the jobs of this session, newest first.
func (s *EngineService) ListJobs(context.Context) ([]JobDTO, error) {
	s.mu.Lock()
	out := make([]JobDTO, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j.snapshot())
	}
	s.mu.Unlock()
	sort.Slice(out, func(a, b int) bool {
		return out[a].StartedAt > out[b].StartedAt || (out[a].StartedAt == out[b].StartedAt && out[a].ID > out[b].ID)
	})
	return out, nil
}

// PauseJob holds the download. It works only while the stage is download.
func (s *EngineService) PauseJob(_ context.Context, id string) (JobDTO, error) {
	j, err := s.job(id)
	if err != nil {
		return JobDTO{}, err
	}
	j.mu.Lock()
	if j.dto.State != jobRunning || j.dto.Stage != "download" {
		j.mu.Unlock()
		return JobDTO{}, apiErr("conflict", "only a running download can be paused", "reason", "notDownloading")
	}
	j.resume = make(chan struct{})
	j.mu.Unlock()
	j.update(true, func(d *JobDTO) { d.State, d.BytesPerSec = jobPaused, 0 })
	return j.snapshot(), nil
}

// ResumeJob continues a paused download.
func (s *EngineService) ResumeJob(_ context.Context, id string) (JobDTO, error) {
	j, err := s.job(id)
	if err != nil {
		return JobDTO{}, err
	}
	j.mu.Lock()
	if j.dto.State != jobPaused {
		j.mu.Unlock()
		return JobDTO{}, apiErr("conflict", "the job is not paused", "reason", "notPaused")
	}
	close(j.resume)
	j.mu.Unlock()
	j.update(true, func(d *JobDTO) { d.State = jobRunning })
	return j.snapshot(), nil
}

// CancelJob stops a job. A partial download stays on disk so a later install
// of the same version continues from it.
func (s *EngineService) CancelJob(_ context.Context, id string) (JobDTO, error) {
	j, err := s.job(id)
	if err != nil {
		return JobDTO{}, err
	}
	if !j.running() {
		return j.snapshot(), nil
	}
	j.mu.Lock()
	j.userCancel = true
	if j.dto.State == jobPaused {
		close(j.resume)
	}
	j.mu.Unlock()
	j.cancel()
	<-j.done
	return j.snapshot(), nil
}

// ApplyJobFix runs the one-click fix of a failed job and starts the job again.
// fixId is retry (continue the partial download), redownload (delete the
// partial download and the zip, then start clean) or freeSpace (delete old
// versions and leftovers, then retry).
func (s *EngineService) ApplyJobFix(ctx context.Context, id, fixID string) (JobDTO, error) {
	j, err := s.job(id)
	if err != nil {
		return JobDTO{}, err
	}
	old := j.snapshot()
	if old.State == jobRunning || old.State == jobPaused {
		return JobDTO{}, apiErr("conflict", "the job is still running", "reason", "jobRunning")
	}
	switch fixID {
	case "retry":
	case "redownload":
		s.removeDownload(old.Version)
	case "freeSpace":
		s.freeSpace()
	default:
		return JobDTO{}, apiErr("invalid", "unknown fix", "fixId", fixID)
	}
	kind := old.Kind
	if kind == "redownload" {
		kind = "install" // the old version is already gone
	}
	return s.startJob(InstallRequest{EngineID: old.EngineID, Version: old.Version}, kind)
}

// ---- the job ----------------------------------------------------------------------

func (s *EngineService) runJob(ctx context.Context, j *engineJob) {
	defer close(j.done)
	err := s.execute(ctx, j)
	switch {
	case err == nil:
		j.update(true, func(d *JobDTO) { d.State, d.Stage, d.Error = jobDone, "", nil })
	case errors.Is(err, context.Canceled):
		j.mu.Lock()
		user := j.userCancel
		j.mu.Unlock()
		kept := fileExists(s.partPath(j.snapshot().Version))
		j.update(true, func(d *JobDTO) {
			d.State, d.PartialKept = jobCancelled, kept
			for i := range d.Stages {
				if d.Stages[i].State == "active" {
					d.Stages[i].State = "pending"
				}
			}
			if !user {
				d.Error = &APIError{Code: "engine.closing"}
			}
		})
	default:
		ae := toEngineError(err)
		j.update(true, func(d *JobDTO) {
			d.State, d.Error, d.PartialKept = jobFailed, ae, fileExists(s.partPath(d.Version))
			for i := range d.Stages {
				if d.Stages[i].State == "active" {
					d.Stages[i].State, d.Stages[i].Code = "fail", ae.Code
				}
			}
		})
	}
}

// toEngineError maps a failure to a code the UI translates and, where one
// exists, the fix the UI offers.
func toEngineError(err error) *APIError {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae
	}
	e := &APIError{Code: "engine.internal", Message: err.Error()}
	switch {
	case errors.Is(err, driver.ErrDebuggingBlocked):
		e.Code = "engine.policy"
	case errors.Is(err, driver.ErrBinaryNotFound):
		e.Code = "engine.binary"
	case errors.Is(err, zip.ErrChecksum), errors.Is(err, zip.ErrFormat):
		e.Code, e.Details = "engine.checksum", map[string]any{"fixId": "redownload"}
	case isDiskFull(err):
		e.Code, e.Details = "engine.disk", map[string]any{"fixId": "freeSpace"}
	case errors.Is(err, context.DeadlineExceeded):
		e.Code = "engine.timeout"
	}
	return e
}

func isDiskFull(err error) bool {
	if errors.Is(err, syscall.ENOSPC) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "no space left") || strings.Contains(m, "not enough space")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (s *EngineService) execute(ctx context.Context, j *engineJob) error {
	d := j.snapshot()

	// 1 check: choose the build.
	j.stage("check", "active", "contacting", nil)
	build, err := s.pickBuild(ctx, d.Version)
	if err != nil {
		return err
	}
	j.update(true, func(d *JobDTO) { d.Version = build.Version })
	if d.Kind == "redownload" {
		if err := s.removeVersion(build.Version); err != nil {
			return err
		}
	} else if s.metaOf(build.Version) != nil {
		return apiErr("engine.installed", "this version is already installed", "version", build.Version)
	}
	j.stage("check", "done", "latest", map[string]any{"version": build.Version, "channel": build.Channel, "have": s.readState().Active})

	// 2 download.
	j.stage("download", "active", "", nil)
	zipPath, err := s.download(ctx, j, build)
	if err != nil {
		return err
	}
	size := fileSize(zipPath)
	j.stage("download", "done", "downloaded", map[string]any{"bytes": size, "attempts": j.snapshot().Attempt})

	// 3 verify: SHA-256 against the value recorded on the first download.
	j.stage("verify", "active", "computing", nil)
	sum, err := fileSHA256(ctx, zipPath)
	if err != nil {
		return err
	}
	s.mu.Lock()
	recorded := s.readSums()[sumKey(build.Platform, build.Version)]
	s.mu.Unlock()
	if recorded != "" && recorded != sum {
		_ = os.Remove(zipPath)
		return apiErr("engine.checksum", "the downloaded file differs from the first download of this version",
			"expected", recorded, "got", sum, "fixId", "redownload")
	}
	if zr, err := zip.OpenReader(zipPath); err != nil {
		_ = os.Remove(zipPath)
		return apiErr("engine.checksum", "the downloaded file is not a valid zip: "+err.Error(), "fixId", "redownload")
	} else {
		_ = zr.Close()
	}
	j.stage("verify", "done", map[bool]string{true: "shaMatches", false: "shaRecorded"}[recorded != ""], map[string]any{"sha256": sum})

	// 4 unpack into a staging folder, then rename.
	j.stage("unpack", "active", "extracting", nil)
	staging := s.versionDir(build.Version) + ".staging"
	_ = os.RemoveAll(staging)
	files, bytes, err := unzipTo(ctx, zipPath, staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		if errors.Is(err, zip.ErrChecksum) {
			_ = os.Remove(zipPath)
		}
		return err
	}
	in := driver.CfTInstall(staging, build.Platform)
	if !fileExists(in.Path) {
		_ = os.RemoveAll(staging)
		return apiErr("engine.unpack", "the zip has no browser binary", "path", relTo(staging, in.Path))
	}
	meta := cftMeta{Version: build.Version, Platform: build.Platform, Channel: build.Channel, URL: build.URL, SHA256: sum, SizeBytes: size, InstalledAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := writeJSON(filepath.Join(staging, engineMetaFile), meta); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	final := s.versionDir(build.Version)
	_ = os.RemoveAll(final)
	if err := os.Rename(staging, final); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	j.stage("unpack", "done", "unpacked", map[string]any{"files": files, "bytes": bytes})

	// 5 self-test: the browser starts and answers over the CDP pipe.
	j.stage("selftest", "active", "testing", nil)
	inst := driver.CfTInstall(final, build.Platform)
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_, perr := s.probe(pctx, inst)
	cancel()
	if perr != nil {
		_ = os.RemoveAll(final) // the old engine stays active; the zip stays for a retry
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return selftestError(perr)
	}
	j.stage("selftest", "done", "selftestOk", nil)

	// 6 switch: record the checksum, make the new version active, prune.
	j.stage("switch", "active", "switching", nil)
	s.mu.Lock()
	sums := s.readSums()
	sums[sumKey(build.Platform, build.Version)] = sum
	err = writeJSON(filepath.Join(s.dir, engineSumsFile), sums)
	var prev string
	if err == nil {
		st := s.readState()
		prev = st.Active
		err = s.writeState(cftState{Active: build.Version, Previous: prev})
		if err == nil {
			s.pruneLocked()
		}
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	_ = os.Remove(zipPath)
	s.applyDefault()
	j.stage("switch", "done", map[bool]string{true: "activeKept", false: "active"}[prev != ""], map[string]any{"version": build.Version, "previous": prev})
	return nil
}

func selftestError(err error) error {
	e := toEngineError(err)
	if e.Code == "engine.internal" {
		e.Code = "engine.selftest"
	}
	return e
}

func relTo(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil {
		return r
	}
	return p
}

// pruneLocked deletes old versions beyond the keepOld setting and sets
// previous to the newest old version that stays. Call with s.mu held.
func (s *EngineService) pruneLocked() {
	st := s.readState()
	keep := s.cfg.Get().Engines.KeepOld
	kept := 0
	st.Previous = ""
	for _, m := range s.installedVersions() { // newest first
		if m.Version == st.Active {
			continue
		}
		if kept < keep {
			if st.Previous == "" {
				st.Previous = m.Version
			}
			kept++
			continue
		}
		_ = os.RemoveAll(s.versionDir(m.Version))
	}
	_ = s.writeState(st)
}

// pickBuild finds the build to install: the asked version, or the newest of
// the chosen channel.
func (s *EngineService) pickBuild(ctx context.Context, version string) (driver.CfTBuild, error) {
	builds, err := s.fetchVersions(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return driver.CfTBuild{}, ctx.Err()
		}
		return driver.CfTBuild{}, apiErr("engine.network", err.Error(), "fixId", "retry")
	}
	s.mu.Lock()
	s.latest = builds
	s.mu.Unlock()
	want := s.cfg.Get().Engines.Channel
	for _, b := range builds {
		if version != "" && b.Version == version {
			return b, nil
		}
		if version == "" && b.Channel == want {
			return b, nil
		}
	}
	if version != "" { // an older version: the list has only the newest of each channel
		return driver.CfTBuild{}, apiErr("engine.versionGone", "this version is not offered any more", "version", version)
	}
	return driver.CfTBuild{}, apiErr("engine.versionGone", "the channel has no build for this system", "channel", want)
}

// ---- download ---------------------------------------------------------------------

func (s *EngineService) downloadDir() string { return filepath.Join(s.dir, "downloads") }
func (s *EngineService) zipPath(v string) string {
	return filepath.Join(s.downloadDir(), v+"-"+s.platform+".zip")
}
func (s *EngineService) partPath(v string) string { return s.zipPath(v) + ".part" }

func (s *EngineService) removeDownload(v string) {
	if validVersion(v) {
		_ = os.Remove(s.partPath(v))
		_ = os.Remove(s.zipPath(v))
	}
}

func (s *EngineService) removeVersion(v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.readState()
	if err := os.RemoveAll(s.versionDir(v)); err != nil {
		return err
	}
	if st.Active == v {
		st.Active = ""
	}
	if st.Previous == v {
		st.Previous = ""
	}
	return s.writeState(st)
}

// freeSpace deletes every download leftover and every old version that is
// neither active nor previous.
func (s *EngineService) freeSpace() {
	_ = os.RemoveAll(s.downloadDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.readState()
	for _, m := range s.installedVersions() {
		if m.Version != st.Active && m.Version != st.Previous {
			_ = os.RemoveAll(s.versionDir(m.Version))
		}
	}
	ents, _ := os.ReadDir(s.dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".staging") {
			_ = os.RemoveAll(filepath.Join(s.dir, e.Name()))
		}
	}
}

// download fetches the zip with up to three attempts and a growing pause. Each
// attempt continues the partial file with a Range request when the server
// allows it. It returns the path of the finished zip.
func (s *EngineService) download(ctx context.Context, j *engineJob, b driver.CfTBuild) (string, error) {
	if err := os.MkdirAll(s.downloadDir(), 0o700); err != nil {
		return "", err
	}
	final, part := s.zipPath(b.Version), s.partPath(b.Version)
	if fileExists(final) { // an earlier job downloaded it and failed later
		j.update(true, func(d *JobDTO) { d.BytesDone, d.BytesTotal = fileSize(final), fileSize(final) })
		return final, nil
	}
	var err error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		j.update(true, func(d *JobDTO) { d.Attempt = attempt })
		if err = s.downloadOnce(ctx, j, b.URL, part); err == nil {
			if err = os.Rename(part, final); err != nil {
				return "", err
			}
			return final, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var ae *APIError
		if errors.As(err, &ae) || isDiskFull(err) {
			return "", err // not worth a retry
		}
		if attempt < downloadAttempts {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * s.retryDelay):
			}
		}
	}
	return "", apiErr("engine.download", err.Error(), "attempts", downloadAttempts, "fixId", "retry")
}

func (s *EngineService) downloadOnce(ctx context.Context, j *engineJob, url, part string) error {
	var off int64
	if st, err := os.Stat(part); err == nil {
		off = st.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusOK:
		off = 0
		flags |= os.O_TRUNC
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part)
		return errors.New("partial file does not fit the server file")
	default:
		return fmt.Errorf("download: %s", resp.Status)
	}
	total := int64(0)
	if resp.ContentLength >= 0 {
		total = off + resp.ContentLength
	}
	f, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	j.update(true, func(d *JobDTO) { d.BytesDone, d.BytesTotal = off, total })
	limit := s.cfg.Get().Engines.BandwidthLimit
	start, written := time.Now(), int64(0)
	lastT, lastN := start, off
	buf := make([]byte, 64*1024)
	for {
		if err := j.waitIfPaused(ctx); err != nil {
			return err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			done := off + written
			if now := time.Now(); now.Sub(lastT) >= engineProgressEvery {
				rate := int64(float64(done-lastN) / now.Sub(lastT).Seconds())
				lastT, lastN = now, done
				j.update(false, func(d *JobDTO) { d.BytesDone, d.BytesPerSec = done, rate })
			} else {
				j.update(false, func(d *JobDTO) { d.BytesDone = done })
			}
			if limit > 0 { // sleep until the average rate drops to the limit
				ahead := time.Duration(float64(written)/float64(limit)*float64(time.Second)) - time.Since(start)
				if ahead > 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(ahead):
					}
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if total > 0 && off+written != total {
		return io.ErrUnexpectedEOF
	}
	j.update(true, func(d *JobDTO) { d.BytesDone, d.BytesTotal, d.BytesPerSec = off+written, off+written, 0 })
	return nil
}

func (j *engineJob) waitIfPaused(ctx context.Context) error {
	for {
		j.mu.Lock()
		ch := j.resume
		j.mu.Unlock()
		select {
		case <-ch:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func fileSize(p string) int64 {
	if st, err := os.Stat(p); err == nil {
		return st.Size()
	}
	return 0
}

func fileSHA256(ctx context.Context, p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, &ctxReader{ctx, f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// ---- unzip ------------------------------------------------------------------------

// unzipTo extracts zipPath into dest. It refuses entries that would land
// outside dest (zip slip) and symlinks that point outside it. A damaged entry
// fails with zip.ErrChecksum.
func unzipTo(ctx context.Context, zipPath, dest string) (files int, size int64, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, 0, err
	}
	defer zr.Close()
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return 0, 0, err
	}
	root := filepath.Clean(dest) + string(os.PathSeparator)
	inside := func(p string) bool { return strings.HasPrefix(filepath.Clean(p)+string(os.PathSeparator), root) }
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return files, size, err
		}
		target := filepath.Join(dest, filepath.FromSlash(f.Name))
		if !inside(target) {
			return files, size, apiErr("engine.unpack", "the zip has an entry outside the folder", "name", f.Name)
		}
		mode := f.Mode()
		switch {
		case f.FileInfo().IsDir():
			if err := os.MkdirAll(target, 0o700); err != nil {
				return files, size, err
			}
		case mode&os.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return files, size, err
			}
			link, err := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if err != nil {
				return files, size, err
			}
			if !inside(filepath.Join(filepath.Dir(target), string(link))) || filepath.IsAbs(string(link)) {
				return files, size, apiErr("engine.unpack", "the zip has a link that points outside the folder", "name", f.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return files, size, err
			}
			if err := os.Symlink(string(link), target); err != nil {
				return files, size, err
			}
			files++
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return files, size, err
			}
			n, err := extractFile(ctx, f, target, mode.Perm()|0o600)
			size += n
			if err != nil {
				return files, size, err
			}
			files++
		}
	}
	return files, size, nil
}

func extractFile(ctx context.Context, f *zip.File, target string, perm os.FileMode) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, &ctxReader{ctx, rc})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}
