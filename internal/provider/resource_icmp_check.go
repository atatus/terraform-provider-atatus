package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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
	_ resource.Resource               = &ICMPCheckResource{}
	_ resource.ResourceWithConfigure  = &ICMPCheckResource{}
	_ resource.ResourceWithModifyPlan = &ICMPCheckResource{}
)

func NewICMPCheckResource() resource.Resource {
	return &ICMPCheckResource{}
}

type ICMPCheckResource struct {
	client    *http.Client
	apiKey    string
	accountID string
	authType  string
}

type ICMPCheckAssertionModel struct {
	Type     types.String `tfsdk:"type"`
	Operator types.String `tfsdk:"operator"`
	Target   types.String `tfsdk:"target"`
	Property types.String `tfsdk:"property"`
}

type ICMPCheckModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Host              types.String `tfsdk:"host"`
	Packets           types.Int64  `tfsdk:"packets"`
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

type ICMPCheckPayload struct {
	Name              string                 `json:"name"`
	Type              string                 `json:"type"`
	Subtype           string                 `json:"subtype"`
	Host              string                 `json:"host"`
	IcmpSendPackets   string                 `json:"icmpSendPackets"`
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

type ICMPCheckResponse struct {
	ID                string              `json:"_id"`
	IDAlt             string              `json:"id"`
	Name              string              `json:"name"`
	Host              string              `json:"host"`
	IcmpSendPackets   string              `json:"icmpSendPackets"`
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

var icmpAssertionElementType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"type":     types.StringType,
		"operator": types.StringType,
		"target":   types.StringType,
		"property": types.StringType,
	},
}

func (r *ICMPCheckResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_icmp_check"
}

func (r *ICMPCheckResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Atatus Synthetic ICMP (Ping) Check.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Identifier for the synthetic ICMP check.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the synthetic ICMP check.",
				Required:    true,
			},
			"host": schema.StringAttribute{
				Description: "Host domain name (e.g. 'example.com', 'www.google.com') or IPv4 address (e.g. '8.8.8.8') to ping.",
				Required:    true,
			},
			"packets": schema.Int64Attribute{
				Description: "Number of ICMP packets to send per test run. Allowed range is 1 to 10 (default: 10).",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(10),
				Validators: []validator.Int64{
					int64validator.Between(1, 10),
				},
			},
			"frequency": schema.StringAttribute{
				Description: "Execution frequency interval ('1m', '5m', '10m', '15m', '30m', '1h', '6h', '12h', '1d'). Default: '1m'.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("1m"),
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
				Description: "List of assertion rules to validate ICMP ping latency, packet loss, or packets received.",
				Optional:    true,
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Description: "Assertion type: 'latency', 'packetLoss', or 'packetsReceived'.",
							Required:    true,
						},
						"property": schema.StringAttribute{
							Description: "Property for latency assertions: 'avg', 'min', 'max', or 'stddev'. Empty for packetLoss and packetsReceived.",
							Optional:    true,
							Computed:    true,
							Default:     stringdefault.StaticString(""),
						},
						"operator": schema.StringAttribute{
							Description: "Comparison operator: 'is', 'lessThan', 'lessThanOrEqual', 'moreThan', 'moreThanOrEqual'.",
							Required:    true,
						},
						"target": schema.StringAttribute{
							Description: "Expected target value threshold (e.g. '1000' for latency, '0' for packetLoss).",
							Required:    true,
						},
					},
				},
			},
		},
	}
}

func (r *ICMPCheckResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ICMPCheckResource) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "terraform-provider-atatus")
	if r.authType == "cookie" {
		req.Header.Set("Cookie", r.apiKey)
	} else {
		req.Header.Set("X-API-KEY", r.apiKey)
	}
}

