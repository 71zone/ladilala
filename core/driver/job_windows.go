package driver

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job is a Windows Job object with KILL_ON_JOB_CLOSE: when the app exits or
// crashes, Windows closes the handle and kills every assigned browser and its
// children, so no orphaned Chrome keeps a profile locked.
//
// Assign runs after the browser started, so a helper process Chrome spawned
// in that gap is not in the job. Those helpers exit by themselves when the
// browser dies because the CDP pipe closes.
type Job struct {
	mu sync.Mutex
	h  windows.Handle
}

func NewJob() (*Job, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("driver: create job object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("driver: configure job object: %w", err)
	}
	return &Job{h: h}, nil
}

// Assign adds the process to the job.
func (j *Job) Assign(pid int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.h == 0 {
		return fmt.Errorf("driver: job object is closed")
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ph)
	return windows.AssignProcessToJobObject(j.h, ph)
}

// Close kills every process in the job.
func (j *Job) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.h == 0 {
		return nil
	}
	err := windows.CloseHandle(j.h)
	j.h = 0
	return err
}
