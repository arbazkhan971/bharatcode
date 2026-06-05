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
	"time"
)

// sleepFn waits for the given duration between retry attempts. Tests replace
// it with a no-op so the backoff path runs offline without real delays.
var sleepFn = time.Sleep

// retryBackoff is the schedule used to space HTTP retries. NoJitter keeps the
// production delays deterministic and the unexported clock defaults to
// time.Now for Retry-After HTTP-date math.
var retryBackoff = Backoff{NoJitter: true}

func postJSON(ctx context.Context, client *http.Client, url string, apiKey string, body any) (*http.Response, error) {
	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "text/event-stream",
	}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	return postJSONWithHeaders(ctx, client, url, headers, body)
}

// postJSONWithHeaders encodes body as JSON and POSTs it to url with the given
// headers, retrying transient failures using retryBackoff. It honors a
// Retry-After header when present, re-sends the body on each attempt, and
// returns a classified error for the final response when retries are
// exhausted.
func postJSONWithHeaders(ctx context.Context, client *http.Client, url string, headers map[string]string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding provider request: %w", err)
	}

	attempts := retryBackoff.Attempts()
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("creating provider request: %w", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}

		if ShouldRetry(status, err) && attempt < attempts {
			delay, retryAfter := retryAfterDelay(resp)
			if !retryAfter {
				delay = retryBackoff.Delay(attempt)
			}
			drainAndClose(resp)
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("sending provider request: %w", ctx.Err())
			default:
			}
			sleepFn(delay)
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("sending provider request: %w", err)
		}
		if resp.StatusCode >= 400 {
			defer resp.Body.Close()
			return nil, classifyHTTPError(resp)
		}
		return resp, nil
	}
}

// retryAfterDelay extracts the Retry-After delay from resp, capping it via the
// backoff schedule. The boolean reports whether a usable header was present.
func retryAfterDelay(resp *http.Response) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}
	return retryBackoff.RetryAfter(resp.Header.Get("Retry-After"))
}

// drainAndClose consumes and closes resp.Body so the underlying connection can
// be reused for the next retry attempt.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	resp.Body.Close()
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
		strings.Contains(s, "too many tokens") ||
		// Gemini phrases an over-budget prompt as "The input token count (X)
		// exceeds the maximum number of tokens allowed (Y)." rather than using the
		// OpenAI/Anthropic wording above, so match its marker too.
		strings.Contains(s, "exceeds the maximum number of tokens") ||
		strings.Contains(s, "input token count")
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
	// When the context is cancelled mid-stream the transport aborts the
	// in-flight Body.Read, so scanner.Scan returns false carrying a
	// connection-closed/net error rather than a wrapped context.Canceled.
	// Check the context unconditionally before inspecting scanner.Err so a
	// cancellation surfaces as ctx.Err() and never as a garbage scan error or a
	// truncated flush of a half-read event. On the clean path ctx.Err is nil and
	// this is a no-op.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("reading provider stream: %w", err)
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("reading provider stream: %w", err)
		}
		return fmt.Errorf("scanning provider stream: %w", err)
	}
	return flush()
}
