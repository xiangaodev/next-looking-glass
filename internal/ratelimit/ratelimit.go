// Package ratelimit provides per-IP token buckets and a per-IP concurrency
// guard for expensive diagnostic tasks.
package ratelimit

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Kind selects which bucket applies to a request.
type Kind int

const (
	Light Kind = iota // ping / traceroute / mtr / host
	Heavy             // bench
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter keeps one token bucket per (IP, kind) and a per-IP in-flight lock.
type Limiter struct {
	mu               sync.Mutex
	buckets          map[string]*bucket
	lightMax         float64
	heavyMax         float64
	inflight         map[string]int
	maxInflightPerIP int
	speedTokens      map[string]time.Time // one-time speedtest permits
	speedTokenTTL    time.Duration
}

// New creates a Limiter. Rates are expressed as allowed requests per hour.
func New(lightPerHour, heavyPerHour int) *Limiter {
	l := &Limiter{
		buckets:          make(map[string]*bucket),
		lightMax:         float64(lightPerHour),
		heavyMax:         float64(heavyPerHour),
		inflight:         make(map[string]int),
		maxInflightPerIP: 1,
		speedTokens:      make(map[string]time.Time),
		speedTokenTTL:    2 * time.Minute,
	}
	go l.janitor()
	return l
}

// IssueSpeedToken generates a cryptographically random one-shot permit for IP.
func (l *Limiter) IssueSpeedToken(ip string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: extremely unlikely; use time-based as last resort.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	tok := hex.EncodeToString(b)
	l.speedTokens[ip+":"+tok] = time.Now().Add(l.speedTokenTTL)
	return tok
}

// HasSpeedToken reports whether ip holds a valid permit.
// Tokens are valid for the TTL window; the speedtest (ping→download→upload)
// reuses the same token across phases.
func (l *Limiter) HasSpeedToken(ip, tok string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := ip + ":" + tok
	exp, ok := l.speedTokens[key]
	return ok && time.Now().Before(exp)
}

// Allow reports whether the IP may start a task of the given kind, and if
// not, how long to wait before retrying. A successful call consumes one
// token immediately.
func (l *Limiter) Allow(ip string, k Kind) (bool, time.Duration) {
	if l.lightMax == 0 && l.heavyMax == 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	max := l.lightMax
	if k == Heavy {
		max = l.heavyMax
	}
	if max <= 0 {
		return true, 0
	}

	key := ip + kindSuffix(k)
	b, ok := l.buckets[key]
	now := time.Now()
	if !ok {
		b = &bucket{tokens: max, last: now}
		l.buckets[key] = b
	}

	// Refill: max tokens per hour.
	refill := now.Sub(b.last).Hours() * max
	if refill > 0 {
		b.tokens = min(max, b.tokens+refill)
		b.last = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	// Time until next token.
	need := (1 - b.tokens) / max // fraction of an hour
	return false, time.Duration(need * float64(time.Hour))
}

// Acquire blocks the IP from running more than maxInflightPerIP concurrent
// tasks. Returns a release function; the caller must invoke it when done.
// ok=false means the IP already has a running task.
func (l *Limiter) Acquire(ip string) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight[ip] >= l.maxInflightPerIP {
		return nil, false
	}
	l.inflight[ip]++
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.inflight[ip]--
		if l.inflight[ip] <= 0 {
			delete(l.inflight, ip)
		}
	}, true
}

// janitor periodically removes stale buckets to bound memory.
func (l *Limiter) janitor() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for range t.C {
		l.mu.Lock()
		for k, b := range l.buckets {
			if time.Since(b.last) > 2*time.Hour && b.tokens >= l.maxForKey(k) {
				delete(l.buckets, k)
			}
		}
		now := time.Now()
		for k, exp := range l.speedTokens {
			if now.After(exp) {
				delete(l.speedTokens, k)
			}
		}
		l.mu.Unlock()
	}
}

func (l *Limiter) maxForKey(key string) float64 {
	// key format: "<ip>\x00L" or "<ip>\x00H"
	if n := len(key); n > 0 && key[n-1] == 'H' {
		return l.heavyMax
	}
	return l.lightMax
}

func kindSuffix(k Kind) string {
	if k == Heavy {
		return "\x00H"
	}
	return "\x00L"
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
