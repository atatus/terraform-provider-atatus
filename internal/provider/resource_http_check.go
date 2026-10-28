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
	_ resource.Resource               = &HTTPCheckResource{}
	_ resource.ResourceWithConfigure  = &HTTPCheckResource{}
	_ resource.ResourceWithModifyPlan = &HTTPCheckResource{}

	assertionAttrTypes = map[string]attr.Type{
		"type":                     types.StringType,
		"operator":                 types.StringType,
		"target":                   types.StringType,
		"property":                 types.StringType,
		"unit":                     types.StringType,
		"target_property_operator": types.StringType,
	}
	assertionElementType = types.ObjectType{AttrTypes: assertionAttrTypes}
)

func NewHTTPCheckResource() resource.Resource {
	return &HTTPCheckResource{}
}

type HTTPCheckResource struct {
	client    *http.Client
	apiKey    string
	accountID string
	authType  string
}

type NotificationChannelSettings struct {
	Recipient string `json:"recipient,omitempty"`
	URL       string `json:"url,omitempty"`
}

type NotificationChannel struct {
	ID        string                      `json:"_id"`
	Name      string                      `json:"name"`
	Type      string                      `json:"type"`
	AccountID string                      `json:"accountId"`
	Settings  NotificationChannelSettings `json:"settings"`
}

type CheckAssertionModel struct {
	Type                   types.String `tfsdk:"type"`
	Operator               types.String `tfsdk:"operator"`
	Target                 types.String `tfsdk:"target"`
	Property               types.String `tfsdk:"property"`
	Unit                   types.String `tfsdk:"unit"`
	TargetPropertyOperator types.String `tfsdk:"target_property_operator"`
}

type HTTPCheckModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Method            types.String `tfsdk:"method"`
	URL               types.String `tfsdk:"url"`
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

type HTTPCheckPayload struct {
	Name              string                   `json:"name"`
	Type              string                   `json:"type"`
	Subtype           string                   `json:"subtype"`
	Host              string                   `json:"host"`
	Frequency         int64                    `json:"frequency"`
	MinLocationFailed *int64                   `json:"minLocationFailed,omitempty"`
	RetryCount        int64                    `json:"retryCount"`
	ChannelIDs        []string                 `json:"channelIds"`
	RunType           string                   `json:"runType"`
	Locations         []string                 `json:"locations"`
	Tags              []string                 `json:"tags"`
	Assertions        []map[string]interface{} `json:"assertions"`
	Request           map[string]interface{}   `json:"request"`
}

type HTTPCheckReqData struct {
	Method string `json:"method"`
	URL    string `json:"url"`
}

type HTTPCheckResponse struct {
	ID                string                   `json:"_id"`
	IDAlt             string                   `json:"id"`
	Name              string                   `json:"name"`
	Host              string                   `json:"host"`
	Subtype           string                   `json:"subtype"`
	Frequency         int64                    `json:"frequency"`
	MinLocationFailed int64                    `json:"minLocationFailed"`
	RetryCount        int64                    `json:"retryCount"`
	ChannelIDs        []string                 `json:"channelIds"`
	RunType           string                   `json:"runType"`
	Locations         []string                 `json:"locations"`
	Tags              []string                 `json:"tags"`
	Assertions        []map[string]interface{} `json:"assertions"`
	AccountID         string                   `json:"accountId"`
	Request           HTTPCheckReqData         `json:"request"`
}

func (r *HTTPCheckResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_http_check"
}

