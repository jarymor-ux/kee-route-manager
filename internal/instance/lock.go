package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

var ErrLocked = errors.New("another Kee Route Manager controller is running")

type Lock struct {
	file *os.File
	path string
}

func Acquire(runDir string) (*Lock, error) {
	if runDir == "" {
		return nil, fmt.Errorf("run directory is empty")
	}
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(runDir, "controller.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, err
	}
	if err = file.Truncate(0); err == nil {
		_, err = file.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	}
	if err == nil {
		err = file.Sync()
	}
	if err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	return &Lock{file: file, path: path}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
}

func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}
