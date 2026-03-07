package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"flota/internal/core"
	"flota/internal/providers"
	"flota/internal/providers/captchasolver"
)

func main() {
	plate := "OVR038"
	if len(os.Args) > 1 {
		plate = os.Args[1]
	}

	apiKey := os.Getenv("CAPSOLVER_API_KEY")
	siteKey := os.Getenv("PBA_SITE_KEY")
	staticToken := os.Getenv("PBA_RECAPTCHA_TOKEN")

	solver := captchasolver.New(apiKey)
	p := providers.NewPBAScraperProvider(staticToken, siteKey, solver)

	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
	defer cancel()

	start := time.Now()
	result, err := p.Fetch(ctx, core.Query{Plate: plate})
	elapsed := time.Since(start).Round(time.Millisecond)

	if err != nil {
		fmt.Printf("ERROR (%s): %v\n", elapsed, err)
		os.Exit(1)
	}

	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Printf("OK (%s):\n%s\n", elapsed, string(b))
}
