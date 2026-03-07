package providers

import (
	"context"
	"sort"
	"sync"
	"time"

	"flota/internal/core"
)

type ProviderRouter struct {
	providers []FineProvider
	timeout   time.Duration
	retries   int
	mu        sync.Mutex
	state     map[string]providerState
}

type providerState struct {
	failures    int
	openUntil   time.Time
	lastFailure time.Time
}

func NewProviderRouter(timeout time.Duration, providers ...FineProvider) *ProviderRouter {
	pp := make([]FineProvider, 0, len(providers))
	for _, p := range providers {
		pp = append(pp, p)
	}
	sort.Slice(pp, func(i, j int) bool {
		return pp[i].Priority() < pp[j].Priority()
	})
	return &ProviderRouter{
		providers: pp,
		timeout:   timeout,
		retries:   2,
		state:     map[string]providerState{},
	}
}

func (r *ProviderRouter) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	var firstErr error
	for _, p := range r.providers {
		if !p.Supports(query) {
			continue
		}
		if r.isOpen(p.Name()) {
			if firstErr == nil {
				firstErr = core.DomainError{Code: core.ErrProviderFailed, Message: "provider circuit open"}
			}
			continue
		}

		callCtx := ctx
		cancel := func() {}
		if r.timeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, r.timeout)
		}
		var out core.FineResult
		var err error
		for attempt := 0; attempt <= r.retries; attempt++ {
			out, err = p.Fetch(callCtx, query)
			if err == nil {
				cancel()
				r.markSuccess(p.Name())
				return NormalizeResult(out, out.Source, out.Confidence), nil
			}
			if callCtx.Err() != nil {
				break
			}
			timer := time.NewTimer(time.Duration(attempt+1) * 120 * time.Millisecond)
			select {
			case <-callCtx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
		cancel()
		r.markFailure(p.Name())
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return core.FineResult{}, firstErr
	}
	return core.FineResult{}, core.DomainError{Code: core.ErrNotFound, Message: "no provider returned results"}
}

func (r *ProviderRouter) isOpen(provider string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.state[provider]
	if !ok {
		return false
	}
	return time.Now().Before(st.openUntil)
}

func (r *ProviderRouter) markFailure(provider string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.state[provider]
	st.failures++
	st.lastFailure = time.Now()
	if st.failures >= 3 {
		st.openUntil = time.Now().Add(20 * time.Second)
	}
	r.state[provider] = st
}

func (r *ProviderRouter) markSuccess(provider string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state[provider] = providerState{}
}

