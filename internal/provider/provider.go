package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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
}

type AtatusProviderModel struct {
	ApiKey    types.String `tfsdk:"api_key"`
	AccountID types.String `tfsdk:"account_id"`
	AuthType  types.String `tfsdk:"auth_type"`
}

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
				Description: "Atatus API Key or session cookie string.",
				Required:    true,
				Sensitive:   true,
			},
			"account_id": schema.StringAttribute{
				Description: "Your Atatus Account ID (e.g., 1234567890abcdef12345678). Optional for v2 API.",
				Optional:    true,
			},
			"auth_type": schema.StringAttribute{
				Description: "Authentication type: 'header' (default) or 'cookie'.",
				Optional:    true,
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

	authType := "header"
	if !data.AuthType.IsNull() {
		authType = data.AuthType.ValueString()
	}

	accountID := ""
	if !data.AccountID.IsNull() {
		accountID = data.AccountID.ValueString()
	}

	cfg := &AtatusConfig{
		ApiKey:    data.ApiKey.ValueString(),
		AccountID: accountID,
		AuthType:  authType,
	}

	resp.ResourceData = cfg
}

func (p *AtatusProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewHTTPCheckResource,
		NewSSLCheckResource,
		NewTCPCheckResource,
		NewDNSCheckResource,
		NewICMPCheckResource,
		NewMultistepCheckResource,
	}
}

func (p *AtatusProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}
