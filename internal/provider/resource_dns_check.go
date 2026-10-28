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
	_ resource.Resource               = &DNSCheckResource{}
	_ resource.ResourceWithConfigure  = &DNSCheckResource{}
	_ resource.ResourceWithModifyPlan = &DNSCheckResource{}
)

func NewDNSCheckResource() resource.Resource {
	return &DNSCheckResource{}
}

type DNSCheckResource struct {
	client    *http.Client
	apiKey    string
	accountID string
	authType  string
}

type DNSCheckAssertionModel struct {
	Type     types.String `tfsdk:"type"`
	Operator types.String `tfsdk:"operator"`
	Target   types.String `tfsdk:"target"`
	Property types.String `tfsdk:"property"`
	Unit     types.String `tfsdk:"unit"`
}

type DNSCheckModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Host              types.String `tfsdk:"host"`
	DNSServer         types.String `tfsdk:"dns_server"`
	Port              types.Int64  `tfsdk:"port"`
	Frequency         types.String `tfsdk:"frequency"`
	Locations         types.List   `tfsdk:"locations"`
	Tags              types.List   `tfsdk:"tags"`
	RetryCount        types.Int64  `tfsdk:"retry_count"`
	MinLocationFailed types.Int64  `tfsdk:"min_location_failed"`
	RunType           types.String `tfsdk:"run_type"`
	ChannelIDs        types.List   `tfsdk:"channel_ids"`
	Emails            types.List   `tfsdk:"emails"`
	Assertions        types.List   `tfsdk:"assertion"`
}

type DNSCheckPayload struct {
	Name              string                 `json:"name"`
	Type              string                 `json:"type"`
	Subtype           string                 `json:"subtype"`
	Host              string                 `json:"host"`
	Server            string                 `json:"server"`
	Port              int64                  `json:"port,omitempty"`
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

type DNSCheckResponse struct {
	ID                string              `json:"_id"`
	IDAlt             string              `json:"id"`
	Name              string              `json:"name"`
	Host              string              `json:"host"`
	Server            string              `json:"server"`
	Port              *int64              `json:"port"`
	Subtype           string              `json:"subtype"`
	Frequency         int64               `json:"frequency"`
	MinLocationFailed int64               `json:"minLocationFailed"`
	RetryCount        int64               `json:"retryCount"`
	ChannelIDs        []string            `json:"channelIds"`
	RunType           string              `json:"runType"`
	Locations         []string            `json:"locations"`
	Tags              []string            `json:"tags"`
	Assertions        []map[string]string `json:"assertions"`
	AccountID         string              `json:"accountId"`
	Request           HTTPCheckReqData    `json:"request"`
}

var dnsAssertionElementType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"type":     types.StringType,
		"operator": types.StringType,
		"target":   types.StringType,
		"property": types.StringType,
		"unit":     types.StringType,
	},
}

func (r *DNSCheckResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_check"
}

func (r *DNSCheckResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Atatus Synthetic DNS Check.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Identifier for the synthetic DNS check.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the synthetic DNS check.",
				Required:    true,
			},
			"host": schema.StringAttribute{
				Description: "Domain name or hostname to query (e.g. 'example.com' or 'www.google.com').",
				Required:    true,
			},
			"dns_server": schema.StringAttribute{
				Description: "Custom DNS Server IP address to query (e.g. '8.8.8.8', '1.1.1.1', '9.9.9.9'). Defaults to empty for system resolver.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
			},
			"port": schema.Int64Attribute{
				Description: "DNS port number (default: 53).",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(53),
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
				Description: "List of assertion rules to validate DNS records and resolution response time.",
				Optional:    true,
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Description: "Assertion type: atLeastOneRecord, everyAvailableRecord, responseTime.",
							Required:    true,
						},
						"property": schema.StringAttribute{
							Description: "DNS Record Type: A, AAAA, CNAME, MX, NS, TXT (used with atLeastOneRecord / everyAvailableRecord).",
							Optional:    true,
							Computed:    true,
							Default:     stringdefault.StaticString(""),
						},
						"operator": schema.StringAttribute{
							Description: "Comparison operator: is, contains, matchesRegex (or matches_regex), doesNotMatchRegex (or does_not_match_regex), lessThan (for responseTime).",
							Required:    true,
						},
						"target": schema.StringAttribute{
							Description: "Expected target value or regex pattern (e.g. '93.184.216.34' or '1000').",
							Optional:    true,
							Computed:    true,
							Default:     stringdefault.StaticString(""),
						},
						"unit": schema.StringAttribute{
							Description: "Measurement unit ('ms' for responseTime, empty otherwise).",
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

func (r *DNSCheckResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *DNSCheckResource) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "terraform-provider-atatus")
	if r.authType == "cookie" {
		req.Header.Set("Cookie", r.apiKey)
	} else {
		req.Header.Set("X-API-KEY", r.apiKey)
	}
}

