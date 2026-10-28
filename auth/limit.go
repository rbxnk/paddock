package auth

import (
	"sync"
	"time"
)

// Limiter counts events per key (an IP address, an email) in memory.
// shortcut: counts live in one process, so they reset on restart and aren't shared between
// machines; move them into the database if the app runs on more than one machine.
type Limiter struct {
	mu     sync.Mutex
	events map[string][]time.Time
	swept  time.Time
}

// Allow reports whether key has had fewer than n events within window, and if so records
// one more. A refused event isn't recorded, so waiting always lets the next one in.
func (l *Limiter) Allow(key string, n int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.count(key, window) >= n {
		return false
	}
	l.add(key)
	return true
}

// Over reports whether key has had n or more events within window, without recording one:
// check it before an attempt, and Add only the attempts that count (failed logins).
func (l *Limiter) Over(key string, n int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count(key, window) >= n
}

// Add records an event for key.
func (l *Limiter) Add(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.add(key)
}

func (l *Limiter) count(key string, window time.Duration) int {
	n, t := 0, now()
	for _, e := range l.events[key] {
		if t.Sub(e) < window {
			n++
		}
	}
	return n
}

// add keeps an hour of events per key, so windows can be up to an hour long.
func (l *Limiter) add(key string) {
	if l.events == nil {
		l.events = map[string][]time.Time{}
	}
	t := now()
	recent := l.events[key][:0]
	for _, e := range l.events[key] {
		if t.Sub(e) < time.Hour {
			recent = append(recent, e)
		}
	}
	l.events[key] = append(recent, t)
	if len(l.events) > 100_000 && t.Sub(l.swept) > time.Minute { // a flood of keys: drop stale ones rather than grow
		l.swept = t
		for k, es := range l.events {
			if t.Sub(es[len(es)-1]) >= time.Hour {
				delete(l.events, k)
			}
		}
	}
}
