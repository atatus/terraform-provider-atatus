package provider

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &AtatusProvider{}

type AtatusProvider struct {
	version string
}

type AtatusConfig struct {
	ApiKey    string
	AccountID string
	AuthType  string
	// Endpoint is the API base URL without a trailing slash.
	Endpoint string
	// Client is the shared API client (see client.go).
	Client *apiClient
}

type AtatusProviderModel struct {
	ApiKey              types.String `tfsdk:"api_key"`
	AccountID           types.String `tfsdk:"account_id"`
	AuthType            types.String `tfsdk:"auth_type"`
	Endpoint            types.String `tfsdk:"endpoint"`
	Validate            types.Bool   `tfsdk:"validate"`
	HTTPRetryEnabled    types.Bool   `tfsdk:"http_retry_enabled"`
	HTTPRetryMaxRetries types.Int64  `tfsdk:"http_retry_max_retries"`
	HTTPRetryTimeout    types.Int64  `tfsdk:"http_retry_timeout"`
}

// Environment variables read when the matching provider argument is not set.
const (
	envAPIKey   = "ATATUS_API_KEY"
	envEndpoint = "ATATUS_ENDPOINT"
)

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &AtatusProvider{version: version}
	}
}

func (p *AtatusProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "atatus"
	resp.Version = p.version
}

func (p *AtatusProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Interact with Atatus Observability Platform.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Description: "Atatus application key (ak_...) with Read & Write permission. Can also be set with the " + envAPIKey + " environment variable.",
				Optional:    true,
				Sensitive:   true,
			},
			"account_id": schema.StringAttribute{
				Description:        "Not used: the API key already identifies the account.",
				Optional:           true,
				DeprecationMessage: "account_id is not used by the provider (the API key identifies the account) and will be removed in a future release. Remove it from the provider block.",
			},
			"auth_type": schema.StringAttribute{
				Description:        "Authentication type: 'header' (default) or 'cookie'.",
				Optional:           true,
				DeprecationMessage: "auth_type is deprecated: the Atatus public API accepts only API keys sent as a header, so 'cookie' cannot authenticate. Remove it from the provider block.",
			},
			"endpoint": schema.StringAttribute{
				Description: "Base URL of the Atatus API, for example a self-hosted or staging install. Defaults to " + defaultEndpoint + ". Must be an http(s) URL; a trailing slash is ignored. Can also be set with the " + envEndpoint + " environment variable.",
				Optional:    true,
				Validators:  []validator.String{endpointValidator{}},
			},
			"validate": schema.BoolAttribute{
				Description: "Check the API key against the API when the provider starts, so a wrong key or endpoint fails before planning. Default: true.",
				Optional:    true,
			},
			"http_retry_enabled": schema.BoolAttribute{
				Description: "Retry requests the API throttled (HTTP 429), and failed requests that are safe to repeat (HTTP 5xx and network errors on reads, updates and deletes). Default: true.",
				Optional:    true,
			},
			"http_retry_max_retries": schema.Int64Attribute{
				Description: "Most retries for one request, 1 to 5. Default: 3.",
				Optional:    true,
				Validators:  []validator.Int64{int64validator.Between(1, 5)},
			},
			"http_retry_timeout": schema.Int64Attribute{
				Description: "Seconds one request may take, including retries and the waits between them. Default: 60.",
				Optional:    true,
				Validators:  []validator.Int64{int64validator.AtLeast(1)},
			},
		},
	}
}

func (p *AtatusProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data AtatusProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.ApiKey.IsUnknown() || data.Endpoint.IsUnknown() {
		// Known only after apply (derived from another resource): leave the
		// resources unconfigured for this plan, as the framework recommends.
		return
	}

	// An empty api_key (say, a variable defaulting to "") falls back to the
	// environment like an unset one.
	apiKey := strings.TrimSpace(data.ApiKey.ValueString())
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv(envAPIKey))
	}
	if apiKey == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_key"),
			"Missing Atatus API Key",
			"Set api_key in the provider block or the "+envAPIKey+" environment variable to an Atatus application key (ak_...) with Read & Write permission.",
		)
		return
	}

	authType := "header"
	if !data.AuthType.IsNull() {
		authType = data.AuthType.ValueString()
	}

	accountID := ""
	if !data.AccountID.IsNull() {
		accountID = data.AccountID.ValueString()
	}

	rawEndpoint, endpointAttr := os.Getenv(envEndpoint), envEndpoint
	if !data.Endpoint.IsNull() {
		rawEndpoint, endpointAttr = data.Endpoint.ValueString(), "endpoint"
	}
	endpoint := defaultEndpoint
	if strings.TrimSpace(rawEndpoint) != "" {
		normalized, err := normalizeEndpoint(rawEndpoint)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Invalid Endpoint", endpointAttr+": "+err.Error())
			return
		}
		endpoint = normalized
	}

	// A setting only known after apply keeps its default.
	client := newAPIClient(endpoint, apiKey, authType, p.version)
	if isKnown(data.HTTPRetryEnabled) {
		client.retry.Enabled = data.HTTPRetryEnabled.ValueBool()
	}
	if isKnown(data.HTTPRetryMaxRetries) {
		client.retry.MaxRetries = int(data.HTTPRetryMaxRetries.ValueInt64())
	}
	if isKnown(data.HTTPRetryTimeout) {
		client.retry.Timeout = time.Duration(data.HTTPRetryTimeout.ValueInt64()) * time.Second
	}

	if !isKnown(data.Validate) || data.Validate.ValueBool() {
		// The channel list is a cheap authenticated read, and the checks need
		// it anyway, so validating also warms the client's cache.
		if _, err := client.notificationChannels(ctx); err != nil {
			resp.Diagnostics.AddError(
				"Atatus API Key Check Failed",
				"The provider could not authenticate against "+endpoint+": "+err.Error()+
					"\n\nCheck the API key (an application key with Read & Write permission) and the endpoint. Set validate = false to skip this check.",
			)
			return
		}
	}

	cfg := &AtatusConfig{
		ApiKey:    apiKey,
		AccountID: accountID,
		AuthType:  authType,
		Endpoint:  endpoint,
		Client:    client,
	}

	resp.ResourceData = cfg
	resp.DataSourceData = cfg
}

func (p *AtatusProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewHTTPCheckResource,
		NewSSLCheckResource,
		NewTCPCheckResource,
		NewDNSCheckResource,
		NewICMPCheckResource,
		NewMultistepCheckResource,
		NewDashboardResource,
		NewDashboardGroupResource,
		NewPowerpackResource,
		NewDashboardRestrictionResource,
		NewTypedDashboardResource,
	}
}

func (p *AtatusProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewDashboardDataSource,
		NewDashboardsDataSource,
		NewDashboardGroupDataSource,
		NewPowerpackDataSource,
		NewNotificationChannelDataSource,
		NewProjectDataSource,
		NewSpaceDataSource,
		NewTeamDataSource,
		NewUserDataSource,
	}
}

// isKnown reports whether a configuration value is set and known.
func isKnown(v interface {
	IsNull() bool
	IsUnknown() bool
}) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// endpointValidator rejects an "endpoint" that is not an absolute http(s) URL
// at validate time, using the same rule Configure applies.
type endpointValidator struct{}

func (endpointValidator) Description(ctx context.Context) string {
	return "must be an http:// or https:// URL"
}

func (v endpointValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (endpointValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := normalizeEndpoint(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Endpoint", err.Error())
	}
}
