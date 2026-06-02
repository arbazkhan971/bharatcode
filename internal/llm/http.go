package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func postJSON(ctx context.Context, client *http.Client, url string, apiKey string, body any) (*http.Response, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, fmt.Errorf("encoding provider request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("creating provider request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending provider request: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, classifyHTTPError(resp)
	}
	return resp, nil
}

func classifyHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	providerErr := parseProviderError(body)
	if providerErr != nil {
		return providerErr
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("provider returned %d: %w", resp.StatusCode, ErrAuth)
	case http.StatusTooManyRequests:
		return fmt.Errorf("provider returned %d: %w", resp.StatusCode, ErrRateLimit)
	case http.StatusNotFound:
		return fmt.Errorf("provider returned %d: %w", resp.StatusCode, ErrModelNotFound)
	case http.StatusBadRequest:
		if mentionsContextLimit(string(body)) {
			return fmt.Errorf("provider returned %d: %w", resp.StatusCode, ErrContextLimit)
		}
		return fmt.Errorf("provider returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	default:
		if resp.StatusCode >= 500 {
			return fmt.Errorf("provider returned %d: %w", resp.StatusCode, ErrServer)
		}
		return fmt.Errorf("provider returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func parseProviderError(body []byte) error {
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}

	code := strings.ToLower(envelope.Error.Code)
	typ := strings.ToLower(envelope.Error.Type)
	msg := strings.ToLower(envelope.Error.Message)
	switch {
	case code == "rate_limit_exceeded" || typ == "rate_limit_error":
		return fmt.Errorf("provider rate limited request: %w", ErrRateLimit)
	case code == "context_length_exceeded" || strings.Contains(msg, "context length") || strings.Contains(msg, "maximum context"):
		return fmt.Errorf("provider context limit: %w", ErrContextLimit)
	case code == "model_not_found" || typ == "not_found_error":
		return fmt.Errorf("provider model lookup: %w", ErrModelNotFound)
	case code == "invalid_api_key" || typ == "authentication_error" || typ == "permission_error":
		return fmt.Errorf("provider authentication: %w", ErrAuth)
	default:
		return nil
	}
}

func mentionsContextLimit(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "context length") ||
		strings.Contains(s, "maximum context") ||
		strings.Contains(s, "too many tokens")
}

type sseEvent struct {
	Name string
	Data string
}

func readSSE(ctx context.Context, r io.Reader, handle func(sseEvent) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024), 1024*1024)

	var name string
	var data []string
	flush := func() error {
		if len(data) == 0 {
			name = ""
			return nil
		}
		ev := sseEvent{Name: name, Data: strings.Join(data, "\n")}
		name = ""
		data = nil
		return handle(ev)
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return fmt.Errorf("reading provider stream: %w", ctx.Err())
		default:
		}

		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			data = append(data, value)
		case "retry":
			_, _ = strconv.Atoi(value)
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("reading provider stream: %w", err)
		}
		return fmt.Errorf("scanning provider stream: %w", err)
	}
	return flush()
}
