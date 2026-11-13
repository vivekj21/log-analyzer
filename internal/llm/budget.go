package llm

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s-log-analyzer/internal/ttlmap"
)

// BudgetConfig limits what one process may spend. A value of 0 disables that limit.
type BudgetConfig struct {
	MaxCalls                  int   // model calls per investigation
	MaxTokensPerInvestigation int64 // tokens per investigation
	MaxTotalTokens            int64 // tokens per process
	BreakerFailures           int   // consecutive failed calls that open the breaker
	BreakerCooldown           time.Duration
}

type budgetUsage struct {
	calls  int
	tokens int64
}

// Budget counts model calls and tokens and pauses calls after repeated failures.
type Budget struct {
	cfg       BudgetConfig
	mu        sync.Mutex
	now       func() time.Time
	usage     *ttlmap.Map[*budgetUsage]
	total     int64
	failures  int
	openUntil time.Time
}

func NewBudget(cfg BudgetConfig) *Budget {
	return &Budget{cfg: cfg, now: time.Now, usage: ttlmap.New[*budgetUsage](ttlmap.DefaultTTL)}
}

func budgetEnvInt(name string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		log.Printf("[Budget] ignoring invalid %s=%q, using %d", name, v, def)
		return def
	}
	return n
}

func NewBudgetFromEnv() *Budget {
	return NewBudget(BudgetConfig{
		MaxCalls:                  int(budgetEnvInt("LOG_ANALYZER_MAX_CALLS", 30)),
		MaxTokensPerInvestigation: budgetEnvInt("LOG_ANALYZER_MAX_TOKENS", 400000),
		MaxTotalTokens:            budgetEnvInt("LOG_ANALYZER_MAX_TOTAL_TOKENS", 4000000),
		BreakerFailures:           int(budgetEnvInt("LOG_ANALYZER_BREAKER_FAILURES", 5)),
		BreakerCooldown:           time.Duration(budgetEnvInt("LOG_ANALYZER_BREAKER_COOLDOWN_SECONDS", 60)) * time.Second,
	})
}

// Allow must be called before each model call.
func (b *Budget) Allow(id string) error {
	u := b.usage.GetOrSet(id, &budgetUsage{})
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	c := b.cfg
	if c.BreakerFailures > 0 && b.failures >= c.BreakerFailures && now.Before(b.openUntil) {
		return fmt.Errorf("circuit breaker open: %d model calls failed in a row; pausing until %s", b.failures, b.openUntil.Format("15:04:05"))
	}
	if c.MaxCalls > 0 && u.calls >= c.MaxCalls {
		return fmt.Errorf("budget exceeded: this investigation already made %d model calls (limit %d)", u.calls, c.MaxCalls)
	}
	if c.MaxTokensPerInvestigation > 0 && u.tokens >= c.MaxTokensPerInvestigation {
		return fmt.Errorf("budget exceeded: this investigation already used %d tokens (limit %d)", u.tokens, c.MaxTokensPerInvestigation)
	}
	if c.MaxTotalTokens > 0 && b.total >= c.MaxTotalTokens {
		return fmt.Errorf("budget exceeded: this process already used %d tokens (limit %d)", b.total, c.MaxTotalTokens)
	}
	u.calls++
	return nil
}

func (b *Budget) Record(id string, tokens int64) {
	u := b.usage.GetOrSet(id, &budgetUsage{})
	b.mu.Lock()
	defer b.mu.Unlock()
	u.tokens += tokens
	b.total += tokens
}

func (b *Budget) Fail() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.cfg.BreakerFailures > 0 && b.failures >= b.cfg.BreakerFailures {
		b.openUntil = b.now().Add(b.cfg.BreakerCooldown)
	}
}

func (b *Budget) Succeed() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
}

func (b *Budget) Forget(id string) { b.usage.Delete(id) }
