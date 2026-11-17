package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/8bu/ladilala/core/driver"
)

// fingerprint-chromium (engine ID "fpc") is a pinned download: one tag in the
// driver.FPCBuilds table, checked against its SHA-256 before it is unpacked.
// It has no update check, no old versions and no rollback.

func (s *EngineService) fpcBuild(tag string) (driver.FPCBuild, bool) {
	for _, b := range s.fpcBuilds {
		if tag == "" || b.Tag == tag {
			return b, true
		}
	}
	return driver.FPCBuild{}, false
}

// fpcInstalled returns the first pinned build that is unpacked.
func (s *EngineService) fpcInstalled() (driver.FPCMeta, driver.Install, bool) {
	for _, b := range s.fpcBuilds {
		if m, in, ok := driver.FPCInstalled(s.fpcDir, b.Tag); ok {
			return m, in, true
		}
	}
	return driver.FPCMeta{}, driver.Install{}, false
}

func (s *EngineService) partialKept(engineID, version string) bool {
	if engineID == engineFPC {
		_, ok := driver.FPCPartial(s.fpcDir, version)
		return ok
	}
	return fileExists(s.partPath(version))
}

// fpcDTO is the Engines screen row of fingerprint-chromium.
func (s *EngineService) fpcDTO(def string) EngineDTO {
	b := s.fpcBuilds[0]
	s.mu.Lock()
	jobID := s.runningJobLocked(engineFPC)
	s.mu.Unlock()
	e := EngineDTO{
		ID: engineFPC, Tier: 2, Name: "fingerprint-chromium", Status: "missing", Path: s.fpcDir,
		Installed: []EngineVersion{}, Default: def == engineFPC, JobID: jobID,
		NotesURL:      "https://github.com/adryfish/fingerprint-chromium/releases/tag/" + b.Tag,
		DownloadBytes: b.Size, DiskBytes: driver.FPCDiskBytes, ChromiumMajor: b.ChromiumMajor,
		Notes: []string{"old", "unsupported"},
	}
	if !s.fpcOK {
		e.Status = "unavailable"
		return e
	}
	if m, in, ok := s.fpcInstalled(); ok {
		dir := driver.FPCTagDir(s.fpcDir, m.Tag)
		e.Status, e.Path, e.Active = "ready", in.Path, m.Tag
		e.Installed = []EngineVersion{{Version: m.Tag, SizeBytes: dirSize(dir), Path: dir, SHA256: m.SHA256, Active: true}}
	}
	return e
}

// executeFPC is the install job of fingerprint-chromium. The stages are those
// of the Chrome for Testing job, so the same dialog shows them.
func (s *EngineService) executeFPC(ctx context.Context, j *engineJob) error {
	d := j.snapshot()

	// 1 check: the build is pinned, so there is nothing to contact.
	j.stage("check", "active", "pinned", nil)
	build, ok := s.fpcBuild(d.Version)
	if !ok {
		return apiErr("engine.versionGone", "this version is not offered", "version", d.Version)
	}
	j.update(true, func(d *JobDTO) { d.Version = build.Tag })
	if d.Kind == "redownload" {
		if err := driver.RemoveFPC(s.fpcDir, build.Tag); err != nil {
			return err
		}
	} else if _, _, ok := driver.FPCInstalled(s.fpcDir, build.Tag); ok {
		return apiErr("engine.installed", "this version is already installed", "version", build.Tag)
	}
	j.stage("check", "done", "pinned", map[string]any{"version": build.Tag, "major": build.ChromiumMajor})

	// 2 to 4: download, verify, unpack.
	var attempts int
	hooks, reset := s.fetchHooks(j)
	_, err := driver.InstallFPC(ctx, driver.FPCInstallOptions{
		Dir: s.fpcDir, Build: build, Client: s.client, RetryDelay: s.retryDelay, Hooks: hooks,
		Attempt: func(n int) { attempts = n; reset(); j.update(true, func(d *JobDTO) { d.Attempt = n }) },
		Stage: func(stage string) {
			switch stage {
			case driver.FPCStageDownload:
				j.stage("download", "active", "", nil)
			case driver.FPCStageVerify:
				j.update(true, func(d *JobDTO) { d.BytesDone, d.BytesTotal, d.BytesPerSec = build.Size, build.Size, 0 })
				j.stage("download", "done", "downloaded", map[string]any{"bytes": build.Size, "attempts": attempts})
				j.stage("verify", "active", "computing", nil)
			case driver.FPCStageUnpack:
				j.stage("verify", "done", "shaPinned", map[string]any{"sha256": build.SHA256})
				j.stage("unpack", "active", "extracting", nil)
			}
		},
		Unpacked: func(files int, bytes int64) {
			j.stage("unpack", "done", "unpacked", map[string]any{"files": files, "bytes": bytes})
		},
	})
	if err != nil {
		return err
	}

	// 5 self-test: the browser starts, answers over the CDP pipe and survives
	// a canvas and WebGL read.
	j.stage("selftest", "active", "testing", nil)
	final := driver.FPCTagDir(s.fpcDir, build.Tag)
	_, inst, ok := driver.FPCInstalled(s.fpcDir, build.Tag)
	if !ok {
		_ = os.RemoveAll(final)
		return apiErr("engine.unpack", "the zip has no browser binary")
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_, perr := s.probe(pctx, inst)
	cancel()
	if perr != nil {
		_ = os.RemoveAll(final) // nothing stays installed
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return selftestError(perr)
	}
	j.stage("selftest", "done", "selftestOk", nil)

	// 6 switch: there is one version, so it is simply the one in use.
	j.stage("switch", "active", "switching", nil)
	s.applyDefault()
	j.stage("switch", "done", "active", map[string]any{"version": build.Tag})
	return nil
}

// removeFPC deletes the installed build. A run in progress blocks it, as for
// Chrome for Testing.
func (s *EngineService) removeFPC() error {
	s.mu.Lock()
	if s.runningJobLocked(engineFPC) != "" {
		s.mu.Unlock()
		return apiErr("conflict", "an install is running", "reason", "jobRunning")
	}
	if s.runsActive() {
		s.mu.Unlock()
		return apiErr("conflict", "a run is using the browser", "reason", "runActive")
	}
	var err error
	for _, b := range s.fpcBuilds {
		if e := driver.RemoveFPC(s.fpcDir, b.Tag); e != nil {
			err = e
		}
	}
	s.mu.Unlock()
	if err != nil {
		return apiErr("engine.inUse", err.Error())
	}
	return nil
}

// fpcFreeSpace deletes the leftover downloads and staging folders.
func (s *EngineService) fpcFreeSpace() {
	_ = os.RemoveAll(filepath.Join(s.fpcDir, "downloads"))
	for _, b := range s.fpcBuilds {
		_ = os.RemoveAll(driver.FPCTagDir(s.fpcDir, b.Tag) + ".staging")
	}
}