func (r *HTTPCheckResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Atatus Synthetic HTTP/API Check.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Identifier for the HTTP synthetic check.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the synthetic HTTP check.",
				Required:    true,
			},
			"method": schema.StringAttribute{
				Description: "HTTP request method: GET, POST, PUT, DELETE, PATCH, etc. (default: GET).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("GET"),
			},
			"url": schema.StringAttribute{
				Description: "The URL endpoint to monitor.",
				Required:    true,
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
				Description: "List of email addresses to alert directly when a check fails (auto-mapped to existing Atatus email notification channels).",
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
				Description: "List of assertion rules to validate against the response (defaults to statusCode is 200).",
				Optional:    true,
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Description: "Assertion type: statusCode, responseTime, header, body, bodyHash.",
							Required:    true,
						},
						"operator": schema.StringAttribute{
							Description: "Comparison operator: is, isNot, lessThan, contains, doesNotContain, matchesRegex, doesNotMatchesRegex, jsonpath, xpath, md5, sha1, sha256.",
							Required:    true,
						},
						"target": schema.StringAttribute{
							Description: "Expected target value (e.g. '200' for statusCode, '1000' for responseTime).",
							Optional:    true,
							Computed:    true,
							Default:     stringdefault.StaticString(""),
						},
						"property": schema.StringAttribute{
							Description: "Header name (for header type, e.g. 'Content-Type') or JSONPath/XPath expression (for body type, e.g. '$.price').",
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
						"target_property_operator": schema.StringAttribute{
							Description: "Comparison operator for JSONPath or XPath property (e.g. 'moreThan', 'lessThan', 'is', 'isNot', 'contains', 'doesNotContain', 'matchesRegex', 'doesNotMatchesRegex').",
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

func (r *HTTPCheckResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *HTTPCheckResource) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "terraform-provider-atatus")
	if r.authType == "cookie" {
		req.Header.Set("Cookie", r.apiKey)
	} else {
		req.Header.Set("X-API-KEY", r.apiKey)
	}
}

