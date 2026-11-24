package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestNormalizeEndpoint(t *testing.T) {
	ok := map[string]string{
		"https://www.atatus.com":      "https://www.atatus.com",
		"https://www.atatus.com/":     "https://www.atatus.com",
		"http://localhost:8080//":     "http://localhost:8080",
		" https://example.test/api/ ": "https://example.test/api",
	}
	for in, want := range ok {
		got, err := normalizeEndpoint(in)
		if err != nil || got != want {
			t.Errorf("normalizeEndpoint(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "www.atatus.com", "ftp://example.test", "https://", "https://example.test/?a=1", "https://example.test/#x", "://x"} {
		if got, err := normalizeEndpoint(bad); err == nil {
			t.Errorf("normalizeEndpoint(%q) = %q, want an error", bad, got)
		}
	}
}

func TestEndpointValidator(t *testing.T) {
	ctx := context.Background()
	run := func(v types.String) bool {
		resp := &validator.StringResponse{}
		endpointValidator{}.ValidateString(ctx, validator.StringRequest{Path: path.Root("endpoint"), ConfigValue: v}, resp)
		return resp.Diagnostics.HasError()
	}
	if run(types.StringValue("https://example.test")) || run(types.StringNull()) || run(types.StringUnknown()) {
		t.Error("valid, null or unknown endpoint rejected")
	}
	if !run(types.StringValue("example.test")) {
		t.Error("endpoint without scheme accepted")
	}
}

func TestAPIClient_headersAndJSON(t *testing.T) {
	var gotMethod, gotPath, gotKey, gotCookie, gotUA, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotBody = r.Method, r.URL.RequestURI(), string(b)
		gotKey, gotCookie = r.Header.Get("X-API-KEY"), r.Header.Get("Cookie")
		gotUA, gotCT = r.Header.Get("User-Agent"), r.Header.Get("Content-Type")
		fmt.Fprint(w, `{"ok":true,"n":5}`)
	}))
	defer srv.Close()

	c := newAPIClient(srv.URL, fakeAPIKey, "header", "1.2.3")
	if c.httpClient.Timeout != 60*time.Second {
		t.Errorf("timeout = %v", c.httpClient.Timeout)
	}
	var out struct {
		OK bool `json:"ok"`
		N  int  `json:"n"`
	}
	if err := c.doJSON(context.Background(), http.MethodPost, "/v2/dashboards?dryRun=true", map[string]any{"title": "T"}, &out); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotPath != "/v2/dashboards?dryRun=true" || gotBody != `{"title":"T"}` {
		t.Errorf("request = %s %s %s", gotMethod, gotPath, gotBody)
	}
	if gotKey != fakeAPIKey || gotCookie != "" || gotUA != "terraform-provider-atatus/1.2.3" || gotCT != "application/json" {
		t.Errorf("headers: key=%q cookie=%q ua=%q ct=%q", gotKey, gotCookie, gotUA, gotCT)
	}
	if !out.OK || out.N != 5 {
		t.Errorf("decoded %+v", out)
	}

	// Raw bytes are sent untouched; cookie auth uses the Cookie header.
	c = newAPIClient(srv.URL, "session=fake", "cookie", "dev")
	if err := c.doJSON(context.Background(), http.MethodPut, "/x", []byte(`{ "raw" : 1 }`), nil); err != nil {
		t.Fatal(err)
	}
	if gotBody != `{ "raw" : 1 }` || gotCookie != "session=fake" || gotKey != "" {
		t.Errorf("raw/cookie: body=%q cookie=%q key=%q", gotBody, gotCookie, gotKey)
	}

	var raw json.RawMessage
	if err := c.doJSON(context.Background(), http.MethodGet, "/x", nil, &raw); err != nil || string(raw) != `{"ok":true,"n":5}` {
		t.Errorf("raw out = %s, %v", raw, err)
	}
}

func TestAPIClient_errorMapping(t *testing.T) {
	status, body := 0, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	c := newAPIClient(srv.URL, fakeAPIKey, "header", "dev")

	cases := []struct {
		status          int
		body, message   string
		notFound, confl bool
	}{
		{404, `{"message":"Dashboard not found"}`, "Dashboard not found", true, false},
		{409, `{"message":"version mismatch"}`, "version mismatch", false, true},
		{400, `{"message":"widgets[0]: bad"}`, "widgets[0]: bad", false, false},
		{500, `upstream exploded`, "upstream exploded", false, false},
		{403, ``, "Forbidden", false, false},
	}
	for _, tc := range cases {
		status, body = tc.status, tc.body
		err := c.doJSON(context.Background(), http.MethodGet, "/v2/dashboards/x", nil, nil)
		var apiErr *apiError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%d: want *apiError, got %T %v", tc.status, err, err)
		}
		if apiErr.StatusCode != tc.status || apiErr.Message != tc.message || apiErr.Path != "/v2/dashboards/x" {
			t.Errorf("%d: got %+v", tc.status, apiErr)
		}
		if IsNotFound(err) != tc.notFound || IsConflict(err) != tc.confl {
			t.Errorf("%d: IsNotFound=%v IsConflict=%v", tc.status, IsNotFound(err), IsConflict(err))
		}
		if apiErrorMessage(err) != tc.message {
			t.Errorf("%d: apiErrorMessage=%q", tc.status, apiErrorMessage(err))
		}
	}
	if IsNotFound(errors.New("plain")) || IsNotFound(nil) {
		t.Error("non-API errors are not 404s")
	}
}
