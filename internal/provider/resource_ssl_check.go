package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource               = &SSLCheckResource{}
	_ resource.ResourceWithConfigure  = &SSLCheckResource{}
	_ resource.ResourceWithModifyPlan = &SSLCheckResource{}
)

func NewSSLCheckResource() resource.Resource {
	return &SSLCheckResource{}
}

type SSLCheckResource struct {
	client    *http.Client
	apiKey    string
	accountID string
	authType  string
}

type SSLCheckAssertionModel struct {
	Type     types.String `tfsdk:"type"`
	Operator types.String `tfsdk:"operator"`
	Target   types.String `tfsdk:"target"`
	Property types.String `tfsdk:"property"`
	Unit     types.String `tfsdk:"unit"`
}

var sslAssertionElementType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"type":     types.StringType,
		"operator": types.StringType,
		"target":   types.StringType,
		"property": types.StringType,
		"unit":     types.StringType,
	},
}

type SSLCheckModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Host              types.String `tfsdk:"host"`
	Port              types.Int64  `tfsdk:"port"`
	Frequency         types.String `tfsdk:"frequency"`
	Locations         types.List   `tfsdk:"locations"`
	Tags              types.List   `tfsdk:"tags"`
	MinLocationFailed types.Int64  `tfsdk:"min_location_failed"`
	RetryCount        types.Int64  `tfsdk:"retry_count"`
	RunType           types.String `tfsdk:"run_type"`
	ChannelIDs        types.List   `tfsdk:"channel_ids"`
	Emails            types.List   `tfsdk:"emails"`
	Assertions        types.List   `tfsdk:"assertion"`
}

type SSLCheckPayload struct {
	Name              string                 `json:"name"`
	Type              string                 `json:"type"`
	Subtype           string                 `json:"subtype"`
	Host              string                 `json:"host"`
	Server            string                 `json:"server"`
	Port              int64                  `json:"port"`
	Frequency         int64                  `json:"frequency"`
	MinLocationFailed *int64                 `json:"minLocationFailed,omitempty"`
	RetryCount        int64                  `json:"retryCount"`
	ChannelIDs        []string               `json:"channelIds"`
	RunType           string                 `json:"runType"`
	Locations         []string               `json:"locations"`
	Tags              []string               `json:"tags"`
	Assertions        []map[string]string    `json:"assertions"`
	Request           map[string]interface{} `json:"request"`
}

type SSLCheckResponse struct {
	ID                string           `json:"_id"`
	IDAlt             string           `json:"id"`
	Name              string           `json:"name"`
	Host              string           `json:"host"`
	Server            string           `json:"server"`
	Port              int64            `json:"port"`
	Subtype           string           `json:"subtype"`
	Frequency         int64            `json:"frequency"`
	MinLocationFailed int64            `json:"minLocationFailed"`
	RetryCount        int64            `json:"retryCount"`
	ChannelIDs        []string         `json:"channelIds"`
	RunType           string           `json:"runType"`
	Locations         []string         `json:"locations"`
	Tags              []string         `json:"tags"`
	Assertions        []map[string]string `json:"assertions"`
	AccountID         string           `json:"accountId"`
	Request           HTTPCheckReqData `json:"request"`
}

func (r *SSLCheckResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssl_check"
}

