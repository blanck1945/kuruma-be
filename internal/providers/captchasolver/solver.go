package captchasolver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Solver resolves CAPTCHA challenges via an external service.
type Solver interface {
	Solve(ctx context.Context, siteKey, pageURL string) (string, error)
	SolveImage(ctx context.Context, imageData []byte) (string, error)
}

// New returns a Noop solver when apiKey is empty, or a CapSolver solver otherwise.
func New(apiKey string) Solver {
	if strings.TrimSpace(apiKey) == "" {
		return Noop{}
	}
	return &CapSolver{
		APIKey: strings.TrimSpace(apiKey),
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// Noop is a no-op solver that returns an empty token.
type Noop struct{}

func (Noop) Solve(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (Noop) SolveImage(_ context.Context, _ []byte) (string, error) { return "", nil }

// CapSolver resolves reCAPTCHA challenges via the capsolver.com API.
type CapSolver struct {
	APIKey string
	client *http.Client
}

func (s *CapSolver) Solve(ctx context.Context, siteKey, pageURL string) (string, error) {
	// Step 1: create task
	taskID, err := s.createTask(ctx, siteKey, pageURL)
	if err != nil {
		return "", fmt.Errorf("capsolver createTask: %w", err)
	}

	// Step 2: poll for result
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}

		token, ready, err := s.getTaskResult(ctx, taskID)
		if err != nil {
			return "", fmt.Errorf("capsolver getTaskResult: %w", err)
		}
		if ready {
			return token, nil
		}
	}
}

func (s *CapSolver) SolveImage(ctx context.Context, imageData []byte) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(imageData)
	payload := map[string]any{
		"clientKey": s.APIKey,
		"task": map[string]any{
			"type": "ImageToTextTask",
			"body": b64,
		},
	}
	body, err := s.post(ctx, "https://api.capsolver.com/createTask", payload)
	if err != nil {
		return "", fmt.Errorf("capsolver createTask (image): %w", err)
	}
	if body["errorId"] != nil {
		if id, ok := body["errorId"].(float64); ok && id != 0 {
			return "", fmt.Errorf("errorId=%v desc=%v", body["errorId"], body["errorDescription"])
		}
	}
	taskID, ok := body["taskId"].(string)
	if !ok || taskID == "" {
		return "", fmt.Errorf("missing taskId in response")
	}

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
		result, err := s.post(ctx, "https://api.capsolver.com/getTaskResult", map[string]any{
			"clientKey": s.APIKey,
			"taskId":    taskID,
		})
		if err != nil {
			return "", fmt.Errorf("capsolver getTaskResult (image): %w", err)
		}
		if result["errorId"] != nil {
			if id, ok := result["errorId"].(float64); ok && id != 0 {
				return "", fmt.Errorf("errorId=%v desc=%v", result["errorId"], result["errorDescription"])
			}
		}
		status, _ := result["status"].(string)
		if status != "ready" {
			continue
		}
		solution, ok := result["solution"].(map[string]any)
		if !ok {
			return "", fmt.Errorf("missing solution in response")
		}
		text, _ := solution["text"].(string)
		if text == "" {
			return "", fmt.Errorf("empty text in image solution")
		}
		return text, nil
	}
}

func (s *CapSolver) createTask(ctx context.Context, siteKey, pageURL string) (string, error) {
	payload := map[string]any{
		"clientKey": s.APIKey,
		"task": map[string]any{
			"type":       "ReCaptchaV2TaskProxyLess",
			"websiteURL": pageURL,
			"websiteKey": siteKey,
		},
	}
	body, err := s.post(ctx, "https://api.capsolver.com/createTask", payload)
	if err != nil {
		return "", err
	}
	if body["errorId"] != nil {
		if id, ok := body["errorId"].(float64); ok && id != 0 {
			return "", fmt.Errorf("errorId=%v desc=%v", body["errorId"], body["errorDescription"])
		}
	}
	taskID, ok := body["taskId"].(string)
	if !ok || taskID == "" {
		return "", fmt.Errorf("missing taskId in response")
	}
	return taskID, nil
}

func (s *CapSolver) getTaskResult(ctx context.Context, taskID string) (token string, ready bool, err error) {
	payload := map[string]any{
		"clientKey": s.APIKey,
		"taskId":    taskID,
	}
	body, err := s.post(ctx, "https://api.capsolver.com/getTaskResult", payload)
	if err != nil {
		return "", false, err
	}
	if body["errorId"] != nil {
		if id, ok := body["errorId"].(float64); ok && id != 0 {
			return "", false, fmt.Errorf("errorId=%v desc=%v", body["errorId"], body["errorDescription"])
		}
	}
	status, _ := body["status"].(string)
	if status != "ready" {
		return "", false, nil
	}
	solution, ok := body["solution"].(map[string]any)
	if !ok {
		return "", false, fmt.Errorf("missing solution in response")
	}
	token, _ = solution["gRecaptchaResponse"].(string)
	if token == "" {
		return "", false, fmt.Errorf("empty gRecaptchaResponse in solution")
	}
	return token, true, nil
}

func (s *CapSolver) post(ctx context.Context, url string, payload map[string]any) (map[string]any, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %s", string(raw))
	}
	return result, nil
}
