package security

import (
	"strings"
	"testing"
)

func TestGenPassword(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		pw, err := GenPassword(20)
		if err != nil {
			t.Fatalf("ошибка: %v", err)
		}
		if len(pw) != 20 {
			t.Errorf("длина = %d", len(pw))
		}
		for _, c := range pw {
			if !strings.ContainsRune(passwordAlphabet, c) {
				t.Errorf("символ %q вне алфавита", c)
			}
		}
		if seen[pw] {
			t.Error("пароли совпали — подозрительно для crypto/rand")
		}
		seen[pw] = true
	}

	if pw, _ := GenPassword(3); len(pw) != 8 {
		t.Errorf("короткий запрос: длина = %d", len(pw))
	}
}

func TestGuard(t *testing.T) {
	g := NewGuard([]int64{100, 200})
	if !g.Allowed(100) || !g.Allowed(200) || g.Allowed(300) {
		t.Error("Allowed работает неверно")
	}
	if !g.IsSuper(100) || g.IsSuper(200) {
		t.Error("IsSuper работает неверно")
	}

	if list, added := g.Add(300); !added || len(list) != 3 {
		t.Error("Add(300) не сработал")
	}
	if _, added := g.Add(300); added {
		t.Error("повторный Add должен вернуть false")
	}

	if _, ok := g.Remove(100); ok {
		t.Error("суперадмин не должен удаляться")
	}
	if list, ok := g.Remove(200); !ok || len(list) != 2 {
		t.Error("Remove(200) не сработал")
	}
	if g.Allowed(200) {
		t.Error("200 должен быть удалён")
	}
}
