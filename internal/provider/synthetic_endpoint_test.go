package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Every synthetic check resource must send its requests to the configured
// endpoint, not a hard-coded host. One fake server; for each resource type we
// list channels and delete a check, and assert both reached the fake.
func TestSyntheticResourcesUseConfiguredEndpoint(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+" key="+r.Header.Get("X-API-KEY"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/channels" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	ctx := context.Background()

	factories := map[string]func() resource.Resource{
		"http": NewHTTPCheckResource, "ssl": NewSSLCheckResource, "tcp": NewTCPCheckResource,
		"dns": NewDNSCheckResource, "icmp": NewICMPCheckResource, "multistep": NewMultistepCheckResource,
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			mu.Lock()
			seen = nil
			mu.Unlock()

			// A fresh client per resource: the client caches the channel
			// list for the provider run, so a shared one would only fetch it
			// in the first subtest.
			cfg := &AtatusConfig{ApiKey: fakeAPIKey, AuthType: "header", Endpoint: srv.URL}
			cfg.Client = newAPIClient(cfg.Endpoint, cfg.ApiKey, cfg.AuthType, "test")

			r := factory()
			cr := r.(resource.ResourceWithConfigure)
			cresp := &resource.ConfigureResponse{}
			cr.Configure(ctx, resource.ConfigureRequest{ProviderData: cfg}, cresp)
			if cresp.Diagnostics.HasError() {
				t.Fatal(cresp.Diagnostics)
			}

			lister, ok := r.(interface {
				fetchNotificationChannels(context.Context) ([]NotificationChannel, error)
			})
			if !ok {
				t.Fatalf("%T has no fetchNotificationChannels", r)
			}
			if _, err := lister.fetchNotificationChannels(ctx); err != nil {
				t.Fatal(err)
			}

			sresp := &resource.SchemaResponse{}
			r.Schema(ctx, resource.SchemaRequest{}, sresp)
			objType := sresp.Schema.Type().TerraformType(ctx).(tftypes.Object)
			vals := map[string]tftypes.Value{}
			for k, typ := range objType.AttributeTypes {
				vals[k] = tftypes.NewValue(typ, nil)
			}
			vals["id"] = tftypes.NewValue(tftypes.String, "chk-123")
			state := tfsdk.State{Schema: sresp.Schema, Raw: tftypes.NewValue(objType, vals)}
			dresp := &resource.DeleteResponse{State: state}
			r.Delete(ctx, resource.DeleteRequest{State: state}, dresp)
			if dresp.Diagnostics.HasError() {
				t.Fatal(dresp.Diagnostics)
			}

			mu.Lock()
			defer mu.Unlock()
			want := []string{
				"GET /v2/channels key=" + fakeAPIKey,
				"DELETE /v2/synthetic/checks/chk-123 key=" + fakeAPIKey,
			}
			if len(seen) != len(want) || seen[0] != want[0] || seen[1] != want[1] {
				t.Fatalf("requests to the configured endpoint = %v, want %v", seen, want)
			}
		})
	}
}
