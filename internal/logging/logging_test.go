package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.log")
	w, err := NewRotatingWriter(p, 1024, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	chunk := strings.Repeat("x", 512) + "\n"
	for i := 0; i < 6; i++ {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if _, err := os.Stat(p + ".1"); err != nil {
		t.Error("бэкап .1 не создан")
	}

	if _, err := os.Stat(p + ".3"); !os.IsNotExist(err) {
		t.Error("бэкап .3 не должен существовать при backups=2")
	}

	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 2048 {
		t.Errorf("текущий лог = %d байт, ротация не сработала", st.Size())
	}
}