func (r *DNSCheckResource) getLocations(ctx context.Context, locationsList types.List) ([]string, diag.Diagnostics) {
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

func (r *DNSCheckResource) getTags(ctx context.Context, tagsList types.List) ([]string, diag.Diagnostics) {
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

func (r *DNSCheckResource) getChannelIDs(ctx context.Context, channelIDsList types.List) ([]string, diag.Diagnostics) {
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

func (r *DNSCheckResource) getEmails(ctx context.Context, emailsList types.List) ([]string, diag.Diagnostics) {
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

func (r *DNSCheckResource) getAssertions(ctx context.Context, list types.List) ([]DNSCheckAssertionModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	var assertions []DNSCheckAssertionModel
	if list.IsNull() || list.IsUnknown() {
		return assertions, diags
	}
	diags.Append(list.ElementsAs(ctx, &assertions, false)...)
	return assertions, diags
}

func (r *DNSCheckResource) buildAssertionList(ctx context.Context, assertions []DNSCheckAssertionModel) (types.List, diag.Diagnostics) {
	return types.ListValueFrom(ctx, dnsAssertionElementType, assertions)
}

func (r *DNSCheckResource) fetchNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
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
		return nil, fmt.Errorf("channels API error HTTP %d: %s", httpResp.StatusCode, string(respBytes))
	}

	var channelList []NotificationChannel
	if err := json.NewDecoder(httpResp.Body).Decode(&channelList); err != nil {
		return nil, fmt.Errorf("failed to decode channels response: %w", err)
	}

	return channelList, nil
}

func (r *DNSCheckResource) resolveEmailsToChannelIDs(ctx context.Context, emails []string) ([]string, error) {
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

func (r *DNSCheckResource) resolveChannelIDsToEmails(ctx context.Context, channelIDs []string) ([]string, []string, error) {
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

func normalizeDNSAssertions(input []DNSCheckAssertionModel) []map[string]string {
	if len(input) == 0 {
		return []map[string]string{
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
		case "responsetime":
			aType = "responseTime"
			cleanOp := strings.ToLower(strings.ReplaceAll(op, "_", ""))
			if cleanOp == "lessthan" || cleanOp == "islessthan" || cleanOp == "less_than" {
				op = "lessThan"
			}
			if target == "" {
				target = "1000"
			}
			prop = ""
			unit = "ms"

		case "atleastonerecord", "atleastone":
			aType = "atLeastOneRecord"
			cleanOp := strings.ToLower(strings.ReplaceAll(op, "_", ""))
			if cleanOp == "matchesregex" || cleanOp == "matches_regex" || cleanOp == "regex" {
				op = "matchesRegex"
			} else if cleanOp == "doesnotmatchregex" || cleanOp == "doesnotmatchesregex" || cleanOp == "does_not_match_regex" || cleanOp == "does_not_matches_regex" || cleanOp == "notmatchesregex" {
				op = "doesNotMatchesRegex"
			} else if cleanOp == "doesnotcontain" || cleanOp == "does_not_contain" {
				op = "doesNotContain"
			} else if cleanOp == "contains" {
				op = "contains"
			} else if cleanOp == "is" {
				op = "is"
			}
			prop = strings.ToUpper(strings.TrimSpace(prop))
			unit = ""

		case "everyavailablerecord", "everyrecord", "everyavailable":
			aType = "everyAvailableRecord"
			cleanOp := strings.ToLower(strings.ReplaceAll(op, "_", ""))
			if cleanOp == "matchesregex" || cleanOp == "matches_regex" || cleanOp == "regex" {
				op = "matchesRegex"
			} else if cleanOp == "doesnotmatchregex" || cleanOp == "doesnotmatchesregex" || cleanOp == "does_not_match_regex" || cleanOp == "does_not_matches_regex" || cleanOp == "notmatchesregex" {
				op = "doesNotMatchesRegex"
			} else if cleanOp == "doesnotcontain" || cleanOp == "does_not_contain" {
				op = "doesNotContain"
			} else if cleanOp == "contains" {
				op = "contains"
			} else if cleanOp == "is" {
				op = "is"
			}
			prop = strings.ToUpper(strings.TrimSpace(prop))
			unit = ""
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

func (r *DNSCheckResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	if r.client == nil || r.apiKey == "" {
		return
	}

	var plan DNSCheckModel
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

	var config DNSCheckModel
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
			var updatedAssertions []DNSCheckAssertionModel
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
				case "responsetime":
					if target == "" {
						target = "1000"
					}
					prop = ""
					unit = "ms"

				case "atleastonerecord", "atleastone":
					if prop == "" {
						prop = "A"
					} else {
						prop = strings.ToUpper(strings.TrimSpace(prop))
					}
					unit = ""

				case "everyavailablerecord", "everyrecord", "everyavailable":
					if prop == "" {
						prop = "A"
					} else {
						prop = strings.ToUpper(strings.TrimSpace(prop))
					}
					unit = ""

				default:
					resp.Diagnostics.AddError("Invalid Assertion Type for DNS Check", fmt.Sprintf("Unsupported DNS assertion type '%s'. Supported types for DNS check: atLeastOneRecord, everyAvailableRecord, responseTime", aType))
				}

				updatedAssertions = append(updatedAssertions, DNSCheckAssertionModel{
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
			defaultAssertions := []DNSCheckAssertionModel{
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

func (r *DNSCheckResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan DNSCheckModel
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

	port := int64(53)
	if !plan.Port.IsNull() && !plan.Port.IsUnknown() && plan.Port.ValueInt64() > 0 {
		port = plan.Port.ValueInt64()
	}

	dnsServer := ""
	if !plan.DNSServer.IsNull() && !plan.DNSServer.IsUnknown() {
		dnsServer = strings.TrimSpace(plan.DNSServer.ValueString())
	}

	payloadAssertions := normalizeDNSAssertions(assertions)

	host := cleanHost(plan.Host.ValueString())

	freqSec, _, _ := parseFrequency(plan.Frequency.ValueString(), 300)

	runType, _ := normalizeRunType(plan.RunType.ValueString())

	var minLocFailedPtr *int64
	if runType != "sequential" {
		minLocFailedPtr = &minLocationFailed
	}

	payload := DNSCheckPayload{
		Name:              plan.Name.ValueString(),
		Type:              "api",
		Subtype:           "dns",
		Host:              host,
		Server:            dnsServer,
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

	var result DNSCheckResponse
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
	plan.DNSServer = types.StringValue(dnsServer)
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

func (r *DNSCheckResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state DNSCheckModel
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

	var result DNSCheckResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&result); err != nil {
		resp.Diagnostics.AddError("JSON Decode Error", err.Error())
		return
	}

	state.Name = types.StringValue(result.Name)
	state.Host = types.StringValue(result.Host)
	state.DNSServer = types.StringValue(result.Server)

	if result.Port != nil && *result.Port > 0 {
		state.Port = types.Int64Value(*result.Port)
	} else if !state.Port.IsNull() && state.Port.ValueInt64() > 0 {
		// keep state port
	} else {
		state.Port = types.Int64Value(53)
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
		prevPropMap := make(map[int]string)
		for idx, pa := range prevAssertions {
			if !pa.Type.IsNull() && !pa.Type.IsUnknown() {
				prevTypeMap[idx] = pa.Type.ValueString()
			}
			if !pa.Operator.IsNull() && !pa.Operator.IsUnknown() {
				prevOpMap[idx] = pa.Operator.ValueString()
			}
			if !pa.Property.IsNull() && !pa.Property.IsUnknown() {
				prevPropMap[idx] = pa.Property.ValueString()
			}
		}

		var assertionModels []DNSCheckAssertionModel
		for idx, a := range result.Assertions {
			apiType := a["type"]
			apiOp := a["operator"]
			apiProp := a["property"]

			finalType := apiType
			if prevT, ok := prevTypeMap[idx]; ok && strings.EqualFold(strings.ReplaceAll(prevT, "_", ""), strings.ReplaceAll(apiType, "_", "")) {
				finalType = prevT
			}

			finalOp := apiOp
			if prevO, ok := prevOpMap[idx]; ok {
				cleanPrev := strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(prevO, "_", ""), "is"))
				cleanAPI := strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(apiOp, "_", ""), "is"))
				cleanPrev = strings.ReplaceAll(cleanPrev, "matches", "match")
				cleanAPI = strings.ReplaceAll(cleanAPI, "matches", "match")
				if cleanPrev == cleanAPI {
					finalOp = prevO
				}
			}

			finalProp := apiProp
			if prevP, ok := prevPropMap[idx]; ok && strings.EqualFold(prevP, apiProp) {
				finalProp = prevP
			}

			assertionModels = append(assertionModels, DNSCheckAssertionModel{
				Type:     types.StringValue(finalType),
				Operator: types.StringValue(finalOp),
				Target:   types.StringValue(a["target"]),
				Property: types.StringValue(finalProp),
				Unit:     types.StringValue(a["unit"]),
			})
		}
		aList, aDiags := r.buildAssertionList(ctx, assertionModels)
		resp.Diagnostics.Append(aDiags...)
		if !resp.Diagnostics.HasError() {
			state.Assertions = aList
		}
	} else {
		defaultAssertions := []DNSCheckAssertionModel{
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

func (r *DNSCheckResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan DNSCheckModel
	var state DNSCheckModel

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

	port := int64(53)
	if !plan.Port.IsNull() && !plan.Port.IsUnknown() && plan.Port.ValueInt64() > 0 {
		port = plan.Port.ValueInt64()
	}

	dnsServer := ""
	if !plan.DNSServer.IsNull() && !plan.DNSServer.IsUnknown() {
		dnsServer = strings.TrimSpace(plan.DNSServer.ValueString())
	}

	payloadAssertions := normalizeDNSAssertions(assertions)

	host := cleanHost(plan.Host.ValueString())

	freqSec, _, _ := parseFrequency(plan.Frequency.ValueString(), 300)

	runType, _ := normalizeRunType(plan.RunType.ValueString())

	payload := map[string]interface{}{
		"name":       plan.Name.ValueString(),
		"host":       host,
		"server":     dnsServer,
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
		resp.Diagnostics.AddError("API Update Failed", err.Error())
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError("API Failure On Update", fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, string(respBytes)))
		return
	}

	plan.Port = types.Int64Value(port)
	plan.DNSServer = types.StringValue(dnsServer)
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

func (r *DNSCheckResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state DNSCheckModel
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
		resp.Diagnostics.AddError("API Delete Failed", err.Error())
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNotFound {
		return
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError("API Failure On Delete", fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, string(respBytes)))
		return
	}
}
