// Package ttlmap is a small concurrency-safe map whose idle entries expire.
package ttlmap

import (
	"sync"
	"time"
)

const DefaultTTL = 2 * time.Hour

type entry[V any] struct {
	v  V
	at time.Time
}

type Map[V any] struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]entry[V]
	now func() time.Time
}

func New[V any](ttl time.Duration) *Map[V] {
	return &Map[V]{ttl: ttl, m: map[string]entry[V]{}, now: time.Now}
}

func (t *Map[V]) sweepLocked(now time.Time) {
	for k, e := range t.m {
		if now.Sub(e.at) > t.ttl {
			delete(t.m, k)
		}
	}
}

// Get returns the value and refreshes its idle timer.
func (t *Map[V]) Get(k string) (V, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	e, ok := t.m[k]
	if !ok || now.Sub(e.at) > t.ttl {
		delete(t.m, k)
		var zero V
		return zero, false
	}
	e.at = now
	t.m[k] = e
	return e.v, true
}

func (t *Map[V]) Set(k string, v V) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.sweepLocked(now)
	t.m[k] = entry[V]{v: v, at: now}
}

// GetOrSet returns the existing value, or stores and returns v. Atomic.
func (t *Map[V]) GetOrSet(k string, v V) V {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if e, ok := t.m[k]; ok && now.Sub(e.at) <= t.ttl {
		e.at = now
		t.m[k] = e
		return e.v
	}
	t.sweepLocked(now)
	t.m[k] = entry[V]{v: v, at: now}
	return v
}

func (t *Map[V]) Delete(k string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, k)
}

func (t *Map[V]) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.m)
}
