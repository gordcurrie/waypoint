//go:build unix

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockQueueFile takes an exclusive flock on the queue's lock file — the same file
// sync.py's _queue_lock flocks — so an append here can't be lost to the sync
// sidecar rewriting the queue from a stale read.
func lockQueueFile(dataDir string) (unlock func(), err error) {
	f, err := os.OpenFile(filepath.Clean(filepath.Join(dataDir, queueLockFile)), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open queue lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock queue: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
