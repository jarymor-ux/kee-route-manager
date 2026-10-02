// Package logging implements a bounded, private rotating runtime log.
package logging

import (
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

type Writer struct {
	mu      sync.Mutex
	path    string
	limit   int64
	backups int
	file    *os.File
	size    int64
}

func New(path string, limit int64, backups int) (*Writer, error) {
	if limit <= 0 || backups < 1 || backups > 10 {
		return nil, fmt.Errorf("invalid log retention")
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	w := &Writer{path: path, limit: limit, backups: backups}
	if e := w.open(); e != nil {
		return nil, e
	}
	return w, nil
}
func (w *Writer) open() error {
	if s, e := os.Lstat(w.path); e == nil && !s.Mode().IsRegular() {
		return fmt.Errorf("log must be a regular file")
	}
	f, e := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	s, e := f.Stat()
	if e != nil {
		f.Close()
		return e
	}
	w.file = f
	w.size = s.Size()
	return nil
}
func (w *Writer) rotate() error {
	if e := w.file.Close(); e != nil {
		return e
	}
	if e := os.Remove(fmt.Sprintf("%s.%d", w.path, w.backups)); e != nil && !os.IsNotExist(e) {
		return e
	}
	for i := w.backups - 1; i >= 1; i-- {
		if e := os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1)); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	if e := os.Rename(w.path, w.path+".1"); e != nil {
		return e
	}
	w.size = 0
	return w.open()
}
func (w *Writer) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	original := len(b)
	// Preserve the tail of oversized records while keeping the file strictly bounded.
	if int64(len(b)) > w.limit {
		b = b[len(b)-int(w.limit):]
	}
	if w.size+int64(len(b)) > w.limit {
		if e := w.rotate(); e != nil {
			return 0, e
		}
	}
	n, e := w.file.Write(b)
	w.size += int64(n)
	if e != nil {
		return n, e
	}
	return original, nil
}
func (w *Writer) Close() error { w.mu.Lock(); defer w.mu.Unlock(); return w.file.Close() }
func Setup(path string) (io.Closer, error) {
	if path == "" {
		return io.NopCloser(nilReader{}), nil
	}
	w, e := New(path, 1<<20, 3)
	if e != nil {
		return nil, e
	}
	log.SetOutput(redactingWriter{target: io.MultiWriter(os.Stderr, w)})
	return w, nil
}

type nilReader struct{}

func (nilReader) Read([]byte) (int, error) { return 0, io.EOF }

type redactingWriter struct{ target io.Writer }

func (w redactingWriter) Write(b []byte) (int, error) {
	sanitized := []byte(redact.Text(string(b)))
	_, err := w.target.Write(sanitized)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}
