package geoip

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"serverbot/internal/storage"
)

const cacheTTL = 7 * 24 * time.Hour

const rateLimit = 1400 * time.Millisecond

type Cache struct {
	store   *storage.Storage
	enabled bool
	client  *http.Client
	mu      sync.Mutex
	lastReq time.Time
}

func New(store *storage.Storage, enabled bool) *Cache {
	return &Cache{
		store:   store,
		enabled: enabled,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

type apiResponse struct {
	Status  string `json:"status"`
	Country string `json:"country"`
	City    string `json:"city"`
}

func (c *Cache) Lookup(ctx context.Context, ip string) string {
	if !c.enabled || c.store == nil {
		return ""
	}
	if isPrivateIP(ip) {
		return "приватный IP"
	}
	if e, ok := c.store.GeoGet(ip); ok && time.Since(e.At) < cacheTTL {
		return format(e.Country, e.City)
	}

	c.mu.Lock()
	wait := time.Until(c.lastReq.Add(rateLimit))
	c.lastReq = time.Now()
	c.mu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(wait):
		}
	}

	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,country,city&lang=ru", ip)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var ar apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil || ar.Status != "success" {
		return ""
	}
	_ = c.store.Update(func(st *storage.State) {
		if st.GeoCache == nil {
			st.GeoCache = make(map[string]storage.GeoEntry)
		}

		if len(st.GeoCache) >= 2000 {
			n := 0
			for k := range st.GeoCache {
				delete(st.GeoCache, k)
				if n++; n >= 500 {
					break
				}
			}
		}
		st.GeoCache[ip] = storage.GeoEntry{Country: ar.Country, City: ar.City, At: time.Now()}
	})
	return format(ar.Country, ar.City)
}

func format(country, city string) string {
	if city == "" {
		return country
	}
	if country == "" {
		return city
	}
	return country + ", " + city
}

func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return true
	}
	return parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() || parsed.IsUnspecified()
}
