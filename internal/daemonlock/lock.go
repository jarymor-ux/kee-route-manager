// Package daemonlock establishes the exclusive owner before any state is opened.
package daemonlock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Owner struct {
	PID          int       `json:"pid"`
	ProcessStart string    `json:"process_start"`
	StartedAt    time.Time `json:"started_at"`
	InstanceID   string    `json:"instance_id"`
}
type Lock struct {
	file  *os.File
	Owner Owner
	once  sync.Once
}

func Acquire(path string) (*Lock, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("daemon lock path must be absolute")
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	info, e := os.Lstat(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("daemon lock parent must be owned by this user and not writable by group or others")
	}
	fd, e := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	fail := func(e error) (*Lock, error) { _ = f.Close(); return nil, e }
	info, e = f.Stat()
	if e != nil {
		return fail(e)
	}
	if !info.Mode().IsRegular() {
		return fail(fmt.Errorf("daemon lock must be a regular file"))
	}
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fail(fmt.Errorf("another controller owns this state directory: %w", e))
	}
	if e = f.Chmod(0600); e != nil {
		return fail(e)
	}
	b := make([]byte, 16)
	if _, e = rand.Read(b); e != nil {
		return fail(e)
	}
	owner := Owner{PID: os.Getpid(), ProcessStart: processStart(), StartedAt: time.Now().UTC(), InstanceID: hex.EncodeToString(b)}
	if e = f.Truncate(0); e != nil {
		return fail(e)
	}
	if _, e = f.Seek(0, 0); e != nil {
		return fail(e)
	}
	if e = json.NewEncoder(f).Encode(owner); e != nil {
		return fail(e)
	}
	if e = f.Sync(); e != nil {
		return fail(e)
	}
	return &Lock{file: f, Owner: owner}, nil
}
func processStart() string {
	b, e := os.ReadFile("/proc/self/stat")
	if e == nil {
		// comm may contain whitespace and parentheses; fields begin after the last ).
		if i := strings.LastIndexByte(string(b), ')'); i >= 0 {
			fields := strings.Fields(string(b[i+1:]))
			if len(fields) > 19 {
				return "linux-ticks:" + fields[19]
			}
		}
	}
	// Non-Linux hosts remain useful for development; PID + random instance ID is
	// authoritative only while the kernel lock is held.
	return "wall-ns:" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
func (l *Lock) Close() error {
	var e error
	l.once.Do(func() { e = l.file.Close() })
	// Never unlink the lock: doing so allows contenders to lock another inode.
	return e
}
