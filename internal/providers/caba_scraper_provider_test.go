package providers

import (
	"context"
	"os"
	"testing"
	"time"

	"flota/internal/core"
)

func TestParseCABAActas_FromHTMLDataJSON(t *testing.T) {
	html := `
<input class="rowcheckbox" type="checkbox" name="actas[]" value="I0001"
data-json="{&quot;numeroActa&quot;:&quot;I0001&quot;,&quot;fechaActa&quot;:&quot;2025-01-01 10:30&quot;,&quot;tipoActa&quot;:&quot;T: Acta de Transito&quot;,&quot;montoActa&quot;:&quot;39925.5&quot;,&quot;estadoReducidoActa&quot;:&quot;Pago Voluntario&quot;,&quot;infracciones&quot;:[{&quot;desc&quot;:&quot;Violar Luz Roja&quot;,&quot;lugar&quot;:&quot;DEL LIBERTADOR AV. 100&quot;}]}" />`

	fines, err := parseCABAActas(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(fines) != 1 {
		t.Fatalf("expected 1 fine, got %d", len(fines))
	}
	if fines[0].SourceRef != "I0001" {
		t.Fatalf("unexpected source_ref: %s", fines[0].SourceRef)
	}
	if fines[0].Amount <= 0 {
		t.Fatalf("expected positive amount, got %f", fines[0].Amount)
	}
}

func TestCABAScraperProvider_LiveByPlate(t *testing.T) {
	if os.Getenv("RUN_LIVE_CABA_TEST") != "1" {
		t.Skip("set RUN_LIVE_CABA_TEST=1 to run live scraper test")
	}

	p := NewCABAScraperProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := p.Fetch(ctx, core.Query{Plate: "AAA000"})
	if err != nil {
		t.Fatalf("live fetch failed: %v", err)
	}
	if result.Total == 0 {
		t.Fatalf("expected at least one fine for plate AAA000 in live test")
	}
}

