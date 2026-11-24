package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// retryTestServer answers each request with the next status in statuses
// (repeating the last one) and counts the requests.
func retryTestServer(t *testing.T, header http.Header, statuses ...int) (*apiClient, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1)) - 1
		if n >= len(statuses) {
			n = len(statuses) - 1
		}
		for k, v := range header {
			w.Header()[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statuses[n])
		if statuses[n] == http.StatusTooManyRequests {
			// express-limiter answers with plain text, not JSON.
			_, _ = w.Write([]byte("Rate limit exceeded"))
			return
		}
		_, _ = w.Write([]byte(`{"message":"from fake"}`))
	}))
	t.Cleanup(srv.Close)
	c := newAPIClient(srv.URL, fakeAPIKey, "header", "test")
	c.retry.BaseDelay = time.Millisecond
	c.retry.MaxDelay = 10 * time.Millisecond
	return c, &calls
}

func TestDoJSON_retriesThrottledRequests(t *testing.T) {
	c, calls := retryTestServer(t, http.Header{"Retry-After": {"0"}}, 429, 429, 200)
	if err := c.doJSON(context.Background(), http.MethodPost, "/v2/dashboards", map[string]any{}, nil); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Fatalf("requests = %d, want 3 (two 429s, then success)", got)
	}
}

func TestDoJSON_doesNotRepeatPostAfterServerError(t *testing.T) {
	c, calls := retryTestServer(t, nil, 503, 200)
	err := c.doJSON(context.Background(), http.MethodPost, "/v2/synthetic/checks", map[string]any{}, nil)
	if apiStatusCode(err) != 503 {
		t.Fatalf("err = %v, want the 503", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("requests = %d, want 1: a POST the server saw may have created the object", got)
	}
}

func TestDoJSON_retriesIdempotentServerErrors(t *testing.T) {
	c, calls := retryTestServer(t, nil, 502, 503, 200)
	if err := c.doJSON(context.Background(), http.MethodGet, "/v2/channels", nil, nil); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}

func TestDoJSON_stopsAfterMaxRetries(t *testing.T) {
	c, calls := retryTestServer(t, nil, 503)
	c.retry.MaxRetries = 2
	err := c.doJSON(context.Background(), http.MethodDelete, "/v2/synthetic/checks/x", nil, nil)
	if apiStatusCode(err) != 503 {
		t.Fatalf("err = %v, want the last 503", err)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Fatalf("requests = %d, want 1 + 2 retries", got)
	}
}

func TestDoJSON_retryDisabled(t *testing.T) {
	c, calls := retryTestServer(t, nil, 429, 200)
	c.retry.Enabled = false
	err := c.doJSON(context.Background(), http.MethodGet, "/v2/channels", nil, nil)
	if apiStatusCode(err) != 429 {
		t.Fatalf("err = %v, want the 429", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	if apiErr, _ := err.(*apiError); apiErr == nil || apiErr.Message != "Rate limit exceeded" {
		t.Fatalf("plain-text 429 body not kept as the message: %#v", err)
	}
}

func TestDoJSON_clientErrorsAreNotRetried(t *testing.T) {
	c, calls := retryTestServer(t, nil, 400, 200)
	if err := c.doJSON(context.Background(), http.MethodGet, "/v2/channels", nil, nil); apiStatusCode(err) != 400 {
		t.Fatalf("err = %v, want the 400", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestDoJSON_timeoutBoundsTheWait(t *testing.T) {
	// The server asks for a 30s wait; with a 1s budget the client gives up
	// instead of sleeping past it.
	c, calls := retryTestServer(t, http.Header{"Retry-After": {"30"}}, 429)
	c.retry.MaxDelay = time.Minute
	c.retry.Timeout = time.Second
	start := time.Now()
	if err := c.doJSON(context.Background(), http.MethodGet, "/v2/channels", nil, nil); apiStatusCode(err) != 429 {
		t.Fatalf("err = %v, want the 429", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s, want an early give-up", elapsed)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestRetryAfter(t *testing.T) {
	cases := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"", 0, false},
		{"2", 2 * time.Second, true},
		{"1.5", 1500 * time.Millisecond, true}, // express-limiter sends fractional seconds
		{"soon", 0, false},
		{time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0, true},
	}
	for _, tc := range cases {
		got, ok := retryAfter(http.Header{"Retry-After": {tc.value}})
		if ok != tc.ok || got != tc.want {
			t.Errorf("retryAfter(%q) = %s, %v; want %s, %v", tc.value, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNotificationChannels_cachedAfterSuccessOnly(t *testing.T) {
	var calls int32
	fail := int32(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if atomic.LoadInt32(&fail) == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`[{"_id":"c1","name":"a@example.com","type":"EMAIL"}]`))
	}))
	defer srv.Close()
	c := newAPIClient(srv.URL, fakeAPIKey, "header", "test")
	ctx := context.Background()

	if _, err := c.notificationChannels(ctx); err == nil {
		t.Fatal("want the 403")
	}
	atomic.StoreInt32(&fail, 0)
	for i := 0; i < 3; i++ {
		got, err := c.notificationChannels(ctx)
		if err != nil || len(got) != 1 {
			t.Fatalf("channels = %v, %v", got, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("requests = %d, want 2 (the failure, then one cached success)", got)
	}
}

func TestDoJSON_timeoutBoundsASingleRequest(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	c := newAPIClient(srv.URL, fakeAPIKey, "header", "test")
	c.retry.Timeout = 300 * time.Millisecond
	start := time.Now()
	if err := c.doJSON(context.Background(), http.MethodGet, "/v2/channels", nil, nil); err == nil {
		t.Fatal("want a timeout error from a hung request")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("hung request took %s; the retry timeout must bound it", elapsed)
	}
}
