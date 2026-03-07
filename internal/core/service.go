package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"flota/internal/storage/cache"
)

type ProviderFetcher interface {
	Fetch(ctx context.Context, query Query) (FineResult, error)
}

type Service struct {
	fetcher      ProviderFetcher
	cache        cache.Cache
	softTTL      time.Duration
	hardTTL      time.Duration
	mu           sync.Mutex
	inflight     map[string]*inflightCall
	observeCache func(result string)
}

type inflightCall struct {
	done chan struct{}
	res  FineResult
	err  error
}

func (s *Service) SetCacheObserver(fn func(result string)) {
	s.observeCache = fn
}

type cachedFineResult struct {
	Value    FineResult `json:"value"`
	CachedAt time.Time  `json:"cached_at"`
}

var plateRx = regexp.MustCompile(`^[A-Z]{2,3}[0-9]{3}[A-Z]{0,2}$`)

func NewService(fetcher ProviderFetcher, c cache.Cache, softTTL, hardTTL time.Duration) *Service {
	return &Service{
		fetcher:  fetcher,
		cache:    c,
		softTTL:  softTTL,
		hardTTL:  hardTTL,
		inflight: map[string]*inflightCall{},
	}
}

func (s *Service) SearchFines(ctx context.Context, query Query) (FineResult, error) {
	query.Plate = normalizePlate(query.Plate)
	query.Document = strings.TrimSpace(query.Document)
	query.Source = strings.ToLower(strings.TrimSpace(query.Source))
	query.PBACaptchaToken = strings.TrimSpace(query.PBACaptchaToken)
	if query.Plate == "" && query.Document == "" {
		return FineResult{}, DomainError{Code: ErrInvalidPlate, Message: "plate or document is required"}
	}
	if query.Plate != "" && !isValidPlate(query.Plate) {
		return FineResult{}, DomainError{Code: ErrInvalidPlate, Message: "invalid plate format"}
	}
	if query.Source == "" {
		query.Source = "all"
	}

	cacheKey := fmt.Sprintf("fines:%s:%s:%s", query.Plate, query.Document, query.Source)
	shouldUseCache := query.Source != "pba"
	if shouldUseCache && s.cache != nil {
		payload, ok, err := s.cache.Get(ctx, cacheKey)
		if err == nil && ok {
			if s.observeCache != nil {
				s.observeCache("hit")
			}
			var cached cachedFineResult
			if unmarshalErr := json.Unmarshal(payload, &cached); unmarshalErr == nil {
				age := time.Since(cached.CachedAt)
				if age <= s.hardTTL {
					if age > s.softTTL {
						go s.refreshCache(cacheKey, query)
					}
					return cached.Value, nil
				}
			}
		}
		if s.observeCache != nil {
			s.observeCache("miss")
		}
	}

	return s.doSingleFlight(cacheKey, func() (FineResult, error) {
		res, fetchErr := s.fetcher.Fetch(ctx, query)
		if fetchErr != nil {
			return FineResult{}, fetchErr
		}
		if shouldUseCache && s.cache != nil {
			entry := cachedFineResult{Value: res, CachedAt: time.Now()}
			payload, marshalErr := json.Marshal(entry)
			if marshalErr == nil {
				_ = s.cache.Set(ctx, cacheKey, payload, s.hardTTL)
			}
		}
		return res, nil
	})
}

func (s *Service) refreshCache(cacheKey string, query Query) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = s.doSingleFlight(cacheKey, func() (FineResult, error) {
		res, err := s.fetcher.Fetch(ctx, query)
		if err != nil || s.cache == nil {
			return FineResult{}, err
		}
		entry := cachedFineResult{Value: res, CachedAt: time.Now()}
		payload, marshalErr := json.Marshal(entry)
		if marshalErr == nil {
			_ = s.cache.Set(ctx, cacheKey, payload, s.hardTTL)
		}
		return res, nil
	})
}

func (s *Service) doSingleFlight(key string, fn func() (FineResult, error)) (FineResult, error) {
	s.mu.Lock()
	if existing, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		<-existing.done
		return existing.res, existing.err
	}
	call := &inflightCall{done: make(chan struct{})}
	s.inflight[key] = call
	s.mu.Unlock()

	call.res, call.err = fn()
	close(call.done)

	s.mu.Lock()
	delete(s.inflight, key)
	s.mu.Unlock()
	return call.res, call.err
}

func normalizePlate(plate string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(plate), "-", ""))
}

func isValidPlate(plate string) bool {
	if plate == "" {
		return false
	}
	return plateRx.MatchString(plate)
}
