package llm

import (
	"sync"
	"testing"
	"time"
)

func TestBudgetCallLimit(t *testing.T) {
	b := NewBudget(BudgetConfig{MaxCalls: 2})
	for i := 0; i < 2; i++ {
		if err := b.Allow("a"); err != nil {
			t.Fatal(err)
		}
	}
	if b.Allow("a") == nil {
		t.Fatal("third call must be refused")
	}
	if err := b.Allow("other"); err != nil {
		t.Fatal("another investigation must not be affected")
	}
}

func TestBudgetTokenLimits(t *testing.T) {
	b := NewBudget(BudgetConfig{MaxTokensPerInvestigation: 100, MaxTotalTokens: 150})
	_ = b.Allow("a")
	b.Record("a", 100)
	if b.Allow("a") == nil {
		t.Fatal("per-investigation token limit not enforced")
	}
	_ = b.Allow("b")
	b.Record("b", 60)
	if b.Allow("c") == nil {
		t.Fatal("process-wide token limit not enforced")
	}
}

func TestBudgetZeroMeansUnlimited(t *testing.T) {
	b := NewBudget(BudgetConfig{})
	for i := 0; i < 1000; i++ {
		if err := b.Allow("a"); err != nil {
			t.Fatal(err)
		}
		b.Record("a", 1_000_000)
	}
}

func TestBudgetBreaker(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewBudget(BudgetConfig{BreakerFailures: 2, BreakerCooldown: time.Minute})
	b.now = func() time.Time { return now }
	b.Fail()
	b.Fail()
	if b.Allow("x") == nil {
		t.Fatal("breaker should be open")
	}
	now = now.Add(2 * time.Minute)
	if err := b.Allow("x"); err != nil {
		t.Fatal("breaker should close after the cooldown:", err)
	}
	b.Succeed()
	b.Fail()
	if err := b.Allow("x"); err != nil {
		t.Fatal("one failure after a success must not open it:", err)
	}
}

func TestBudgetConcurrent(t *testing.T) {
	b := NewBudget(BudgetConfig{MaxCalls: 1000})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.Allow("a")
			b.Record("a", 10)
			b.Fail()
			b.Succeed()
			b.Forget("a")
		}()
	}
	wg.Wait()
}
