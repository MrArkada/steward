package sysutil

import "testing"

func TestParseSSProcess(t *testing.T) {
	cases := map[string]string{
		`users:(("nginx",pid=123,fd=6))`:                          "nginx",
		`users:(("postgres",pid=1,fd=3),("postgres",pid=2,fd=4))`: "postgres",
		`-`:   "",
		`xyz`: "",
		"":    "",
	}
	for in, want := range cases {
		if got := parseSSProcess(in); got != want {
			t.Errorf("parseSSProcess(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestCmdErrorShort(t *testing.T) {
	e := &CmdError{Cmd: "apt-get update", Output: "\nE: Could not get lock\nmore\n"}
	if got := e.Short(); got != "E: Could not get lock" {
		t.Errorf("Short() = %q", got)
	}
	e2 := &CmdError{Cmd: "x", Timeout: true}
	if got := e2.Short(); got != "превышено время ожидания" {
		t.Errorf("Short() = %q", got)
	}
}
