package cache

import (
	"context"
	"sync"
	"time"
)

type memoryEntry struct {
	value     []byte
	expiresAt time.Time
}

type MemoryCache struct {
	mu    sync.RWMutex
	items map[string]memoryEntry
}

func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		items: make(map[string]memoryEntry),
	}
}

func (m *MemoryCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	entry, ok := m.items[key]
	m.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	if time.Now().After(entry.expiresAt) {
		m.mu.Lock()
		delete(m.items, key)
		m.mu.Unlock()
		return nil, false, nil
	}
	return append([]byte(nil), entry.value...), true, nil
}

func (m *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	m.items[key] = memoryEntry{
		value:     append([]byte(nil), value...),
		expiresAt: time.Now().Add(ttl),
	}
	m.mu.Unlock()
	return nil
}

