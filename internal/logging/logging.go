package logging

import (
	"fmt"
	"log"
	"os"
	"sync"
)

type RotatingWriter struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	backups int
	f       *os.File
	size    int64
}

func NewRotatingWriter(path string, maxSize int64, backups int) (*RotatingWriter, error) {
	if maxSize <= 0 {
		maxSize = 10 << 20
	}
	if backups < 1 {
		backups = 3
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &RotatingWriter{path: path, maxSize: maxSize, backups: backups, f: f, size: st.Size()}, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(p)) > w.maxSize {
		if err := w.rotateLocked(); err != nil {

			fmt.Fprintf(os.Stderr, "log rotate: %v\n", err)
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) rotateLocked() error {
	if err := w.f.Close(); err != nil {
		return err
	}

	_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.backups))
	for i := w.backups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	_ = os.Rename(w.path, w.path+".1")

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	w.f = f
	w.size = 0
	return nil
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}

func New(path string, maxSize int64, backups int) (*log.Logger, *RotatingWriter, error) {
	w, err := NewRotatingWriter(path, maxSize, backups)
	if err != nil {
		return nil, nil, err
	}
	return log.New(w, "", log.LstdFlags), w, nil
}
