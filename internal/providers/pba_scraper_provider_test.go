package providers

import (
	"testing"
)

func TestParsePBAResponse_WithInfracciones(t *testing.T) {
	body := []byte(`{
		"error": false,
		"infracciones": [
			{
				"nroCausa": "PBA-1001",
				"descripcionInfraccion": "Exceso de velocidad",
				"monto": "125000,50",
				"estadoDesc": "Pendiente",
				"fechaInfraccion": "2025-02-01 11:30:00",
				"juzgado": "Moron",
				"tieneFoto": true
			}
		]
	}`)

	fines, err := parsePBAResponse(body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(fines) != 1 {
		t.Fatalf("expected 1 fine, got %d", len(fines))
	}
	if fines[0].SourceRef != "PBA-1001" {
		t.Fatalf("unexpected source_ref: %s", fines[0].SourceRef)
	}
	if fines[0].Jurisdiction != "PBA - Moron" {
		t.Fatalf("unexpected jurisdiction: %s", fines[0].Jurisdiction)
	}
	if fines[0].Amount <= 0 {
		t.Fatalf("expected positive amount, got %f", fines[0].Amount)
	}
	if !fines[0].HasPhoto {
		t.Fatalf("expected has_photo true")
	}
}

func TestParsePBAResponse_EmptyWithoutError(t *testing.T) {
	body := []byte(`{"error": false, "infracciones": []}`)

	fines, err := parsePBAResponse(body)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(fines) != 0 {
		t.Fatalf("expected zero fines, got %d", len(fines))
	}
}

func TestParsePBAResponse_ErrorRequiresCaptcha(t *testing.T) {
	body := []byte(`{"error": true}`)

	_, err := parsePBAResponse(body)
	if err == nil {
		t.Fatalf("expected provider error for captcha-required response")
	}
}
