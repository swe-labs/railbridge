package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// clientLimiter tracks a token bucket limiter along with last seen timestamp for TTL eviction.
type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// MemoryRateLimiter implements an in-memory, thread-safe, TTL-evicted token bucket rate limiter.
type MemoryRateLimiter struct {
	mu            sync.Mutex
	tenantLimits  map[string]*clientLimiter
	ipLimits      map[string]*clientLimiter
	ttl           time.Duration
	stopCleaner   chan struct{}
}

// NewMemoryRateLimiter initializes the limiter and starts background garbage collection for idle keys.
func NewMemoryRateLimiter(ttl time.Duration) *MemoryRateLimiter {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	rl := &MemoryRateLimiter{
		tenantLimits: make(map[string]*clientLimiter),
		ipLimits:     make(map[string]*clientLimiter),
		ttl:          ttl,
		stopCleaner:  make(chan struct{}),
	}
	go rl.cleanLoop()
	return rl
}

// Close halts the background cleanup routine.
func (rl *MemoryRateLimiter) Close() {
	select {
	case <-rl.stopCleaner:
	default:
		close(rl.stopCleaner)
	}
}

func (rl *MemoryRateLimiter) cleanLoop() {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			cutoff := time.Now().Add(-rl.ttl)
			for id, entry := range rl.tenantLimits {
				if entry.lastSeen.Before(cutoff) {
					delete(rl.tenantLimits, id)
				}
			}
			for ip, entry := range rl.ipLimits {
				if entry.lastSeen.Before(cutoff) {
					delete(rl.ipLimits, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.stopCleaner:
			return
		}
	}
}

// AllowTenant checks whether an authenticated tenant has tokens available based on their RPM.
func (rl *MemoryRateLimiter) AllowTenant(tenantID string, rpm int) bool {
	if rpm <= 0 {
		rpm = 120
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.tenantLimits[tenantID]
	if !exists {
		limit := rate.Limit(float64(rpm) / 60.0)
		burst := rpm / 6
		if burst < 5 {
			burst = 5
		}
		entry = &clientLimiter{
			limiter:  rate.NewLimiter(limit, burst),
			lastSeen: time.Now(),
		}
		rl.tenantLimits[tenantID] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter.Allow()
}

// AllowIP checks whether a public IP address is within reasonable burst limits (default: 30 RPM, burst 10).
func (rl *MemoryRateLimiter) AllowIP(ip string, rpm int) bool {
	if rpm <= 0 {
		rpm = 30
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.ipLimits[ip]
	if !exists {
		limit := rate.Limit(float64(rpm) / 60.0)
		burst := 10
		entry = &clientLimiter{
			limiter:  rate.NewLimiter(limit, burst),
			lastSeen: time.Now(),
		}
		rl.ipLimits[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter.Allow()
}

// ExtractClientIP extracts the real remote IP, checking X-Forwarded-For and RemoteAddr.
func ExtractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
