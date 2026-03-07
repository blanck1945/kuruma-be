package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Recorder struct {
	mu              sync.RWMutex
	requestDuration map[string]time.Duration
	requestTotal    map[string]uint64
	cacheTotals     map[string]uint64
}

func NewRecorder() *Recorder {
	return &Recorder{
		requestDuration: map[string]time.Duration{},
		requestTotal:    map[string]uint64{},
		cacheTotals:     map[string]uint64{},
	}
}

func (r *Recorder) Observe(path, method, status string, duration time.Duration) {
	k := metricKey(path, method, status)
	r.mu.Lock()
	r.requestDuration[k] += duration
	r.requestTotal[k]++
	r.mu.Unlock()
}

func (r *Recorder) ObserveCache(result string) {
	r.mu.Lock()
	r.cacheTotals[result]++
	r.mu.Unlock()
}

func (r *Recorder) SnapshotText() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	keys := make([]string, 0, len(r.requestTotal))
	for k := range r.requestTotal {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		total := r.requestTotal[k]
		avg := 0.0
		if total > 0 {
			avg = r.requestDuration[k].Seconds() / float64(total)
		}
		parts := strings.SplitN(k, "|", 3)
		b.WriteString(fmt.Sprintf("flota_request_total{path=\"%s\",method=\"%s\",status=\"%s\"} %d\n", parts[0], parts[1], parts[2], total))
		b.WriteString(fmt.Sprintf("flota_request_avg_seconds{path=\"%s\",method=\"%s\",status=\"%s\"} %.6f\n", parts[0], parts[1], parts[2], avg))
	}
	for k, v := range r.cacheTotals {
		b.WriteString(fmt.Sprintf("flota_cache_total{result=\"%s\"} %d\n", k, v))
	}
	return b.String()
}

func metricKey(path, method, status string) string {
	return path + "|" + method + "|" + status
}

