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

// appendPath joins an endpoint path (such as "/chat/completions") onto a
// provider base URL while preserving any query string already present on the
// base. Azure OpenAI endpoints encode a required api-version on the base URL
// (".../deployments/<name>?api-version=2024-06-01"), so a naive base+path concat
// would place the path after the query — ".../<name>?api-version=2024-06-01/chat/completions"
// — yielding an invalid URL that Azure rejects. Splitting on the first "?" keeps
// the query trailing the inserted path. A base without a query is concatenated
// unchanged, so non-Azure providers are unaffected.
func appendPath(base, path string) string {
	if i := strings.IndexByte(base, '?'); i >= 0 {
		return base[:i] + path + base[i:]
	}
	return base + path
}

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

// azureOpenAIHostSuffix marks Azure OpenAI endpoints. Azure authenticates an API
// key through the "api-key" request header, unlike every other OpenAI-dialect
// provider, which uses the "Authorization: Bearer" scheme. The deployment-scoped
// base URL (".../openai/deployments/<name>?api-version=...") always carries this
// host, so the base URL alone selects the auth scheme.
const azureOpenAIHostSuffix = ".openai.azure.com"

// azureCognitiveServicesHostSuffix marks the other host Azure OpenAI is served on:
// resources created through the Azure AI Foundry portal default to a multi-service
// "Cognitive Services" / "AI Services" account whose OpenAI route lives at
// ".../cognitiveservices.azure.com/openai/deployments/<name>?api-version=...". That
// route authenticates with the same "api-key" header as the classic
// ".openai.azure.com" host, so it must select the Azure auth scheme too — otherwise
// the key goes out as a Bearer token the endpoint rejects.
const azureCognitiveServicesHostSuffix = ".cognitiveservices.azure.com"

// isAzureOpenAI reports whether baseURL points at an Azure OpenAI endpoint. The
// match is a case-insensitive substring scan so it also recognizes the
// sovereign-cloud hosts (".openai.azure.us", ".openai.azure.cn") that share the
// "openai.azure" marker but differ in their top-level domain, plus the
// ".cognitiveservices.azure.com" host that AI Foundry resources serve the same
// OpenAI route on.
func isAzureOpenAI(baseURL string) bool {
	lower := strings.ToLower(baseURL)
	return strings.Contains(lower, azureOpenAIHostSuffix) ||
		strings.Contains(lower, ".openai.azure.") ||
		strings.Contains(lower, azureCognitiveServicesHostSuffix)
}

// postOpenAIJSON POSTs body to an OpenAI-dialect endpoint, selecting the API-key
// auth scheme by host: Azure OpenAI takes the key in the "api-key" header while
// every other provider uses the "Authorization: Bearer" scheme of postJSON. An
// empty key sends neither, matching postJSON for keyless local endpoints (Ollama,
// LM Studio). baseURL drives the scheme choice and url is the concrete request
// target; they differ only in tests, where url points at a local stub.
func postOpenAIJSON(ctx context.Context, client *http.Client, baseURL, url, apiKey string, body any) (*http.Response, error) {
	if apiKey != "" && isAzureOpenAI(baseURL) {
		headers := map[string]string{
			"Content-Type": "application/json",
			"Accept":       "text/event-stream",
			"api-key":      apiKey,
		}
		return postJSONWithHeaders(ctx, client, url, headers, body)
	}
	return postJSON(ctx, client, url, apiKey, body)
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
			Type string `json:"type"`
			// Code is captured raw because providers disagree on its JSON type:
			// OpenAI/Anthropic send a string code ("context_length_exceeded"),
			// while Gemini sends a numeric HTTP status ("code": 400). Decoding into
			// a string field would fail the whole unmarshal on Gemini's numeric
			// form, dropping every Gemini error to the status-code fallback; a raw
			// message accepts either and errorCodeString normalizes it.
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}

	code := strings.ToLower(errorCodeString(envelope.Error.Code))
	typ := strings.ToLower(envelope.Error.Type)
	msg := strings.ToLower(envelope.Error.Message)
	switch {
	case code == "rate_limit_exceeded" || typ == "rate_limit_error":
		return fmt.Errorf("provider rate limited request: %w", ErrRateLimit)
	case code == "context_length_exceeded" || mentionsContextLimit(msg):
		// Match the over-budget wording every provider uses through the shared
		// mentionsContextLimit scan rather than re-listing markers here: OpenAI's
		// "context length", Anthropic's "prompt is too long: N tokens > M maximum",
		// and Gemini's "input token count (X) exceeds the maximum number of tokens"
		// all classify as ErrContextLimit so the agent's compaction path can recover
		// the turn instead of failing it as a generic 400 — regardless of the HTTP
		// status the envelope arrives on.
		return fmt.Errorf("provider context limit: %w", ErrContextLimit)
	case code == "model_not_found" || typ == "not_found_error":
		return fmt.Errorf("provider model lookup: %w", ErrModelNotFound)
	case code == "invalid_api_key" || typ == "authentication_error" || typ == "permission_error":
		return fmt.Errorf("provider authentication: %w", ErrAuth)
	default:
		return nil
	}
}

// errorCodeString renders an error envelope's "code" field as a string,
// accepting either the quoted string OpenAI/Anthropic send or the bare JSON
// number Gemini sends. A quoted string is unquoted to its value; anything else
// (a number, null, or absent field) is returned as its raw text, so a numeric
// Gemini code becomes "400" rather than breaking the comparison. An empty raw
// message yields "".
func errorCodeString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func mentionsContextLimit(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "context length") ||
		strings.Contains(s, "maximum context") ||
		strings.Contains(s, "too many tokens") ||
		// Anthropic's over-budget wording is "prompt is too long: N tokens > M
		// maximum", which shares none of the markers above.
		strings.Contains(s, "prompt is too long") ||
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
