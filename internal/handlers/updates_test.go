package handlers

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFmtBytesValidUTF8(t *testing.T) {
	cases := map[uint64]string{
		0:           "0Б",
		512:         "512Б",
		1024:        "1.0К",
		1536 * 1024: "1.5М",
		5 << 30:     "5.0Г",
		2 << 40:     "2.0Т",
	}
	for in, want := range cases {
		got := FmtBytes(in)
		if got != want {
			t.Errorf("FmtBytes(%d) = %q, ожидалось %q", in, got, want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("FmtBytes(%d) = %q — невалидный UTF-8", in, got)
		}
	}
}

func TestFmtDur(t *testing.T) {
	if got := FmtDur(0); got != "менее 1м" {
		t.Errorf("FmtDur(0) = %q (здесь раньше был «<», ломавший HTML)", got)
	}
	if got := FmtDur(90_000_000_000_000); got == "" {
		t.Error("FmtDur не должен быть пустым")
	}
}

func TestFormatUpgradable(t *testing.T) {
	out := "Listing... Done\nnginx/stable 1.24.0-2 amd64 [upgradable from: 1.24.0-1]\ncurl/stable-security 8.5.0-2+deb12u5 amd64 [upgradable from: 8.5.0-2+deb12u4]\n\n"
	list, n := formatUpgradable(out)
	if n != 2 {
		t.Fatalf("n = %d, ожидалось 2", n)
	}
	if !strings.Contains(list, "nginx: 1.24.0-1 → 1.24.0-2") {
		t.Errorf("строка nginx не распарсилась: %q", list)
	}
	if !strings.Contains(list, "curl: 8.5.0-2+deb12u4 → 8.5.0-2+deb12u5") {
		t.Errorf("строка curl не распарсилась: %q", list)
	}

	if strings.Contains(list, "Listing") {
		t.Errorf("заголовок не отфильтрован: %q", list)
	}
}
