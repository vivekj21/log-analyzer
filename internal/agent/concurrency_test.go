package agent

import (
	"fmt"
	"sync"
	"testing"

	"k8s-log-analyzer/internal/toolbox"
	"k8s-log-analyzer/internal/ttlmap"
)

func TestFetcherScopesConcurrent(t *testing.T) {
	f := &FetcherAgent{investigations: ttlmap.New[toolbox.InvestigationScope](ttlmap.DefaultTTL)}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("inv-%d", i%5)
			f.RegisterInvestigation(id, toolbox.InvestigationScope{})
			f.investigations.Get(id)
			f.Forget(id)
		}(i)
	}
	wg.Wait()
}

func TestAnalyzerTokenConcurrentAgree(t *testing.T) {
	a := NewAnalyzerAgent(nil, nil)
	var wg sync.WaitGroup
	got := make([]string, 50)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], _ = a.tokenFor("same")
		}(i)
	}
	wg.Wait()
	for _, g := range got {
		if g == "" || g != got[0] {
			t.Fatalf("callers disagreed on the token: %q vs %q", g, got[0])
		}
	}
	a.Forget("same")
	if _, ok := a.tokens.Get("same"); ok {
		t.Fatal("Forget did not remove the token")
	}
}
