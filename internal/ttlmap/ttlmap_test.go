package ttlmap

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestExpiryAndRefresh(t *testing.T) {
	m := New[string](time.Minute)
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	m.Set("a", "1")
	now = now.Add(50 * time.Second)
	if v, ok := m.Get("a"); !ok || v != "1" {
		t.Fatal("expected a")
	}
	now = now.Add(50 * time.Second)
	if _, ok := m.Get("a"); !ok {
		t.Fatal("Get should refresh the idle timer")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := m.Get("a"); ok {
		t.Fatal("expected expiry")
	}
}

func TestSweepOnSet(t *testing.T) {
	m := New[int](time.Minute)
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	m.Set("old", 1)
	now = now.Add(2 * time.Minute)
	m.Set("new", 2)
	if m.Len() != 1 {
		t.Fatalf("expected 1 entry after sweep, got %d", m.Len())
	}
}

func TestGetOrSetKeepsFirstValue(t *testing.T) {
	m := New[string](time.Hour)
	if m.GetOrSet("k", "first") != "first" || m.GetOrSet("k", "second") != "first" {
		t.Fatal("GetOrSet must keep the first value")
	}
}

func TestConcurrentUse(t *testing.T) {
	m := New[int](time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := fmt.Sprintf("k%d", i%5)
			m.Set(k, i)
			m.Get(k)
			m.GetOrSet(k, i)
			m.Len()
			m.Delete(k)
		}(i)
	}
	wg.Wait()
}
