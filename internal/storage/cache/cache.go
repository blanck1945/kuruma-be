package cache

import (
	"context"
	"time"
)

type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

type MultiLevel struct {
	L1 Cache
	L2 Cache
}

func (m MultiLevel) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if m.L1 != nil {
		v, ok, err := m.L1.Get(ctx, key)
		if err != nil {
			return nil, false, err
		}
		if ok {
			return v, true, nil
		}
	}

	if m.L2 != nil {
		v, ok, err := m.L2.Get(ctx, key)
		if err != nil {
			return nil, false, err
		}
		if ok {
			if m.L1 != nil {
				_ = m.L1.Set(ctx, key, v, 20*time.Second)
			}
			return v, true, nil
		}
	}

	return nil, false, nil
}

func (m MultiLevel) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if m.L1 != nil {
		if err := m.L1.Set(ctx, key, value, minDuration(ttl, 20*time.Second)); err != nil {
			return err
		}
	}
	if m.L2 != nil {
		if err := m.L2.Set(ctx, key, value, ttl); err != nil {
			return err
		}
	}
	return nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

