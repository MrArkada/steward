package security

import (
	"crypto/rand"
	"fmt"
	"os"
	"sync"
	"time"
)

type Guard struct {
	mu    sync.RWMutex
	users []int64
}

func NewGuard(users []int64) *Guard {
	cp := make([]int64, len(users))
	copy(cp, users)
	return &Guard{users: cp}
}

func (g *Guard) Allowed(id int64) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, u := range g.users {
		if u == id {
			return true
		}
	}
	return false
}

func (g *Guard) IsSuper(id int64) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.users) > 0 && g.users[0] == id
}

func (g *Guard) List() []int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]int64, len(g.users))
	copy(out, g.users)
	return out
}

func (g *Guard) Add(id int64) ([]int64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, u := range g.users {
		if u == id {
			out := make([]int64, len(g.users))
			copy(out, g.users)
			return out, false
		}
	}
	g.users = append(g.users, id)
	out := make([]int64, len(g.users))
	copy(out, g.users)
	return out, true
}

func (g *Guard) Remove(id int64) ([]int64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.users) == 0 || g.users[0] == id {
		return nil, false
	}
	for i, u := range g.users {
		if u == id {
			g.users = append(g.users[:i], g.users[i+1:]...)
			out := make([]int64, len(g.users))
			copy(out, g.users)
			return out, true
		}
	}
	return nil, false
}

const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#$%*-_=+"

func GenPassword(n int) (string, error) {
	if n < 8 {
		n = 8
	}
	buf := make([]byte, n)
	max := byte(256 - (256 % len(passwordAlphabet)))
	for i := 0; i < n; {
		var b [1]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("crypto/rand: %w", err)
		}
		if b[0] >= max {
			continue
		}
		buf[i] = passwordAlphabet[int(b[0])%len(passwordAlphabet)]
		i++
	}
	return string(buf), nil
}

type AuditLogger struct {
	mu sync.Mutex
	f  *os.File
}

func NewAuditLogger(path string) (*AuditLogger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &AuditLogger{f: f}, nil
}

func (a *AuditLogger) Log(userID int64, action string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	line := fmt.Sprintf("%s user=%d action=%s\n",
		time.Now().Format("2006-01-02 15:04:05"), userID, action)
	_, _ = a.f.WriteString(line)
}

func (a *AuditLogger) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.f.Close()
}
