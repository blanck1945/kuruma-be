package providers

import (
	"strings"
	"time"

	"flota/internal/core"
)

func NormalizeResult(input core.FineResult, source string, confidence string) core.FineResult {
	out := input
	out.Source = strings.TrimSpace(source)
	out.Confidence = strings.ToLower(strings.TrimSpace(confidence))
	if out.Confidence == "" {
		out.Confidence = "medium"
	}
	if out.FetchedAt.IsZero() {
		out.FetchedAt = time.Now()
	}

	for i, fine := range out.Fines {
		fine.Currency = strings.ToUpper(strings.TrimSpace(fine.Currency))
		fine.Status = strings.ToUpper(strings.TrimSpace(fine.Status))
		fine.Jurisdiction = strings.ToUpper(strings.TrimSpace(fine.Jurisdiction))
		fine.Source = out.Source
		fine.Vehicle.Plate = strings.ToUpper(strings.TrimSpace(fine.Vehicle.Plate))
		out.Fines[i] = fine
	}
	out.Total = len(out.Fines)
	return out
}

