package agent

import (
	"context"
	"fmt"
	"sync"

	"k8s-log-analyzer/internal/protocol"
)

// Request is one investigation to run.
type Request struct {
	Namespace string
	Service   string
	Symptom   string
	Since     string
}

// Service runs investigations one call at a time on top of the running agents and waits for
// each result. The command line does not use it; `log-analyzer serve` does.
type Service struct {
	bus     *protocol.AgentBus
	p       *ProducerAgent
	mu      sync.Mutex
	waiters map[string]chan Result
}

// NewService connects to a producer. It takes over the producer's result handler.
func NewService(bus *protocol.AgentBus, p *ProducerAgent) *Service {
	s := &Service{bus: bus, p: p, waiters: map[string]chan Result{}}
	p.SetResultHandler(s.onResult)
	return s
}

func (s *Service) onResult(r Result) {
	s.mu.Lock()
	ch := s.waiters[r.ID]
	delete(s.waiters, r.ID)
	s.mu.Unlock()
	if ch != nil {
		ch <- r // buffered, never blocks
	}
}

// Investigate starts an investigation with the given id and waits for its result. If ctx ends
// first (timeout or shutdown), the investigation is cancelled and ctx's error is returned.
func (s *Service) Investigate(ctx context.Context, id string, req Request) (Result, error) {
	ch := make(chan Result, 1)
	s.mu.Lock()
	if _, busy := s.waiters[id]; busy {
		s.mu.Unlock()
		return Result{}, fmt.Errorf("investigation %s is already running", id)
	}
	s.waiters[id] = ch
	s.mu.Unlock()

	forget := func() {
		s.mu.Lock()
		delete(s.waiters, id)
		s.mu.Unlock()
	}
	msg := protocol.NewMessage(protocol.MsgInvestigate, id, "serve", "producer",
		protocol.InvestigatePayload{Symptom: req.Symptom, Namespace: req.Namespace, Service: req.Service, Since: req.Since})
	select {
	case s.bus.ProducerCh <- msg:
	case <-ctx.Done():
		forget()
		return Result{}, ctx.Err()
	}
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		forget()
		s.p.Cancel(id)
		return Result{}, ctx.Err()
	}
}
