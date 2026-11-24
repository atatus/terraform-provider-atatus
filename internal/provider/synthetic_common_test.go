package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// A resource whose provider could not be configured (api_key known only after
// apply) must return an error from every API call, not panic.
func TestSyntheticBase_unconfiguredClient(t *testing.T) {
	var b syntheticBase
	ctx := context.Background()
	for name, err := range map[string]error{
		"create": b.createCheck(ctx, map[string]any{}, nil),
		"get":    b.getCheck(ctx, "x", "http", nil),
		"patch":  b.patchCheck(ctx, "x", map[string]any{}),
		"delete": b.deleteCheck(ctx, "x"),
	} {
		if err != errNotConfigured {
			t.Errorf("%s: err = %v, want errNotConfigured", name, err)
		}
	}
	if _, err := b.fetchNotificationChannels(ctx); err != errNotConfigured {
		t.Errorf("channels: err = %v, want errNotConfigured", err)
	}
}

// A check of another kind (an SSL check's id read as an HTTP check, as after
// `terraform import atatus_http_check.x <ssl id>`) is refused, so it is never
// written back as the wrong kind; the matching kind reads normally.
func TestGetCheck_refusesAnotherKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"_id":"c1","name":"cert","subtype":"ssl"}`))
	}))
	defer srv.Close()
	b := syntheticBase{client: newAPIClient(srv.URL, fakeAPIKey, "header", "test")}
	ctx := context.Background()

	var out struct {
		Name string `json:"name"`
	}
	err := b.getCheck(ctx, "c1", "http", &out)
	if err == nil || !strings.Contains(err.Error(), `is a "ssl" check, not a "http" check`) || out.Name != "" {
		t.Fatalf("http read of an ssl check: err = %v, out = %+v; want the kind error and nothing read", err, out)
	}
	if err := b.getCheck(ctx, "c1", "ssl", &out); err != nil || out.Name != "cert" {
		t.Fatalf("ssl read: err = %v, out = %+v", err, out)
	}
}

// When the channel list cannot be read, Read keeps emails and channel_ids as
// they were and warns, instead of failing or silently leaving them stale.
func TestReadChannels_lookupFailureWarnsAndKeepsValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	defer srv.Close()
	b := syntheticBase{client: newAPIClient(srv.URL, fakeAPIKey, "header", "test")}
	ctx := context.Background()

	emails, _ := types.ListValueFrom(ctx, types.StringType, []string{"devops@example.com"})
	others, _ := types.ListValueFrom(ctx, types.StringType, []string{"ch-slack-alerts"})
	var diags diag.Diagnostics
	b.readChannels(ctx, []string{"ch-email-devops", "ch-slack-alerts"}, &emails, &others, &diags)

	if diags.HasError() || diags.WarningsCount() != 1 {
		t.Fatalf("diags = %v, want exactly one warning", diags)
	}
	var gotEmails, gotOthers []string
	emails.ElementsAs(ctx, &gotEmails, false)
	others.ElementsAs(ctx, &gotOthers, false)
	if len(gotEmails) != 1 || gotEmails[0] != "devops@example.com" || len(gotOthers) != 1 || gotOthers[0] != "ch-slack-alerts" {
		t.Fatalf("values changed: emails=%v channel_ids=%v", gotEmails, gotOthers)
	}
}

// Empty strings in a list are kept, as the resources always sent them.
func TestStringsFromList_keepsEmptyStrings(t *testing.T) {
	ctx := context.Background()
	list, _ := types.ListValueFrom(ctx, types.StringType, []string{"a", ""})
	got, diags := stringsFromList(ctx, list, nil)
	if diags.HasError() || len(got) != 2 {
		t.Fatalf("got %q, %v", got, diags)
	}
}
