package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// FailoverProvider wraps an ordered list of providers and tries each in turn
// until one accepts the request. It is a thin composable Provider: the same
// Request is forwarded unchanged to every provider, and the first provider
// whose Stream call succeeds owns the returned stream.
//
// Failover is triggered only by retryable availability errors (rate limits,
// provider-side server errors, and transport/connection failures). A
// user-facing error such as authentication, an invalid request, an unknown
// model, or a context-limit overflow is terminal and returned immediately
// without trying the next provider, since retrying it elsewhere would fail the
// same way. The error reported when every provider fails is the last one seen.
type FailoverProvider struct {
	// providers is the ordered failover chain: index 0 is the primary and the
	// rest are fallbacks tried in sequence.
	providers []Provider
}

// FailoverProvider is a Provider so it can compose transparently with any
// caller that holds a plain Provider.
var _ Provider = (*FailoverProvider)(nil)

// NewFailoverProvider builds a FailoverProvider from a primary provider and an
// ordered list of fallbacks. The primary is tried first, then each fallback in
// order. It returns an error if the primary is nil so the chain always has a
// usable head.
func NewFailoverProvider(primary Provider, fallbacks ...Provider) (*FailoverProvider, error) {
	if primary == nil {
		return nil, fmt.Errorf("constructing failover provider: primary is nil")
	}
	providers := make([]Provider, 0, 1+len(fallbacks))
	providers = append(providers, primary)
	providers = append(providers, fallbacks...)
	return &FailoverProvider{providers: providers}, nil
}

// Name returns the primary provider's name.
func (p *FailoverProvider) Name() string {
	if len(p.providers) == 0 {
		return ""
	}
	return p.providers[0].Name()
}

// Models returns the primary provider's models.
func (p *FailoverProvider) Models() []Model {
	if len(p.providers) == 0 {
		return nil
	}
	return p.providers[0].Models()
}

// SupportsTools reports whether the primary provider supports tools.
func (p *FailoverProvider) SupportsTools() bool {
	if len(p.providers) == 0 {
		return false
	}
	return p.providers[0].SupportsTools()
}

// SupportsImages reports whether the primary provider supports images.
func (p *FailoverProvider) SupportsImages() bool {
	if len(p.providers) == 0 {
		return false
	}
	return p.providers[0].SupportsImages()
}

// Stream tries each provider in order, forwarding req unchanged. It returns the
// first successful stream. On a retryable availability error it falls over to
// the next provider; on any other error it returns immediately. When every
// provider fails the last error is returned.
func (p *FailoverProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	if len(p.providers) == 0 {
		return nil, fmt.Errorf("streaming failover request: %w", ErrProviderNotFound)
	}

	var lastErr error
	for _, provider := range p.providers {
		events, err := provider.Stream(ctx, req)
		if err == nil {
			return events, nil
		}
		lastErr = err
		if !shouldFailover(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// shouldFailover reports whether err is a retryable availability failure that
// warrants trying the next provider. It mirrors the retry semantics of
// ShouldRetry but operates on the already-classified error rather than a raw
// status code: rate limits, provider server errors, and transport/connection
// failures fail over, while user/auth 4xx errors (and any other terminal
// error) do not. Context cancellation and deadline errors never fail over,
// since the caller, not the provider, ended the request.
func shouldFailover(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrRateLimit) || errors.Is(err, ErrServer) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
