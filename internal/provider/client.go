package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultEndpoint is the Atatus public API host used when the provider's
// "endpoint" attribute is not set.
const defaultEndpoint = "https://www.atatus.com"

// apiClient talks to the Atatus public API (X-API-KEY authenticated routes in
// atatus-server api/lib/routes/public-api.js). Every resource uses it.
type apiClient struct {
	baseURL    string
	apiKey     string
	authType   string
	userAgent  string
	httpClient *http.Client
	retry      retryConfig

	// The account's notification channels, read once per provider run (see
	// notificationChannels).
	channelsMu sync.Mutex
	channels   []NotificationChannel
}

// retryConfig controls how doJSON retries throttled and failed requests.
type retryConfig struct {
	Enabled    bool
	MaxRetries int
	// Timeout bounds the whole call, including waits between attempts.
	Timeout time.Duration
	// BaseDelay is the first backoff step; it doubles on every attempt.
	BaseDelay time.Duration
	// MaxDelay caps one exponential backoff step.
	MaxDelay time.Duration
}

func defaultRetryConfig() retryConfig {
	return retryConfig{
		Enabled:    true,
		MaxRetries: 3,
		Timeout:    60 * time.Second,
		BaseDelay:  time.Second,
		MaxDelay:   30 * time.Second,
	}
}

func newAPIClient(baseURL, apiKey, authType, version string) *apiClient {
	return &apiClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		authType:   authType,
		userAgent:  "terraform-provider-atatus/" + version,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		retry:      defaultRetryConfig(),
	}
}

// apiError is a non-2xx answer from the Atatus API. Message is the server's
// {"message": "..."} when the body carries one, otherwise the raw body text.
type apiError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("Atatus API %s %s returned HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
}

func apiStatusCode(err error) int {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// IsNotFound reports whether err is an API 404.
func IsNotFound(err error) bool { return apiStatusCode(err) == http.StatusNotFound }

// IsConflict reports whether err is an API 409.
func IsConflict(err error) bool { return apiStatusCode(err) == http.StatusConflict }

// setHeaders sends the key as X-API-KEY (or as a Cookie when the deprecated
// auth_type = "cookie" is set), with a versioned User-Agent.
func (c *apiClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.authType == "cookie" {
		req.Header.Set("Cookie", c.apiKey)
	} else {
		req.Header.Set("X-API-KEY", c.apiKey)
	}
}

// doJSON sends body (nil, raw JSON as []byte, or any value to marshal) to
// baseURL+path and decodes a 2xx response into out (nil to discard it; a
// *json.RawMessage keeps the bytes as they came). Non-2xx returns *apiError.
//
// Throttled (429) requests are retried for every method, after the server's
// Retry-After when it sends one. 5xx answers and transport errors are retried
// only for methods that are safe to repeat: a POST may already have created
// the object, so it is never repeated after the server saw it.
func (c *apiClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	switch b := body.(type) {
	case nil:
	case []byte:
		payload = b
	default:
		encoded, err := json.Marshal(b)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		payload = encoded
	}

	// The timeout bounds the whole call: every attempt runs under it, and a
	// wait that would end past it is not started.
	deadline := time.Now().Add(c.retry.Timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	for attempt := 0; ; attempt++ {
		status, header, respBytes, err := c.send(ctx, method, path, payload)
		if err == nil && status >= 200 && status < 300 {
			if out == nil || len(bytes.TrimSpace(respBytes)) == 0 {
				return nil
			}
			if err := json.Unmarshal(respBytes, out); err != nil {
				return fmt.Errorf("decoding Atatus API response from %s %s: %w", method, path, err)
			}
			return nil
		}

		var callErr error
		if err != nil {
			callErr = fmt.Errorf("Atatus API %s %s: %w", method, path, err)
		} else {
			callErr = newAPIError(method, path, status, respBytes)
		}

		if !c.retry.Enabled || attempt >= c.retry.MaxRetries || !shouldRetry(method, status, err) || ctx.Err() != nil {
			return callErr
		}
		wait := c.backoff(attempt, header)
		if time.Now().Add(wait).After(deadline) {
			return callErr
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return callErr
		case <-timer.C:
		}
	}
}

// send performs one HTTP round trip. err is a transport error; a non-2xx
// status is returned as a status, not an error.
func (c *apiClient) send(ctx context.Context, method, path string, payload []byte) (int, http.Header, []byte, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("creating request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("reading Atatus API response: %w", err)
	}
	return resp.StatusCode, resp.Header, respBytes, nil
}

// shouldRetry reports whether a failed attempt may be repeated.
func shouldRetry(method string, status int, transportErr error) bool {
	if transportErr != nil {
		return isIdempotent(method)
	}
	if status == http.StatusTooManyRequests {
		return true
	}
	if status >= 500 && status != http.StatusNotImplemented {
		return isIdempotent(method)
	}
	return false
}

// isIdempotent covers the methods whose repeat cannot create a second object.
// PATCH qualifies here because the provider always sends the full document.
func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// backoff is the server's Retry-After when it sent one, otherwise an
// exponential step (BaseDelay * 2^attempt), capped at MaxDelay, with up to
// 50% jitter. Retry-After is not capped: retrying before the server said it
// would accept the request only spends another attempt, so when the wait does
// not fit in the timeout doJSON gives up instead.
func (c *apiClient) backoff(attempt int, header http.Header) time.Duration {
	if wait, ok := retryAfter(header); ok {
		return wait
	}
	step := c.retry.BaseDelay << attempt
	if step <= 0 || step > c.retry.MaxDelay {
		step = c.retry.MaxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(step)/2 + 1))
	return step/2 + jitter
}

// retryAfter reads a Retry-After header given in (possibly fractional)
// seconds or as an HTTP date.
func retryAfter(header http.Header) (time.Duration, bool) {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds * float64(time.Second)), true
	}
	if at, err := http.ParseTime(value); err == nil {
		if wait := time.Until(at); wait > 0 {
			return wait, true
		}
		return 0, true
	}
	return 0, false
}

// notificationChannels returns the account's channels (GET /v2/channels),
// read once per provider run so a plan with many checks makes one request
// instead of one per resource. A failed read is not cached.
func (c *apiClient) notificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	c.channelsMu.Lock()
	defer c.channelsMu.Unlock()
	if c.channels != nil {
		return c.channels, nil
	}
	var channels []NotificationChannel
	if err := c.doJSON(ctx, http.MethodGet, "/v2/channels", nil, &channels); err != nil {
		return nil, fmt.Errorf("reading notification channels: %w", err)
	}
	if channels == nil {
		channels = []NotificationChannel{}
	}
	c.channels = channels
	return channels, nil
}

func newAPIError(method, path string, status int, body []byte) *apiError {
	text := strings.TrimSpace(string(body))
	msg := text
	var parsed struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Message != "" {
		msg = parsed.Message
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &apiError{Method: method, Path: path, StatusCode: status, Message: msg, Body: text}
}

// normalizeEndpoint checks the provider "endpoint" is an absolute http(s) URL
// and trims trailing slashes so paths can be appended directly.
func normalizeEndpoint(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("endpoint %q is not a valid URL: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("endpoint %q must start with http:// or https://", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("endpoint %q has no host", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("endpoint %q must not contain a query string or fragment", raw)
	}
	return trimmed, nil
}