func (r *HTTPCheckResource) getLocations(ctx context.Context, locationsList types.List) ([]string, diag.Diagnostics) {
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

func (r *HTTPCheckResource) getTags(ctx context.Context, tagsList types.List) ([]string, diag.Diagnostics) {
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

func (r *HTTPCheckResource) getChannelIDs(ctx context.Context, channelIDsList types.List) ([]string, diag.Diagnostics) {
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

func (r *HTTPCheckResource) getEmails(ctx context.Context, emailsList types.List) ([]string, diag.Diagnostics) {
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

func (r *HTTPCheckResource) getAssertions(ctx context.Context, list types.List) ([]CheckAssertionModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	var assertions []CheckAssertionModel
	if list.IsNull() || list.IsUnknown() {
		return assertions, diags
	}
	diags.Append(list.ElementsAs(ctx, &assertions, false)...)
	return assertions, diags
}

func (r *HTTPCheckResource) buildAssertionList(ctx context.Context, assertions []CheckAssertionModel) (types.List, diag.Diagnostics) {
	return types.ListValueFrom(ctx, assertionElementType, assertions)
}

func (r *HTTPCheckResource) fetchNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
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

func (r *HTTPCheckResource) resolveEmailsToChannelIDs(ctx context.Context, emails []string) ([]string, error) {
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

func (r *HTTPCheckResource) resolveChannelIDsToEmails(ctx context.Context, channelIDs []string) ([]string, []string, error) {
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

func mapToAssertionModel(a map[string]interface{}) CheckAssertionModel {
	aType := fmt.Sprintf("%v", a["type"])
	op := fmt.Sprintf("%v", a["operator"])
	target := ""
	if a["target"] != nil {
		target = fmt.Sprintf("%v", a["target"])
	}
	prop := ""
	if a["property"] != nil {
		prop = fmt.Sprintf("%v", a["property"])
	}
	unit := ""
	if a["unit"] != nil {
		unit = fmt.Sprintf("%v", a["unit"])
	}
	targetPropOp := ""
	if tpRaw, ok := a["targetProperty"]; ok && tpRaw != nil {
		if tpMap, ok := tpRaw.(map[string]interface{}); ok {
			if k, ok := tpMap["key"]; ok && k != nil {
				prop = fmt.Sprintf("%v", k)
			}
			if o, ok := tpMap["operator"]; ok && o != nil {
				targetPropOp = fmt.Sprintf("%v", o)
			}
		} else if tpMap, ok := tpRaw.(map[string]string); ok {
			if k, ok := tpMap["key"]; ok {
				prop = k
			}
			if o, ok := tpMap["operator"]; ok {
				targetPropOp = o
			}
		}
	}
	return CheckAssertionModel{
		Type:                   types.StringValue(aType),
		Operator:               types.StringValue(op),
		Target:                 types.StringValue(target),
		Property:               types.StringValue(prop),
		Unit:                   types.StringValue(unit),
		TargetPropertyOperator: types.StringValue(targetPropOp),
	}
}

func normalizeHTTPAssertions(input []CheckAssertionModel) []map[string]interface{} {
	if len(input) == 0 {
		return []map[string]interface{}{
			{
				"type":     "statusCode",
				"property": "",
				"operator": "is",
				"target":   "200",
				"unit":     "",
			},
		}
	}

	var result []map[string]interface{}
	for _, a := range input {
		aType := a.Type.ValueString()
		op := a.Operator.ValueString()
		target := a.Target.ValueString()
		prop := a.Property.ValueString()
		unit := a.Unit.ValueString()
		targetPropOp := a.TargetPropertyOperator.ValueString()

		item := map[string]interface{}{
			"type":     aType,
			"property": prop,
			"operator": op,
			"target":   target,
			"unit":     unit,
		}

		switch aType {
		case "statusCode":
			if target == "" {
				target = "200"
			}
			if op == "" || op == "is" {
				op = "is"
			} else if op == "isNot" {
				op = "isNot"
			}
			item["property"] = ""
			item["unit"] = ""
			item["operator"] = op
			item["target"] = target
		case "responseTime":
			if target == "" {
				target = "1000"
			}
			if op == "" || op == "lessThan" {
				op = "lessThan"
			}
			item["property"] = ""
			item["unit"] = "ms"
			item["operator"] = op
			item["target"] = target
		case "header":
			item["property"] = prop
			item["unit"] = ""
			item["operator"] = op
			item["target"] = target
		case "body":
			item["unit"] = ""
			if op == "jsonpath" || op == "xpath" {
				item["property"] = ""
				if targetPropOp == "" {
					targetPropOp = "is"
				}
				item["targetProperty"] = map[string]string{
					"key":      prop,
					"operator": targetPropOp,
				}
			} else {
				item["property"] = ""
			}
		case "bodyHash", "bodyhash":
			item["property"] = ""
			item["unit"] = ""
		}

		result = append(result, item)
	}
	return result
}

func (r *HTTPCheckResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	if r.client == nil || r.apiKey == "" {
		return
	}

	var plan HTTPCheckModel
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

	var config HTTPCheckModel
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
			var updatedAssertions []CheckAssertionModel
			for _, a := range assertions {
				if a.Type.IsNull() || a.Type.IsUnknown() {
					continue
				}
				aType := a.Type.ValueString()
				op := a.Operator.ValueString()
				target := a.Target.ValueString()
				prop := a.Property.ValueString()
				unit := a.Unit.ValueString()
				targetPropOp := a.TargetPropertyOperator.ValueString()

				switch strings.ToLower(aType) {
				case "statuscode":
					if op != "is" && op != "isNot" {
						resp.Diagnostics.AddError("Invalid Assertion Operator", fmt.Sprintf("Unsupported operator '%s' for assertion type 'statusCode'. Supported operators: is, isNot", op))
					}
					if target == "" {
						target = "200"
					}
					if op == "" {
						op = "is"
					}
					prop = ""
					unit = ""
					targetPropOp = ""
				case "responsetime":
					if op != "lessThan" {
						resp.Diagnostics.AddError("Invalid Assertion Operator", fmt.Sprintf("Unsupported operator '%s' for assertion type 'responseTime'. Supported operator: lessThan", op))
					}
					if target == "" {
						target = "1000"
					}
					if op == "" {
						op = "lessThan"
					}
					prop = ""
					unit = "ms"
					targetPropOp = ""
				case "header":
					if op != "is" && op != "isNot" &&
						op != "contains" && op != "doesNotContain" &&
						op != "matchesRegex" && op != "doesNotMatchesRegex" {
						resp.Diagnostics.AddError("Invalid Assertion Operator", fmt.Sprintf("Unsupported operator '%s' for assertion type 'header'. Supported operators: is, isNot, contains, doesNotContain, matchesRegex, doesNotMatchesRegex", op))
					}
					if a.Property.IsNull() || a.Property.ValueString() == "" {
						resp.Diagnostics.AddError("Missing Header Name", "Assertion type 'header' requires 'property' (header name, e.g. 'Content-Type') to be specified.")
					}
					unit = ""
					targetPropOp = ""
				case "body":
					if op == "jsonpath" || op == "xpath" {
						if a.Property.IsNull() || a.Property.ValueString() == "" {
							resp.Diagnostics.AddError("Missing Body Property", "Assertion type 'body' with operator 'jsonpath' or 'xpath' requires 'property' (JSONPath or XPath expression, e.g. '$.price') to be specified.")
						}
						if targetPropOp == "" {
							targetPropOp = "is"
						} else if targetPropOp != "is" && targetPropOp != "isNot" &&
							targetPropOp != "contains" && targetPropOp != "doesNotContain" &&
							targetPropOp != "matchesRegex" && targetPropOp != "doesNotMatchesRegex" &&
							targetPropOp != "moreThan" && targetPropOp != "lessThan" {
							resp.Diagnostics.AddError("Invalid Target Property Operator", fmt.Sprintf("Unsupported target_property_operator '%s' for body assertion. Supported operators: is, isNot, contains, doesNotContain, matchesRegex, doesNotMatchesRegex, moreThan, lessThan", targetPropOp))
						}
					} else if op == "is" || op == "isNot" || op == "contains" || op == "doesNotContain" || op == "matchesRegex" || op == "doesNotMatchesRegex" {
						prop = ""
						targetPropOp = ""
					} else {
						resp.Diagnostics.AddError("Invalid Assertion Operator", fmt.Sprintf("Unsupported operator '%s' for assertion type 'body'. Supported operators: jsonpath, xpath, is, isNot, contains, doesNotContain, matchesRegex, doesNotMatchesRegex", op))
					}
					unit = ""
				case "bodyhash":
					if strings.ToLower(op) != "md5" && strings.ToLower(op) != "sha1" && strings.ToLower(op) != "sha256" {
						resp.Diagnostics.AddError("Invalid Hash Operator", fmt.Sprintf("Unsupported operator '%s' for assertion type 'bodyHash'. Supported operators: md5, sha1, sha256", op))
					}
					prop = ""
					unit = ""
					targetPropOp = ""
				default:
					resp.Diagnostics.AddError("Invalid Assertion Type", fmt.Sprintf("Unsupported assertion type '%s'. Supported types: statusCode, responseTime, header, body, bodyHash", aType))
				}

				updatedAssertions = append(updatedAssertions, CheckAssertionModel{
					Type:                   a.Type,
					Operator:               a.Operator,
					Target:                 types.StringValue(target),
					Property:               types.StringValue(prop),
					Unit:                   types.StringValue(unit),
					TargetPropertyOperator: types.StringValue(targetPropOp),
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
			defaultAssertion := []CheckAssertionModel{
				{
					Type:                   types.StringValue("statusCode"),
					Operator:               types.StringValue("is"),
					Target:                 types.StringValue("200"),
					Property:               types.StringValue(""),
					Unit:                   types.StringValue(""),
					TargetPropertyOperator: types.StringValue(""),
				},
			}
			aList, aBuildDiags := r.buildAssertionList(ctx, defaultAssertion)
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

func (r *HTTPCheckResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HTTPCheckModel
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

	payloadAssertions := normalizeHTTPAssertions(assertions)
	freqSec, _, _ := parseFrequency(plan.Frequency.ValueString(), 300)

	runType, _ := normalizeRunType(plan.RunType.ValueString())

	var minLocFailedPtr *int64
	if runType != "sequential" {
		minLocFailedPtr = &minLocationFailed
	}

	payload := HTTPCheckPayload{
		Name:              plan.Name.ValueString(),
		Type:              "api",
		Subtype:           "http",
		Host:              plan.URL.ValueString(),
		Frequency:         freqSec,
		MinLocationFailed: minLocFailedPtr,
		RetryCount:        retryCount,
		ChannelIDs:        allAPIChannelIDs,
		RunType:           runType,
		Locations:         locations,
		Tags:              tags,
		Assertions:        payloadAssertions,
		Request: map[string]interface{}{
			"method":                  plan.Method.ValueString(),
			"url":                     plan.URL.ValueString(),
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

	var result HTTPCheckResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&result); err != nil {
		resp.Diagnostics.AddError("JSON Decode Error", err.Error())
		return
	}

	checkID := result.ID
	if checkID == "" {
		checkID = result.IDAlt
	}
	plan.ID = types.StringValue(checkID)
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

	var finalAssertionModels []CheckAssertionModel
	for _, a := range payloadAssertions {
		finalAssertionModels = append(finalAssertionModels, mapToAssertionModel(a))
	}
	aList, aBuildDiags := r.buildAssertionList(ctx, finalAssertionModels)
	resp.Diagnostics.Append(aBuildDiags...)
	plan.Assertions = aList

	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

func (r *HTTPCheckResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HTTPCheckModel
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

	var result HTTPCheckResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&result); err != nil {
		resp.Diagnostics.AddError("JSON Decode Error", err.Error())
		return
	}

	state.Name = types.StringValue(result.Name)
	state.URL = types.StringValue(result.Host)
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

	if result.Request.Method != "" {
		state.Method = types.StringValue(result.Request.Method)
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
		var prevTargetPropOpMap = make(map[int]string)
		for idx, pa := range prevAssertions {
			if !pa.Type.IsNull() && !pa.Type.IsUnknown() {
				prevTypeMap[idx] = pa.Type.ValueString()
			}
			if !pa.Operator.IsNull() && !pa.Operator.IsUnknown() {
				prevOpMap[idx] = pa.Operator.ValueString()
			}
			if !pa.TargetPropertyOperator.IsNull() && !pa.TargetPropertyOperator.IsUnknown() {
				prevTargetPropOpMap[idx] = pa.TargetPropertyOperator.ValueString()
			}
		}

		var assertionModels []CheckAssertionModel
		for idx, a := range result.Assertions {
			model := mapToAssertionModel(a)
			finalType := model.Type.ValueString()
			if prevT, ok := prevTypeMap[idx]; ok && strings.EqualFold(strings.ReplaceAll(prevT, "_", ""), strings.ReplaceAll(finalType, "_", "")) {
				finalType = prevT
			}

			finalOp := model.Operator.ValueString()
			if prevO, ok := prevOpMap[idx]; ok && strings.EqualFold(strings.ReplaceAll(prevO, "_", ""), strings.ReplaceAll(finalOp, "_", "")) {
				finalOp = prevO
			}

			finalTargetPropOp := model.TargetPropertyOperator.ValueString()
			if prevTPO, ok := prevTargetPropOpMap[idx]; ok && strings.EqualFold(strings.ReplaceAll(prevTPO, "_", ""), strings.ReplaceAll(finalTargetPropOp, "_", "")) {
				finalTargetPropOp = prevTPO
			}

			assertionModels = append(assertionModels, CheckAssertionModel{
				Type:                   types.StringValue(finalType),
				Operator:               types.StringValue(finalOp),
				Target:                 model.Target,
				Property:               model.Property,
				Unit:                   model.Unit,
				TargetPropertyOperator: types.StringValue(finalTargetPropOp),
			})
		}
		aList, aDiags := r.buildAssertionList(ctx, assertionModels)
		resp.Diagnostics.Append(aDiags...)
		if !resp.Diagnostics.HasError() {
			state.Assertions = aList
		}
	} else {
		defaultAssertion := []CheckAssertionModel{
			{
				Type:                   types.StringValue("statusCode"),
				Operator:               types.StringValue("is"),
				Target:                 types.StringValue("200"),
				Property:               types.StringValue(""),
				Unit:                   types.StringValue(""),
				TargetPropertyOperator: types.StringValue(""),
			},
		}
		aList, aDiags := r.buildAssertionList(ctx, defaultAssertion)
		resp.Diagnostics.Append(aDiags...)
		if !resp.Diagnostics.HasError() {
			state.Assertions = aList
		}
	}

	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
}

func (r *HTTPCheckResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HTTPCheckModel
	var state HTTPCheckModel

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

	payloadAssertions := normalizeHTTPAssertions(assertions)
	freqSec, _, _ := parseFrequency(plan.Frequency.ValueString(), 300)

	runType, _ := normalizeRunType(plan.RunType.ValueString())

	payload := map[string]interface{}{
		"name":              plan.Name.ValueString(),
		"host":              plan.URL.ValueString(),
		"frequency":         freqSec,
		"retryCount":        retryCount,
		"channelIds":        allAPIChannelIDs,
		"runType":           runType,
		"locations":         locations,
		"tags":              tags,
		"assertions":        payloadAssertions,
		"request": map[string]interface{}{
			"method":                  plan.Method.ValueString(),
			"url":                     plan.URL.ValueString(),
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

	var finalAssertionModels []CheckAssertionModel
	for _, a := range payloadAssertions {
		finalAssertionModels = append(finalAssertionModels, mapToAssertionModel(a))
	}
	aList, aBuildDiags := r.buildAssertionList(ctx, finalAssertionModels)
	resp.Diagnostics.Append(aBuildDiags...)
	plan.Assertions = aList

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *HTTPCheckResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HTTPCheckModel
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
