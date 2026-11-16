//go:build !windows

package driver

// Job ties browser processes to the app's lifetime. Outside Windows there is
// no Job object; configureCmd puts each browser in its own process group and
// Linux adds a parent-death signal, so this type has nothing to do.
type Job struct{}

func NewJob() (*Job, error) { return &Job{}, nil }

func (*Job) Assign(pid int) error { return nil }

func (*Job) Close() error { return nil }
