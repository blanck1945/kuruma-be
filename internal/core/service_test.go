package core

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type fakeFetcher struct {
	mu    sync.Mutex
	count int
}

func (f *fakeFetcher) Fetch(_ context.Context, query Query) (FineResult, error) {
	f.mu.Lock()
	f.count++
	f.mu.Unlock()
	return FineResult{
		Fines: []Fine{
			{
				Vehicle:      VehicleInfo{Plate: query.Plate},
				Jurisdiction: "CABA",
				Offense:      "Test offense",
				Amount:       1,
				Currency:     "ARS",
				Status:       "PENDING",
				IssuedAt:     time.Now(),
				Source:       "fake",
				SourceRef:    "x",
			},
		},
		Total:      1,
		Source:     "fake",
		Confidence: "high",
		FetchedAt:  time.Now(),
	}, nil
}

type fakeCache struct {
	mu    sync.Mutex
	items map[string][]byte
}

func (f *fakeCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.items[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (f *fakeCache) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.items == nil {
		f.items = map[string][]byte{}
	}
	f.items[key] = append([]byte(nil), value...)
	return nil
}

func TestSearchFines_InvalidPlate(t *testing.T) {
	svc := NewService(&fakeFetcher{}, nil, time.Second, time.Minute)
	_, err := svc.SearchFines(context.Background(), Query{Plate: "BAD"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestSearchFines_UsesCacheAfterFirstCall(t *testing.T) {
	fetch := &fakeFetcher{}
	cache := &fakeCache{items: map[string][]byte{}}
	svc := NewService(fetch, cache, 10*time.Second, time.Minute)

	_, err := svc.SearchFines(context.Background(), Query{Plate: "AAA000"})
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	_, err = svc.SearchFines(context.Background(), Query{Plate: "AAA000"})
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	fetch.mu.Lock()
	defer fetch.mu.Unlock()
	if fetch.count != 1 {
		t.Fatalf("expected 1 fetch call, got %d", fetch.count)
	}

	var parsed cachedFineResult
	for _, v := range cache.items {
		if err := json.Unmarshal(v, &parsed); err != nil {
			t.Fatalf("cache payload invalid json: %v", err)
		}
	}
}

func TestSearchFines_AllowsDocumentWithoutPlate(t *testing.T) {
	fetch := &fakeFetcher{}
	svc := NewService(fetch, nil, 10*time.Second, time.Minute)

	_, err := svc.SearchFines(context.Background(), Query{Document: "30111222"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