func (r *SSLCheckResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Atatus Synthetic SSL/TLS Check.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Identifier for the synthetic SSL check.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the synthetic SSL check.",
				Required:    true,
			},
			"host": schema.StringAttribute{
				Description: "Hostname or domain to check for SSL/TLS (e.g. 'example.com' or 'www.google.com').",
				Required:    true,
			},
			"port": schema.Int64Attribute{
				Description: "Port number for the SSL connection (default: 443).",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(443),
			},
			"frequency": schema.StringAttribute{
				Description: "Execution frequency interval ('1m', '5m', '10m', '15m', '30m', '1h', '6h', '12h', '1d'). Default: '5m'.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("5m"),
			},
			"retry_count": schema.Int64Attribute{
				Description: "Number of retries before triggering an alert (default: 0).",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(0),
			},
			"min_location_failed": schema.Int64Attribute{
				Description: "Minimum number of locations that must fail before alerting (default: 1). Only applicable when run_type is 'parallel'. Inapplicable and not allowed when run_type is 'sequential'.",
				Optional:    true,
				Computed:    true,
			},
			"run_type": schema.StringAttribute{
				Description: "Execution strategy across selected locations ('parallel' or 'sequential', default: 'parallel').",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("parallel"),
			},
			"channel_ids": schema.ListAttribute{
				Description: "List of notification channel IDs to notify on failure.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"emails": schema.ListAttribute{
				Description: "List of email addresses to alert directly when the check fails (auto-mapped to existing Atatus email notification channels).",
				ElementType: types.StringType,
				Optional:    true,
				Validators: []validator.List{
					listvalidator.ValueStringsAre(
						stringvalidator.RegexMatches(
							regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`),
							"Must be a valid email format (e.g. user@example.com)",
						),
					),
				},
			},
			"locations": schema.ListAttribute{
				Description: "List of location region codes (default: [\"US_NEW_YORK\"]).",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"tags": schema.ListAttribute{
				Description: "List of tags (e.g., [\"env:production\"]).",
				Optional:    true,
				ElementType: types.StringType,
			},
			"assertion": schema.ListNestedAttribute{
				Description: "List of assertion rules to validate SSL/TLS certificate and connection (defaults to certificate expiresInMoreThan 10 days, maximumTLSVersion moreThanOrEqual 1.3, responseTime lessThan 1000ms).",
				Optional:    true,
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Description: "Assertion type: certificate, maximumTLSVersion, minimumTLSVersion, responseTime.",
							Required:    true,
						},
						"operator": schema.StringAttribute{
							Description: "Comparison operator: expiresInMoreThan, expiresInLessThan (for certificate); moreThanOrEqual, is, moreThan, lessThan, lessThanOrEqual (for maximumTLSVersion); moreThanOrEqual, moreThan (for minimumTLSVersion); lessThan (for responseTime).",
							Required:    true,
						},
						"target": schema.StringAttribute{
							Description: "Expected target value (e.g. '10' for certificate days, '1.3' for TLS version, '1000' for responseTime ms).",
							Optional:    true,
							Computed:    true,
							Default:     stringdefault.StaticString(""),
						},
						"property": schema.StringAttribute{
							Description: "Property (unused for SSL checks, defaults to empty).",
							Optional:    true,
							Computed:    true,
							Default:     stringdefault.StaticString(""),
						},
						"unit": schema.StringAttribute{
							Description: "Measurement unit ('days' for certificate, 'ms' for responseTime, empty otherwise).",
							Optional:    true,
							Computed:    true,
							Default:     stringdefault.StaticString(""),
						},
					},
				},
			},
		},
	}
}

func (r *SSLCheckResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cfg, ok := req.ProviderData.(*AtatusConfig)
	if !ok {
		resp.Diagnostics.AddError("Configuration Error", "Expected *AtatusConfig")
		return
	}
	r.client = &http.Client{}
	r.apiKey = cfg.ApiKey
	r.accountID = cfg.AccountID
	r.authType = cfg.AuthType
}

func (r *SSLCheckResource) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "terraform-provider-atatus")
	if r.authType == "cookie" {
		req.Header.Set("Cookie", r.apiKey)
	} else {
		req.Header.Set("X-API-KEY", r.apiKey)
	}
}

func (r *SSLCheckResource) getLocations(ctx context.Context, locationsList types.List) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	locations := []string{"US_NEW_YORK"}

	if !locationsList.IsNull() && !locationsList.IsUnknown() {
		var elements []types.String
		diags.Append(locationsList.ElementsAs(ctx, &elements, false)...)
		if diags.HasError() {
			return locations, diags
		}

		if len(elements) > 0 {
			locations = make([]string, 0, len(elements))
			for _, el := range elements {
				locations = append(locations, el.ValueString())
			}
		}
	}
	return locations, diags
}

func (r *SSLCheckResource) getTags(ctx context.Context, tagsList types.List) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	tags := []string{}

	if !tagsList.IsNull() && !tagsList.IsUnknown() {
		var elements []types.String
		diags.Append(tagsList.ElementsAs(ctx, &elements, false)...)
		if diags.HasError() {
			return tags, diags
		}

		if len(elements) > 0 {
			tags = make([]string, 0, len(elements))
			for _, el := range elements {
				tags = append(tags, el.ValueString())
			}
		}
	}
	return tags, diags
}

func (r *SSLCheckResource) getChannelIDs(ctx context.Context, channelIDsList types.List) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	channels := []string{}

	if !channelIDsList.IsNull() && !channelIDsList.IsUnknown() {
		var elements []types.String
		diags.Append(channelIDsList.ElementsAs(ctx, &elements, false)...)
		if diags.HasError() {
			return channels, diags
		}

		if len(elements) > 0 {
			channels = make([]string, 0, len(elements))
			for _, el := range elements {
				channels = append(channels, el.ValueString())
			}
		}
	}
	return channels, diags
}

func (r *SSLCheckResource) getEmails(ctx context.Context, emailsList types.List) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	emails := []string{}

	if !emailsList.IsNull() && !emailsList.IsUnknown() {
		var elements []types.String
		diags.Append(emailsList.ElementsAs(ctx, &elements, false)...)
		if diags.HasError() {
			return emails, diags
		}

		if len(elements) > 0 {
			emails = make([]string, 0, len(elements))
			for _, el := range elements {
				emails = append(emails, el.ValueString())
			}
		}
	}
	return emails, diags
}

func (r *SSLCheckResource) getAssertions(ctx context.Context, list types.List) ([]SSLCheckAssertionModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	var assertions []SSLCheckAssertionModel
	if list.IsNull() || list.IsUnknown() {
		return assertions, diags
	}
	diags.Append(list.ElementsAs(ctx, &assertions, false)...)
	return assertions, diags
}

func (r *SSLCheckResource) buildAssertionList(ctx context.Context, assertions []SSLCheckAssertionModel) (types.List, diag.Diagnostics) {
	return types.ListValueFrom(ctx, sslAssertionElementType, assertions)
}

func (r *SSLCheckResource) fetchNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	if r.client == nil || r.apiKey == "" {
		return nil, fmt.Errorf("provider client is not configured")
	}

	apiURL := "https://www.atatus.com/v2/channels"
	httpReq, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	r.setHeaders(httpReq)

	httpResp, err := r.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to query notification channels: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("failed to fetch notification channels (HTTP %d): %s", httpResp.StatusCode, string(respBytes))
	}

	var channels []NotificationChannel
	if err := json.NewDecoder(httpResp.Body).Decode(&channels); err != nil {
		return nil, fmt.Errorf("failed to parse notification channels: %w", err)
	}

	return channels, nil
}

func (r *SSLCheckResource) resolveEmailsToChannelIDs(ctx context.Context, emails []string) ([]string, error) {
	if len(emails) == 0 {
		return nil, nil
	}

	channels, err := r.fetchNotificationChannels(ctx)
	if err != nil {
		return nil, err
	}

	emailToID := make(map[string]string)
	var availableEmails []string
	for _, ch := range channels {
		if strings.EqualFold(ch.Type, "EMAIL") {
			rec := ch.Settings.Recipient
			if rec == "" {
				rec = ch.Name
			}
			if rec != "" {
				emailToID[strings.ToLower(strings.TrimSpace(rec))] = ch.ID
				availableEmails = append(availableEmails, rec)
			}
		}
	}

	var resolvedIDs []string
	for _, email := range emails {
		clean := strings.ToLower(strings.TrimSpace(email))
		if id, ok := emailToID[clean]; ok {
			resolvedIDs = append(resolvedIDs, id)
		} else {
			if len(availableEmails) > 0 {
				return nil, fmt.Errorf("no email notification channel found for '%s' in Atatus account. Please create this notification channel in Atatus first. Available email channels in account: [%s]", email, strings.Join(availableEmails, ", "))
			}
			return nil, fmt.Errorf("no email notification channel found for '%s' in Atatus account. Please create this notification channel in Atatus first", email)
		}
	}

	return resolvedIDs, nil
}

func (r *SSLCheckResource) resolveChannelIDsToEmails(ctx context.Context, channelIDs []string) ([]string, []string, error) {
	if len(channelIDs) == 0 {
		return nil, nil, nil
	}

	channels, err := r.fetchNotificationChannels(ctx)
	if err != nil {
		return nil, nil, err
	}

	idToChannel := make(map[string]NotificationChannel)
	for _, ch := range channels {
		idToChannel[ch.ID] = ch
	}

	var emails []string
	var nonEmailChannelIDs []string

	for _, id := range channelIDs {
		if ch, ok := idToChannel[id]; ok {
			if strings.EqualFold(ch.Type, "EMAIL") {
				rec := ch.Settings.Recipient
				if rec == "" {
					rec = ch.Name
				}
				if rec != "" {
					emails = append(emails, rec)
				}
			} else {
				nonEmailChannelIDs = append(nonEmailChannelIDs, id)
			}
		} else {
			nonEmailChannelIDs = append(nonEmailChannelIDs, id)
		}
	}

	return emails, nonEmailChannelIDs, nil
}

func normalizeSSLAssertions(input []SSLCheckAssertionModel) []map[string]string {
	if len(input) == 0 {
		return []map[string]string{
			{
				"type":     "certificate",
				"property": "",
				"operator": "expiresInMoreThan",
				"target":   "10",
				"unit":     "days",
			},
			{
				"type":     "maximumTLSVersion",
				"property": "",
				"operator": "moreThanOrEqual",
				"target":   "1.3",
				"unit":     "",
			},
			{
				"type":     "responseTime",
				"property": "",
				"operator": "lessThan",
				"target":   "1000",
				"unit":     "ms",
			},
		}
	}

	var result []map[string]string
	for _, a := range input {
		aType := a.Type.ValueString()
		op := a.Operator.ValueString()
		target := a.Target.ValueString()
		prop := a.Property.ValueString()
		unit := a.Unit.ValueString()

		cleanType := strings.ToLower(strings.ReplaceAll(aType, "_", ""))
		switch cleanType {
		case "certificate":
			aType = "certificate"
			cleanOp := strings.ToLower(strings.ReplaceAll(op, "_", ""))
			if cleanOp == "expiresinlessthan" || cleanOp == "lessthan" {
				op = "expiresInLessThan"
			} else if cleanOp == "expiresinmorethan" || cleanOp == "morethan" || cleanOp == "is" {
				op = "expiresInMoreThan"
			}
			if target == "" {
				target = "10"
			}
			prop = ""
			unit = "days"

		case "maximumtlsversion", "maxtlsversion":
			aType = "maximumTLSVersion"
			cleanOp := strings.ToLower(strings.ReplaceAll(op, "_", ""))
			if cleanOp == "morethanorequal" || cleanOp == "ismorethanorequal" || cleanOp == "greaterthanorequal" {
				op = "moreThanOrEqual"
			} else if cleanOp == "lessthanorequal" || cleanOp == "islessthanorequal" {
				op = "lessThanOrEqual"
			} else if cleanOp == "lessthan" || cleanOp == "islessthan" {
				op = "lessThan"
			} else if cleanOp == "morethan" || cleanOp == "ismorethan" || cleanOp == "greaterthan" {
				op = "moreThan"
			} else if cleanOp == "is" {
				op = "is"
			}
			if target == "" {
				target = "1.3"
			}
			prop = ""
			unit = ""

		case "minimumtlsversion", "mintlsversion":
			aType = "minimumTLSVersion"
			cleanOp := strings.ToLower(strings.ReplaceAll(op, "_", ""))
			if cleanOp == "morethanorequal" || cleanOp == "ismorethanorequal" || cleanOp == "greaterthanorequal" {
				op = "moreThanOrEqual"
			} else if cleanOp == "morethan" || cleanOp == "ismorethan" || cleanOp == "greaterthan" {
				op = "moreThan"
			}
			if target == "" {
				target = "1.0"
			}
			prop = ""
			unit = ""

		case "responsetime":
			aType = "responseTime"
			op = "lessThan"
			if target == "" {
				target = "1000"
			}
			prop = ""
			unit = "ms"
		}

		result = append(result, map[string]string{
			"type":     aType,
			"property": prop,
			"operator": op,
			"target":   target,
			"unit":     unit,
		})
	}
	return result
}

func (r *SSLCheckResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	if r.client == nil || r.apiKey == "" {
		return
	}

	var plan SSLCheckModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.Frequency.IsNull() && !plan.Frequency.IsUnknown() {
		_, canonicalStr, err := parseFrequency(plan.Frequency.ValueString(), 300)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Frequency", err.Error())
			return
		}
		plan.Frequency = types.StringValue(canonicalStr)
	}

	rt := "parallel"
	if !plan.RunType.IsNull() && !plan.RunType.IsUnknown() {
		normalized, err := normalizeRunType(plan.RunType.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Invalid Run Type", err.Error())
			return
		}
		rt = normalized
		plan.RunType = types.StringValue(rt)
	}

	var config SSLCheckModel
	req.Config.Get(ctx, &config)

	if rt == "sequential" {
		if !config.MinLocationFailed.IsNull() && !config.MinLocationFailed.IsUnknown() {
			resp.Diagnostics.AddError(
				"Invalid Configuration",
				"Attribute 'min_location_failed' is not allowed when 'run_type' is 'sequential'. Minimum failed locations only applies to 'parallel' checks.",
			)
			return
		}
		plan.MinLocationFailed = types.Int64Null()
	} else {
		if plan.MinLocationFailed.IsNull() || plan.MinLocationFailed.IsUnknown() {
			plan.MinLocationFailed = types.Int64Value(1)
		}
	}

	emailsSlice, emailDiags := r.getEmails(ctx, plan.Emails)
	resp.Diagnostics.Append(emailDiags...)
	if !resp.Diagnostics.HasError() && len(emailsSlice) > 0 {
		_, err := r.resolveEmailsToChannelIDs(ctx, emailsSlice)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Email Notification Channel", err.Error())
			return
		}
	}

	assertions, aDiags := r.getAssertions(ctx, plan.Assertions)
	resp.Diagnostics.Append(aDiags...)
	if !resp.Diagnostics.HasError() {
		if len(assertions) > 0 {
			var updatedAssertions []SSLCheckAssertionModel
			for _, a := range assertions {
				if a.Type.IsNull() || a.Type.IsUnknown() {
					continue
				}
				aType := a.Type.ValueString()
				op := a.Operator.ValueString()
				target := a.Target.ValueString()
				prop := a.Property.ValueString()
				unit := a.Unit.ValueString()

				cleanType := strings.ToLower(strings.ReplaceAll(aType, "_", ""))
				switch cleanType {
				case "certificate":
					aType = "certificate"
					if op != "expiresInMoreThan" && op != "expiresInLessThan" {
						resp.Diagnostics.AddError(
							"Invalid Assertion Operator for SSL Certificate",
							fmt.Sprintf("Unsupported operator '%s' for SSL certificate assertion. Supported operators: expiresInMoreThan, expiresInLessThan", op),
						)
					}
					if target == "" {
						target = "10"
					}
					prop = ""
					unit = "days"

				case "maximumtlsversion", "maxtlsversion":
					aType = "maximumTLSVersion"
					if op != "moreThanOrEqual" && op != "is" && op != "moreThan" && op != "lessThan" && op != "lessThanOrEqual" {
						resp.Diagnostics.AddError(
							"Invalid Assertion Operator for SSL maximumTLSVersion",
							fmt.Sprintf("Unsupported operator '%s' for SSL maximumTLSVersion assertion. Supported operators: moreThanOrEqual, is, moreThan, lessThan, lessThanOrEqual", op),
						)
					}
					if target == "" {
						target = "1.3"
					}
					prop = ""
					unit = ""

				case "minimumtlsversion", "mintlsversion":
					aType = "minimumTLSVersion"
					if op != "moreThanOrEqual" && op != "moreThan" {
						resp.Diagnostics.AddError(
							"Invalid Assertion Operator for SSL minimumTLSVersion",
							fmt.Sprintf("Unsupported operator '%s' for SSL minimumTLSVersion assertion. Supported operators: moreThanOrEqual, moreThan ('is' is not supported)", op),
						)
					}
					if target == "" {
						target = "1.0"
					}
					prop = ""
					unit = ""

				case "responsetime":
					aType = "responseTime"
					if op != "lessThan" {
						resp.Diagnostics.AddError(
							"Invalid Assertion Operator for SSL responseTime",
							fmt.Sprintf("Unsupported operator '%s' for SSL responseTime assertion. Only 'lessThan' is supported ('moreThan' and 'is' are not supported).", op),
						)
					}
					op = "lessThan"
					if target == "" {
						target = "1000"
					}
					prop = ""
					unit = "ms"

				default:
					resp.Diagnostics.AddError("Invalid Assertion Type for SSL Check", fmt.Sprintf("Unsupported SSL assertion type '%s'. Supported types for SSL check: certificate, maximumTLSVersion, minimumTLSVersion, responseTime", aType))
				}

				updatedAssertions = append(updatedAssertions, SSLCheckAssertionModel{
					Type:     types.StringValue(aType),
					Operator: types.StringValue(op),
					Target:   types.StringValue(target),
					Property: types.StringValue(prop),
					Unit:     types.StringValue(unit),
				})
			}

			if !resp.Diagnostics.HasError() && len(updatedAssertions) > 0 {
				aList, aBuildDiags := r.buildAssertionList(ctx, updatedAssertions)
				resp.Diagnostics.Append(aBuildDiags...)
				if !resp.Diagnostics.HasError() {
					plan.Assertions = aList
				}
			}
		} else if plan.Assertions.IsNull() || plan.Assertions.IsUnknown() {
			defaultAssertions := []SSLCheckAssertionModel{
				{
					Type:     types.StringValue("certificate"),
					Operator: types.StringValue("expiresInMoreThan"),
					Target:   types.StringValue("10"),
					Property: types.StringValue(""),
					Unit:     types.StringValue("days"),
				},
				{
					Type:     types.StringValue("maximumTLSVersion"),
					Operator: types.StringValue("moreThanOrEqual"),
					Target:   types.StringValue("1.3"),
					Property: types.StringValue(""),
					Unit:     types.StringValue(""),
				},
				{
					Type:     types.StringValue("responseTime"),
					Operator: types.StringValue("lessThan"),
					Target:   types.StringValue("1000"),
					Property: types.StringValue(""),
					Unit:     types.StringValue("ms"),
				},
			}
			aList, aBuildDiags := r.buildAssertionList(ctx, defaultAssertions)
			resp.Diagnostics.Append(aBuildDiags...)
			if !resp.Diagnostics.HasError() {
				plan.Assertions = aList
			}
		}
	}

	if !resp.Diagnostics.HasError() {
		resp.Plan.Set(ctx, &plan)
	}
}

func (r *SSLCheckResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SSLCheckModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	locations, locDiags := r.getLocations(ctx, plan.Locations)
	resp.Diagnostics.Append(locDiags...)

	tags, tagDiags := r.getTags(ctx, plan.Tags)
	resp.Diagnostics.Append(tagDiags...)

	channelSlice, channelDiags := r.getChannelIDs(ctx, plan.ChannelIDs)
	resp.Diagnostics.Append(channelDiags...)

	emailsSlice, emailDiags := r.getEmails(ctx, plan.Emails)
	resp.Diagnostics.Append(emailDiags...)

	assertions, aDiags := r.getAssertions(ctx, plan.Assertions)
	resp.Diagnostics.Append(aDiags...)

	if resp.Diagnostics.HasError() {
		return
	}

	var apiChannelIDs []string
	apiChannelIDs = append(apiChannelIDs, channelSlice...)

	if len(emailsSlice) > 0 {
		emailChannelIDs, err := r.resolveEmailsToChannelIDs(ctx, emailsSlice)
		if err != nil {
			resp.Diagnostics.AddError("Email Resolution Error", err.Error())
			return
		}
		apiChannelIDs = append(apiChannelIDs, emailChannelIDs...)
	}

	allAPIChannelIDs := deduplicateStrings(apiChannelIDs)

	retryCount := int64(0)
	if !plan.RetryCount.IsNull() && !plan.RetryCount.IsUnknown() {
		retryCount = plan.RetryCount.ValueInt64()
	}

	minLocationFailed := int64(1)
	if !plan.MinLocationFailed.IsNull() && !plan.MinLocationFailed.IsUnknown() {
		minLocationFailed = plan.MinLocationFailed.ValueInt64()
	}

	port := int64(443)
	if !plan.Port.IsNull() && !plan.Port.IsUnknown() && plan.Port.ValueInt64() > 0 {
		port = plan.Port.ValueInt64()
	}

	payloadAssertions := normalizeSSLAssertions(assertions)

	host := cleanHost(plan.Host.ValueString())

	freqSec, _, _ := parseFrequency(plan.Frequency.ValueString(), 300)

	runType, _ := normalizeRunType(plan.RunType.ValueString())

	var minLocFailedPtr *int64
	if runType != "sequential" {
		minLocFailedPtr = &minLocationFailed
	}

	payload := SSLCheckPayload{
		Name:              plan.Name.ValueString(),
		Type:              "api",
		Subtype:           "ssl",
		Host:              host,
		Server:            "",
		Port:              port,
		Frequency:         freqSec,
		MinLocationFailed: minLocFailedPtr,
		RetryCount:        retryCount,
		ChannelIDs:        allAPIChannelIDs,
		RunType:           runType,
		Locations:         locations,
		Tags:              tags,
		Assertions:        payloadAssertions,
		Request: map[string]interface{}{
			"method":                  "GET",
			"url":                     host,
			"followRedirects":         true,
			"ignoreServerCertificate": false,
			"dontSaveResBody":         false,
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		resp.Diagnostics.AddError("JSON Encoding Error", err.Error())
		return
	}

	apiURL := "https://www.atatus.com/v2/synthetic/checks"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		resp.Diagnostics.AddError("Request Creation Error", err.Error())
		return
	}

	r.setHeaders(httpReq)

	httpResp, err := r.client.Do(httpReq)
	if err != nil {
		resp.Diagnostics.AddError("API Request Failed", err.Error())
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError("API Failure On Create", fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, string(respBytes)))
		return
	}

	var result SSLCheckResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&result); err != nil {
		resp.Diagnostics.AddError("JSON Decode Error", err.Error())
		return
	}

	checkID := result.ID
	if checkID == "" {
		checkID = result.IDAlt
	}
	plan.ID = types.StringValue(checkID)
	plan.Port = types.Int64Value(port)
	plan.RetryCount = types.Int64Value(retryCount)
	if runType == "sequential" {
		plan.MinLocationFailed = types.Int64Null()
	} else {
		plan.MinLocationFailed = types.Int64Value(minLocationFailed)
	}
	plan.RunType = types.StringValue(runType)

	locList, listDiags := types.ListValueFrom(ctx, types.StringType, locations)
	resp.Diagnostics.Append(listDiags...)
	plan.Locations = locList

	if len(tags) > 0 {
		tagsList, tagListDiags := types.ListValueFrom(ctx, types.StringType, tags)
		resp.Diagnostics.Append(tagListDiags...)
		plan.Tags = tagsList
	} else {
		plan.Tags = types.ListNull(types.StringType)
	}

	if len(channelSlice) > 0 {
		chList, listDiags := types.ListValueFrom(ctx, types.StringType, channelSlice)
		resp.Diagnostics.Append(listDiags...)
		plan.ChannelIDs = chList
	} else {
		plan.ChannelIDs = types.ListNull(types.StringType)
	}

	if len(emailsSlice) > 0 {
		emList, listDiags := types.ListValueFrom(ctx, types.StringType, emailsSlice)
		resp.Diagnostics.Append(listDiags...)
		plan.Emails = emList
	} else {
		plan.Emails = types.ListNull(types.StringType)
	}

	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

func (r *SSLCheckResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SSLCheckModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiURL := fmt.Sprintf("https://www.atatus.com/v2/synthetic/checks/%s", state.ID.ValueString())
	httpReq, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		resp.Diagnostics.AddError("Request Creation Error", err.Error())
		return
	}

	r.setHeaders(httpReq)

	httpResp, err := r.client.Do(httpReq)
	if err != nil {
		resp.Diagnostics.AddError("API Read Failed", err.Error())
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNotFound {
		resp.State.RemoveResource(ctx)
		return
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError("API Failure On Read", fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, string(respBytes)))
		return
	}

	var result SSLCheckResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&result); err != nil {
		resp.Diagnostics.AddError("JSON Decode Error", err.Error())
		return
	}

	state.Name = types.StringValue(result.Name)
	state.Host = types.StringValue(result.Host)
	if result.Port > 0 {
		state.Port = types.Int64Value(result.Port)
	} else {
		state.Port = types.Int64Value(443)
	}
	state.Frequency = types.StringValue(secondsToFrequencyString(result.Frequency))
	state.RetryCount = types.Int64Value(result.RetryCount)

	runType := "parallel"
	if result.RunType != "" {
		runType, _ = normalizeRunType(result.RunType)
		state.RunType = types.StringValue(runType)
	} else if state.RunType.IsNull() || state.RunType.IsUnknown() || state.RunType.ValueString() == "" {
		state.RunType = types.StringValue("parallel")
	}

	if runType == "sequential" {
		state.MinLocationFailed = types.Int64Null()
	} else {
		minLoc := result.MinLocationFailed
		if minLoc <= 0 {
			minLoc = 1
		}
		state.MinLocationFailed = types.Int64Value(minLoc)
	}

	if len(result.Locations) > 0 {
		locationsList, listDiags := types.ListValueFrom(ctx, types.StringType, result.Locations)
		resp.Diagnostics.Append(listDiags...)
		if !resp.Diagnostics.HasError() {
			state.Locations = locationsList
		}
	}

	if len(result.Tags) > 0 {
		tagsList, listDiags := types.ListValueFrom(ctx, types.StringType, result.Tags)
		resp.Diagnostics.Append(listDiags...)
		if !resp.Diagnostics.HasError() {
			state.Tags = tagsList
		}
	} else {
		state.Tags = types.ListNull(types.StringType)
	}

	if len(result.ChannelIDs) > 0 {
		resolvedEmails, nonEmailChannelIDs, err := r.resolveChannelIDsToEmails(ctx, result.ChannelIDs)
		if err == nil {
			if len(resolvedEmails) > 0 {
				emList, listDiags := types.ListValueFrom(ctx, types.StringType, resolvedEmails)
				resp.Diagnostics.Append(listDiags...)
				if !resp.Diagnostics.HasError() {
					state.Emails = emList
				}
			} else {
				state.Emails = types.ListNull(types.StringType)
			}

			if len(nonEmailChannelIDs) > 0 {
				chList, listDiags := types.ListValueFrom(ctx, types.StringType, nonEmailChannelIDs)
				resp.Diagnostics.Append(listDiags...)
				if !resp.Diagnostics.HasError() {
					state.ChannelIDs = chList
				}
			} else {
				state.ChannelIDs = types.ListNull(types.StringType)
			}
		}
	} else {
		state.ChannelIDs = types.ListNull(types.StringType)
		state.Emails = types.ListNull(types.StringType)
	}

	if len(result.Assertions) > 0 {
		prevAssertions, _ := r.getAssertions(ctx, state.Assertions)
		prevTypeMap := make(map[int]string)
		prevOpMap := make(map[int]string)
		for idx, pa := range prevAssertions {
			if !pa.Type.IsNull() && !pa.Type.IsUnknown() {
				prevTypeMap[idx] = pa.Type.ValueString()
			}
			if !pa.Operator.IsNull() && !pa.Operator.IsUnknown() {
				prevOpMap[idx] = pa.Operator.ValueString()
			}
		}

		var assertionModels []SSLCheckAssertionModel
		for idx, a := range result.Assertions {
			apiType := a["type"]
			apiOp := a["operator"]

			finalType := apiType
			if prevT, ok := prevTypeMap[idx]; ok && strings.EqualFold(strings.ReplaceAll(prevT, "_", ""), strings.ReplaceAll(apiType, "_", "")) {
				finalType = prevT
			}

			finalOp := apiOp
			if prevO, ok := prevOpMap[idx]; ok {
				cleanPrev := strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(prevO, "_", ""), "is"))
				cleanAPI := strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(apiOp, "_", ""), "is"))
				if cleanPrev == cleanAPI {
					finalOp = prevO
				}
			}

			assertionModels = append(assertionModels, SSLCheckAssertionModel{
				Type:     types.StringValue(finalType),
				Operator: types.StringValue(finalOp),
				Target:   types.StringValue(a["target"]),
				Property: types.StringValue(a["property"]),
				Unit:     types.StringValue(a["unit"]),
			})
		}
		aList, aDiags := r.buildAssertionList(ctx, assertionModels)
		resp.Diagnostics.Append(aDiags...)
		if !resp.Diagnostics.HasError() {
			state.Assertions = aList
		}
	} else {
		defaultAssertions := []SSLCheckAssertionModel{
			{
				Type:     types.StringValue("certificate"),
				Operator: types.StringValue("expiresInMoreThan"),
				Target:   types.StringValue("10"),
				Property: types.StringValue(""),
				Unit:     types.StringValue("days"),
			},
			{
				Type:     types.StringValue("maximumTLSVersion"),
				Operator: types.StringValue("moreThanOrEqual"),
				Target:   types.StringValue("1.3"),
				Property: types.StringValue(""),
				Unit:     types.StringValue(""),
			},
			{
				Type:     types.StringValue("responseTime"),
				Operator: types.StringValue("lessThan"),
				Target:   types.StringValue("1000"),
				Property: types.StringValue(""),
				Unit:     types.StringValue("ms"),
			},
		}
		aList, aDiags := r.buildAssertionList(ctx, defaultAssertions)
		resp.Diagnostics.Append(aDiags...)
		if !resp.Diagnostics.HasError() {
			state.Assertions = aList
		}
	}

	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
}

func (r *SSLCheckResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SSLCheckModel
	var state SSLCheckModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = state.ID

	locations, locDiags := r.getLocations(ctx, plan.Locations)
	resp.Diagnostics.Append(locDiags...)

	tags, tagDiags := r.getTags(ctx, plan.Tags)
	resp.Diagnostics.Append(tagDiags...)

	channelSlice, channelDiags := r.getChannelIDs(ctx, plan.ChannelIDs)
	resp.Diagnostics.Append(channelDiags...)

	emailsSlice, emailDiags := r.getEmails(ctx, plan.Emails)
	resp.Diagnostics.Append(emailDiags...)

	assertions, aDiags := r.getAssertions(ctx, plan.Assertions)
	resp.Diagnostics.Append(aDiags...)

	if resp.Diagnostics.HasError() {
		return
	}

	var apiChannelIDs []string
	apiChannelIDs = append(apiChannelIDs, channelSlice...)

	if len(emailsSlice) > 0 {
		emailChannelIDs, err := r.resolveEmailsToChannelIDs(ctx, emailsSlice)
		if err != nil {
			resp.Diagnostics.AddError("Email Resolution Error", err.Error())
			return
		}
		apiChannelIDs = append(apiChannelIDs, emailChannelIDs...)
	}

	allAPIChannelIDs := deduplicateStrings(apiChannelIDs)

	retryCount := int64(0)
	if !plan.RetryCount.IsNull() && !plan.RetryCount.IsUnknown() {
		retryCount = plan.RetryCount.ValueInt64()
	}

	minLocationFailed := int64(1)
	if !plan.MinLocationFailed.IsNull() && !plan.MinLocationFailed.IsUnknown() {
		minLocationFailed = plan.MinLocationFailed.ValueInt64()
	}

	port := int64(443)
	if !plan.Port.IsNull() && !plan.Port.IsUnknown() && plan.Port.ValueInt64() > 0 {
		port = plan.Port.ValueInt64()
	}

	payloadAssertions := normalizeSSLAssertions(assertions)

	host := cleanHost(plan.Host.ValueString())

	freqSec, _, _ := parseFrequency(plan.Frequency.ValueString(), 300)

	runType, _ := normalizeRunType(plan.RunType.ValueString())

	payload := map[string]interface{}{
		"name":       plan.Name.ValueString(),
		"host":       host,
		"server":     "",
		"port":       port,
		"frequency":  freqSec,
		"retryCount": retryCount,
		"channelIds": allAPIChannelIDs,
		"runType":    runType,
		"locations":  locations,
		"tags":       tags,
		"assertions": payloadAssertions,
		"request": map[string]interface{}{
			"method":                  "GET",
			"url":                     host,
			"followRedirects":         true,
			"ignoreServerCertificate": false,
			"dontSaveResBody":         false,
		},
	}
	if runType != "sequential" {
		payload["minLocationFailed"] = minLocationFailed
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		resp.Diagnostics.AddError("JSON Encoding Error", err.Error())
		return
	}

	apiURL := fmt.Sprintf("https://www.atatus.com/v2/synthetic/checks/%s", state.ID.ValueString())
	httpReq, err := http.NewRequestWithContext(ctx, "PATCH", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		resp.Diagnostics.AddError("Request Creation Error", err.Error())
		return
	}

	r.setHeaders(httpReq)

	httpResp, err := r.client.Do(httpReq)
	if err != nil {
		resp.Diagnostics.AddError("API Request Failed", err.Error())
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError("API Failure On Update", fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, string(respBytes)))
		return
	}

	plan.Port = types.Int64Value(port)
	plan.RetryCount = types.Int64Value(retryCount)
	if runType == "sequential" {
		plan.MinLocationFailed = types.Int64Null()
	} else {
		plan.MinLocationFailed = types.Int64Value(minLocationFailed)
	}
	plan.RunType = types.StringValue(runType)

	locList, listDiags := types.ListValueFrom(ctx, types.StringType, locations)
	resp.Diagnostics.Append(listDiags...)
	plan.Locations = locList

	if len(tags) > 0 {
		tagsList, tagListDiags := types.ListValueFrom(ctx, types.StringType, tags)
		resp.Diagnostics.Append(tagListDiags...)
		plan.Tags = tagsList
	} else {
		plan.Tags = types.ListNull(types.StringType)
	}

	if len(channelSlice) > 0 {
		chList, listDiags := types.ListValueFrom(ctx, types.StringType, channelSlice)
		resp.Diagnostics.Append(listDiags...)
		plan.ChannelIDs = chList
	} else {
		plan.ChannelIDs = types.ListNull(types.StringType)
	}

	if len(emailsSlice) > 0 {
		emList, listDiags := types.ListValueFrom(ctx, types.StringType, emailsSlice)
		resp.Diagnostics.Append(listDiags...)
		plan.Emails = emList
	} else {
		plan.Emails = types.ListNull(types.StringType)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SSLCheckResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SSLCheckModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiURL := fmt.Sprintf("https://www.atatus.com/v2/synthetic/checks/%s", state.ID.ValueString())
	httpReq, err := http.NewRequestWithContext(ctx, "DELETE", apiURL, nil)
	if err != nil {
		resp.Diagnostics.AddError("Request Creation Error", err.Error())
		return
	}

	r.setHeaders(httpReq)

	httpResp, err := r.client.Do(httpReq)
	if err != nil {
		resp.Diagnostics.AddError("API Request Failed", err.Error())
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError("API Failure On Delete", fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, string(respBytes)))
		return
	}
}