func (r *ICMPCheckResource) getLocations(ctx context.Context, locationsList types.List) ([]string, diag.Diagnostics) {
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

func (r *ICMPCheckResource) getTags(ctx context.Context, tagsList types.List) ([]string, diag.Diagnostics) {
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

func (r *ICMPCheckResource) getChannelIDs(ctx context.Context, channelIDsList types.List) ([]string, diag.Diagnostics) {
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

func (r *ICMPCheckResource) getEmails(ctx context.Context, emailsList types.List) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	var emails []string

	if !emailsList.IsNull() && !emailsList.IsUnknown() {
		var elements []types.String
		diags.Append(emailsList.ElementsAs(ctx, &elements, false)...)
		if diags.HasError() {
			return emails, diags
		}

		for _, el := range elements {
			if !el.IsNull() && !el.IsUnknown() && el.ValueString() != "" {
				emails = append(emails, el.ValueString())
			}
		}
	}
	return emails, diags
}

func (r *ICMPCheckResource) getAssertions(ctx context.Context, assertionsList types.List) ([]ICMPCheckAssertionModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	var assertions []ICMPCheckAssertionModel

	if !assertionsList.IsNull() && !assertionsList.IsUnknown() {
		diags.Append(assertionsList.ElementsAs(ctx, &assertions, false)...)
		if diags.HasError() {
			return nil, diags
		}
	}
	return assertions, diags
}

func (r *ICMPCheckResource) fetchNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
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

func (r *ICMPCheckResource) resolveEmailsToChannelIDs(ctx context.Context, emails []string) ([]string, error) {
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

func (r *ICMPCheckResource) resolveChannelIDsToEmails(ctx context.Context, channelIDs []string) ([]string, []string, error) {
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

func normalizeICMPOperator(op string) string {
	cleanOp := strings.ToLower(strings.ReplaceAll(op, "_", ""))
	switch cleanOp {
	case "is":
		return "is"
	case "lessthan", "islessthan":
		return "lessThan"
	case "lessthanorequal", "lessthanorequals", "islessthanorequal", "islessthanorequals":
		return "lessThanOrEqual"
	case "morethan", "ismorethan", "greaterthan", "isgreaterthan":
		return "moreThan"
	case "morethanorequal", "morethanorequals", "ismorethanorequal", "greaterthanorequal", "isgreaterthanorequal":
		return "moreThanOrEqual"
	default:
		return op
	}
}

func normalizeICMPAssertions(input []ICMPCheckAssertionModel) []map[string]string {
	if len(input) == 0 {
		return []map[string]string{
			{
				"type":     "latency",
				"property": "avg",
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
		unit := ""

		cleanType := strings.ToLower(strings.ReplaceAll(aType, "_", ""))
		normalizedOp := normalizeICMPOperator(op)

		switch cleanType {
		case "latency":
			aType = "latency"
			cleanProp := strings.ToLower(strings.TrimSpace(prop))
			if cleanProp == "" || cleanProp == "avg" || cleanProp == "average" {
				prop = "avg"
			} else if cleanProp == "min" || cleanProp == "minimum" {
				prop = "min"
			} else if cleanProp == "max" || cleanProp == "maximum" {
				prop = "max"
			} else if cleanProp == "stddev" || cleanProp == "standarddeviation" || cleanProp == "std_dev" {
				prop = "stddev"
			}
			unit = "ms"

		case "packetloss", "loss":
			aType = "packetLoss"
			prop = ""
			unit = "%"

		case "packetsreceived", "packetsrecived", "received", "packets":
			aType = "packetsRecived"
			prop = ""
			unit = ""
		}

		result = append(result, map[string]string{
			"type":     aType,
			"property": prop,
			"operator": normalizedOp,
			"target":   target,
			"unit":     unit,
		})
	}
	return result
}

func (r *ICMPCheckResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	if r.client == nil || r.apiKey == "" {
		return
	}

	var plan ICMPCheckModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.Frequency.IsNull() && !plan.Frequency.IsUnknown() {
		_, canonicalStr, err := parseFrequency(plan.Frequency.ValueString(), 60)
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

	var config ICMPCheckModel
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
			var updatedAssertions []ICMPCheckAssertionModel
			for _, a := range assertions {
				if a.Type.IsNull() || a.Type.IsUnknown() {
					continue
				}
				aType := a.Type.ValueString()
				op := a.Operator.ValueString()
				target := a.Target.ValueString()
				prop := a.Property.ValueString()

				cleanType := strings.ToLower(strings.ReplaceAll(aType, "_", ""))
				normalizedOp := normalizeICMPOperator(op)

				switch cleanType {
				case "latency":
					aType = "latency"
					cleanProp := strings.ToLower(strings.TrimSpace(prop))
					if cleanProp == "" || cleanProp == "avg" || cleanProp == "average" {
						prop = "avg"
					} else if cleanProp == "min" || cleanProp == "minimum" {
						prop = "min"
					} else if cleanProp == "max" || cleanProp == "maximum" {
						prop = "max"
					} else if cleanProp == "stddev" || cleanProp == "standarddeviation" || cleanProp == "std_dev" {
						prop = "stddev"
					}

				case "packetloss", "loss":
					aType = "packetLoss"
					prop = ""

				case "packetsreceived", "packetsrecived", "received", "packets":
					if cleanType == "packetsrecived" {
						aType = "packetsRecived"
					} else {
						aType = "packetsReceived"
					}
					prop = ""

					if target != "" {
						if parsedTarget, err := strconv.ParseInt(strings.TrimSpace(target), 10, 64); err == nil {
							if parsedTarget > 10 || parsedTarget < 0 {
								resp.Diagnostics.AddError(
									"Invalid Assertion Target for Packets Received",
									fmt.Sprintf("Target value '%s' for '%s' assertion is invalid. Atatus ICMP checks only send 1 to 10 packets, so received packets cannot be greater than 10.", target, aType),
								)
							} else if !plan.Packets.IsNull() && !plan.Packets.IsUnknown() && parsedTarget > plan.Packets.ValueInt64() {
								resp.Diagnostics.AddError(
									"Invalid Assertion Target for Packets Received",
									fmt.Sprintf("Target value '%s' for '%s' assertion cannot be greater than the configured 'packets' count (%d).", target, aType, plan.Packets.ValueInt64()),
								)
							}
						}
					}
				}

				updatedAssertions = append(updatedAssertions, ICMPCheckAssertionModel{
					Type:     types.StringValue(aType),
					Property: types.StringValue(prop),
					Operator: types.StringValue(normalizedOp),
					Target:   types.StringValue(target),
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
			defaultAssertions := []ICMPCheckAssertionModel{
				{
					Type:     types.StringValue("latency"),
					Property: types.StringValue("avg"),
					Operator: types.StringValue("lessThan"),
					Target:   types.StringValue("1000"),
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

func (r *ICMPCheckResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ICMPCheckModel
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

	packets := int64(10)
	if !plan.Packets.IsNull() && !plan.Packets.IsUnknown() && plan.Packets.ValueInt64() > 0 {
		packets = plan.Packets.ValueInt64()
	}

	payloadAssertions := normalizeICMPAssertions(assertions)

	host := cleanHost(plan.Host.ValueString())

	freqSec, _, _ := parseFrequency(plan.Frequency.ValueString(), 60)

	runType, _ := normalizeRunType(plan.RunType.ValueString())

	var minLocFailedPtr *int64
	if runType != "sequential" {
		minLocFailedPtr = &minLocationFailed
	}

	payload := ICMPCheckPayload{
		Name:              plan.Name.ValueString(),
		Type:              "api",
		Subtype:           "icmp",
		Host:              host,
		IcmpSendPackets:   strconv.FormatInt(packets, 10),
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

	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		resp.Diagnostics.AddError("Response Read Error", err.Error())
		return
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		resp.Diagnostics.AddError("API Failure", fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, string(respBytes)))
		return
	}

	var atatusResp ICMPCheckResponse
	if err := json.Unmarshal(respBytes, &atatusResp); err != nil {
		resp.Diagnostics.AddError("JSON Decoding Error", fmt.Sprintf("Error decoding response: %s. Raw: %s", err.Error(), string(respBytes)))
		return
	}

	checkID := atatusResp.ID
	if checkID == "" {
		checkID = atatusResp.IDAlt
	}
	plan.ID = types.StringValue(checkID)
	plan.Packets = types.Int64Value(packets)
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

func (r *ICMPCheckResource) buildAssertionList(ctx context.Context, assertions []ICMPCheckAssertionModel) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	if len(assertions) == 0 {
		return types.ListNull(icmpAssertionElementType), diags
	}

	var elements []attr.Value
	for _, a := range assertions {
		objVal, objDiags := types.ObjectValue(
			map[string]attr.Type{
				"type":     types.StringType,
				"operator": types.StringType,
				"target":   types.StringType,
				"property": types.StringType,
			},
			map[string]attr.Value{
				"type":     a.Type,
				"operator": a.Operator,
				"target":   a.Target,
				"property": a.Property,
			},
		)
		diags.Append(objDiags...)
		elements = append(elements, objVal)
	}

	listVal, listDiags := types.ListValue(icmpAssertionElementType, elements)
	diags.Append(listDiags...)
	return listVal, diags
}

func (r *ICMPCheckResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ICMPCheckModel
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
		resp.Diagnostics.AddError("API Read Failure", fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, string(respBytes)))
		return
	}

	var atatusResp ICMPCheckResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&atatusResp); err != nil {
		resp.Diagnostics.AddError("JSON Decode Error", err.Error())
		return
	}

	state.Name = types.StringValue(atatusResp.Name)
	state.Host = types.StringValue(atatusResp.Host)
	state.Frequency = types.StringValue(secondsToFrequencyString(atatusResp.Frequency))
	state.RetryCount = types.Int64Value(atatusResp.RetryCount)

	runType := "parallel"
	if atatusResp.RunType != "" {
		runType, _ = normalizeRunType(atatusResp.RunType)
		state.RunType = types.StringValue(runType)
	} else if state.RunType.IsNull() || state.RunType.IsUnknown() || state.RunType.ValueString() == "" {
		state.RunType = types.StringValue("parallel")
	}

	if runType == "sequential" {
		state.MinLocationFailed = types.Int64Null()
	} else {
		minLoc := atatusResp.MinLocationFailed
		if minLoc <= 0 {
			minLoc = 1
		}
		state.MinLocationFailed = types.Int64Value(minLoc)
	}

	if atatusResp.IcmpSendPackets != "" {
		if pVal, err := strconv.ParseInt(atatusResp.IcmpSendPackets, 10, 64); err == nil {
			state.Packets = types.Int64Value(pVal)
		}
	}

	locList, listDiags := types.ListValueFrom(ctx, types.StringType, atatusResp.Locations)
	resp.Diagnostics.Append(listDiags...)
	state.Locations = locList

	if len(atatusResp.Tags) > 0 {
		tagsList, tagListDiags := types.ListValueFrom(ctx, types.StringType, atatusResp.Tags)
		resp.Diagnostics.Append(tagListDiags...)
		state.Tags = tagsList
	} else {
		state.Tags = types.ListNull(types.StringType)
	}

	emails, nonEmailChannels, err := r.resolveChannelIDsToEmails(ctx, atatusResp.ChannelIDs)
	if err == nil {
		if len(emails) > 0 {
			emList, listDiags := types.ListValueFrom(ctx, types.StringType, emails)
			resp.Diagnostics.Append(listDiags...)
			state.Emails = emList
		} else {
			state.Emails = types.ListNull(types.StringType)
		}

		if len(nonEmailChannels) > 0 {
			chList, listDiags := types.ListValueFrom(ctx, types.StringType, nonEmailChannels)
			resp.Diagnostics.Append(listDiags...)
			state.ChannelIDs = chList
		} else {
			state.ChannelIDs = types.ListNull(types.StringType)
		}
	}

	if len(atatusResp.Assertions) > 0 {
		var assertionModels []ICMPCheckAssertionModel
		for _, a := range atatusResp.Assertions {
			aType := a["type"]
			cleanType := strings.ToLower(strings.ReplaceAll(aType, "_", ""))
			op := normalizeICMPOperator(a["operator"])
			prop := a["property"]

			switch cleanType {
			case "latency":
				aType = "latency"
				cleanProp := strings.ToLower(strings.TrimSpace(prop))
				if cleanProp == "" || cleanProp == "avg" || cleanProp == "average" {
					prop = "avg"
				} else if cleanProp == "min" || cleanProp == "minimum" {
					prop = "min"
				} else if cleanProp == "max" || cleanProp == "maximum" {
					prop = "max"
				} else if cleanProp == "stddev" || cleanProp == "standarddeviation" || cleanProp == "std_dev" {
					prop = "stddev"
				}

			case "packetloss", "loss":
				aType = "packetLoss"
				prop = ""

			case "packetsreceived", "packetsrecived", "received", "packets":
				aType = "packetsReceived"
				prop = ""
			}

			assertionModels = append(assertionModels, ICMPCheckAssertionModel{
				Type:     types.StringValue(aType),
				Operator: types.StringValue(op),
				Target:   types.StringValue(a["target"]),
				Property: types.StringValue(prop),
			})
		}
		aList, aBuildDiags := r.buildAssertionList(ctx, assertionModels)
		resp.Diagnostics.Append(aBuildDiags...)
		if !resp.Diagnostics.HasError() {
			state.Assertions = aList
		}
	} else {
		state.Assertions = types.ListNull(icmpAssertionElementType)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ICMPCheckResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ICMPCheckModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state ICMPCheckModel
	stateDiags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(stateDiags...)
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

	packets := int64(10)
	if !plan.Packets.IsNull() && !plan.Packets.IsUnknown() && plan.Packets.ValueInt64() > 0 {
		packets = plan.Packets.ValueInt64()
	}

	payloadAssertions := normalizeICMPAssertions(assertions)

	host := cleanHost(plan.Host.ValueString())

	freqSec, _, _ := parseFrequency(plan.Frequency.ValueString(), 60)

	runType, _ := normalizeRunType(plan.RunType.ValueString())

	payload := map[string]interface{}{
		"name":            plan.Name.ValueString(),
		"host":            host,
		"icmpSendPackets": strconv.FormatInt(packets, 10),
		"frequency":       freqSec,
		"retryCount":      retryCount,
		"channelIds":      allAPIChannelIDs,
		"runType":         runType,
		"locations":       locations,
		"tags":            tags,
		"assertions":      payloadAssertions,
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

	plan.Packets = types.Int64Value(packets)
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

func (r *ICMPCheckResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ICMPCheckModel
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
