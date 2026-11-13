package llm

import (
	"sync"
	"testing"
	"time"

	"google.golang.org/genai"
)

func newTestClient() *Client {
	return &Client{histories: map[string][]*genai.Content{}, touched: touchMap{}}
}

func TestIdleHistoriesAreSwept(t *testing.T) {
	c := newTestClient()
	c.histories["old"] = []*genai.Content{{}}
	c.touched["old"] = time.Now().Add(-3 * time.Hour)
	c.mu.Lock()
	c.touchLocked("new")
	c.mu.Unlock()
	if _, ok := c.histories["old"]; ok {
		t.Fatal("idle history was not removed")
	}
	if _, ok := c.touched["old"]; ok {
		t.Fatal("idle timestamp was not removed")
	}
}

func TestForgetRemovesHistory(t *testing.T) {
	c := newTestClient()
	c.histories["a"] = []*genai.Content{{}}
	c.touched["a"] = time.Now()
	c.Forget("a")
	if len(c.histories) != 0 || len(c.touched) != 0 {
		t.Fatal("Forget left state behind")
	}
}

func TestHistoryConcurrent(t *testing.T) {
	c := newTestClient()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.mu.Lock()
			c.touchLocked("a")
			c.mu.Unlock()
			c.Forget("a")
		}()
	}
	wg.Wait()
}
