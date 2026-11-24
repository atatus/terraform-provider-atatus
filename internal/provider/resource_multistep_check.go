package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &MultistepCheckResource{}
	_ resource.ResourceWithConfigure   = &MultistepCheckResource{}
	_ resource.ResourceWithModifyPlan  = &MultistepCheckResource{}
	_ resource.ResourceWithImportState = &MultistepCheckResource{}
)

func NewMultistepCheckResource() resource.Resource {
	return &MultistepCheckResource{}
}

type MultistepCheckResource struct {
	syntheticBase
}

type APIStepAssertionModel struct {
	Type                   types.String `tfsdk:"type"`
	Operator               types.String `tfsdk:"operator"`
	Target                 types.String `tfsdk:"target"`
	Property               types.String `tfsdk:"property"`
	Unit                   types.String `tfsdk:"unit"`
	TargetPropertyOperator types.String `tfsdk:"target_property_operator"`
}

type APIStepModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	URL                types.String `tfsdk:"url"`
	Method             types.String `tfsdk:"method"`
	AllowFailure       types.Bool   `tfsdk:"allow_failure"`
	ConsiderTestFailed types.Bool   `tfsdk:"consider_test_failed"`
	RetryCount         types.Int64  `tfsdk:"retry_count"`
	MinLocationFailed  types.Int64  `tfsdk:"min_location_failed"`
	Assertions         types.List   `tfsdk:"assertion"`
}

type MultistepCheckModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Frequency         types.String `tfsdk:"frequency"`
	Locations         types.List   `tfsdk:"locations"`
	Tags              types.List   `tfsdk:"tags"`
	RetryCount        types.Int64  `tfsdk:"retry_count"`
	MinLocationFailed types.Int64  `tfsdk:"min_location_failed"`
	RunType           types.String `tfsdk:"run_type"`
	ChannelIDs        types.List   `tfsdk:"channel_ids"`
	Emails            types.List   `tfsdk:"emails"`
	Steps             types.List   `tfsdk:"step"`
}

type APIStepPayload struct {
	ID                 string                   `json:"_id,omitempty"`
	Name               string                   `json:"name"`
	Type               string                   `json:"type"`
	Subtype            string                   `json:"subtype"`
	AllowFailure       bool                     `json:"allowFailure"`
	ConsiderTestFailed bool                     `json:"considerTestFailed"`
	RetryCount         int64                    `json:"retryCount"`
	MinLocationFailed  int64                    `json:"minLocationFailed"`
	ExtractedValues    []interface{}            `json:"extractedValues"`
	Assertions         []map[string]interface{} `json:"assertions"`
	Request            map[string]interface{}   `json:"request"`
}

type MultistepCheckPayload struct {
	Name              string                   `json:"name"`
	Type              string                   `json:"type"`
	Subtype           string                   `json:"subtype"`
	Frequency         int64                    `json:"frequency"`
	MinLocationFailed *int64                   `json:"minLocationFailed,omitempty"`
	RetryCount        int64                    `json:"retryCount"`
	ChannelIDs        []string                 `json:"channelIds"`
	RunType           string                   `json:"runType"`
	Locations         []string                 `json:"locations"`
	Tags              []string                 `json:"tags"`
	Assertions        []map[string]interface{} `json:"assertions"`
	APISteps          []APIStepPayload         `json:"apiSteps"`
	Request           map[string]interface{}   `json:"request"`
}

type APIStepResponse struct {
	ID                 string                   `json:"_id"`
	Name               string                   `json:"name"`
	Type               string                   `json:"type"`
	Subtype            string                   `json:"subtype"`
	AllowFailure       bool                     `json:"allowFailure"`
	ConsiderTestFailed bool                     `json:"considerTestFailed"`
	RetryCount         int64                    `json:"retryCount"`
	MinLocationFailed  int64                    `json:"minLocationFailed"`
	Assertions         []map[string]interface{} `json:"assertions"`
	Request            HTTPCheckReqData         `json:"request"`
}

type MultistepCheckResponse struct {
	ID                string            `json:"_id"`
	IDAlt             string            `json:"id"`
	Name              string            `json:"name"`
	Subtype           string            `json:"subtype"`
	Frequency         int64             `json:"frequency"`
	MinLocationFailed int64             `json:"minLocationFailed"`
	RetryCount        int64             `json:"retryCount"`
	ChannelIDs        []string          `json:"channelIds"`
	RunType           string            `json:"runType"`
	Locations         []string          `json:"locations"`
	Tags              []string          `json:"tags"`
	APISteps          []APIStepResponse `json:"apiSteps"`
	AccountID         string            `json:"accountId"`
}

var stepAssertionElementType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"type":                     types.StringType,
		"operator":                 types.StringType,
		"target":                   types.StringType,
		"property":                 types.StringType,
		"unit":                     types.StringType,
		"target_property_operator": types.StringType,
	},
}

var apiStepElementType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"id":                   types.StringType,
		"name":                 types.StringType,
		"url":                  types.StringType,
		"method":               types.StringType,
		"allow_failure":        types.BoolType,
		"consider_test_failed": types.BoolType,
		"retry_count":          types.Int64Type,
		"min_location_failed":  types.Int64Type,
		"assertion":            types.ListType{ElemType: stepAssertionElementType},
	},
}

func (r *MultistepCheckResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_multistep_check"
}

func (r *MultistepCheckResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Atatus Synthetic Multistep API Check.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Identifier for the synthetic multistep check.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the synthetic multistep check.",
				Required:    true,
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
				Validators:  []validator.Int64{int64validator.Between(0, 3)},
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
				Default:     defaultLocations,
			},
			"tags": schema.ListAttribute{
				Description: "List of tags (e.g., [\"env:production\"]).",
				Optional:    true,
				ElementType: types.StringType,
			},
			"step": schema.ListNestedAttribute{
				Description: "Ordered sequence of API steps to execute during the check run.",
				Required:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Description: "Identifier of the step.",
							Computed:    true,
							PlanModifiers: []planmodifier.String{
								stringplanmodifier.UseStateForUnknown(),
							},
						},
						"name": schema.StringAttribute{
							Description: "Name of this step.",
							Required:    true,
						},
						"url": schema.StringAttribute{
							Description: "The URL endpoint to call for this step.",
							Required:    true,
						},
						"method": schema.StringAttribute{
							Description: "HTTP request method: GET, POST, PUT, DELETE, PATCH, etc. (default: GET).",
							Optional:    true,
							Computed:    true,
							Default:     stringdefault.StaticString("GET"),
						},
						"allow_failure": schema.BoolAttribute{
							Description: "Whether subsequent steps can continue if this step fails (default: false).",
							Optional:    true,
							Computed:    true,
							Default:     booldefault.StaticBool(false),
						},
						"consider_test_failed": schema.BoolAttribute{
							Description: "Whether this step's failure marks the entire synthetic test as failed. Only applicable when allow_failure is true (default: true when allow_failure is true).",
							Optional:    true,
							Computed:    true,
						},
						"retry_count": schema.Int64Attribute{
							Description: "Retries for this specific step (default: 0).",
							Optional:    true,
							Computed:    true,
							Default:     int64default.StaticInt64(0),
						},
						"min_location_failed": schema.Int64Attribute{
							Description: "Minimum number of locations that must fail for this step before alerting (default: 1).",
							Optional:    true,
							Computed:    true,
							Default:     int64default.StaticInt64(1),
						},
						"assertion": schema.ListNestedAttribute{
							Description: "List of assertion rules to validate against this step's response (defaults to statusCode is 200).",
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
				},
			},
		},
	}
}

func (r *MultistepCheckResource) getSteps(ctx context.Context, stepsList types.List) ([]APIStepModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	var steps []APIStepModel

	if !stepsList.IsNull() && !stepsList.IsUnknown() {
		diags.Append(stepsList.ElementsAs(ctx, &steps, false)...)
		if diags.HasError() {
			return nil, diags
		}
	}
	return steps, diags
}

func (r *MultistepCheckResource) getStepAssertions(ctx context.Context, assertionsList types.List) ([]APIStepAssertionModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	var assertions []APIStepAssertionModel

	if !assertionsList.IsNull() && !assertionsList.IsUnknown() {
		diags.Append(assertionsList.ElementsAs(ctx, &assertions, false)...)
		if diags.HasError() {
			return nil, diags
		}
	}
	return assertions, diags
}

func mapToStepAssertionModel(a map[string]interface{}) APIStepAssertionModel {
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
	return APIStepAssertionModel{
		Type:                   types.StringValue(aType),
		Operator:               types.StringValue(op),
		Target:                 types.StringValue(target),
		Property:               types.StringValue(prop),
		Unit:                   types.StringValue(unit),
		TargetPropertyOperator: types.StringValue(targetPropOp),
	}
}

func normalizeStepAssertions(input []APIStepAssertionModel) []map[string]interface{} {
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

func (r *MultistepCheckResource) buildStepAssertionList(ctx context.Context, assertions []APIStepAssertionModel) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	if len(assertions) == 0 {
		return types.ListNull(stepAssertionElementType), diags
	}

	var elements []attr.Value
	for _, a := range assertions {
		objVal, objDiags := types.ObjectValue(
			map[string]attr.Type{
				"type":                     types.StringType,
				"operator":                 types.StringType,
				"target":                   types.StringType,
				"property":                 types.StringType,
				"unit":                     types.StringType,
				"target_property_operator": types.StringType,
			},
			map[string]attr.Value{
				"type":                     a.Type,
				"operator":                 a.Operator,
				"target":                   a.Target,
				"property":                 a.Property,
				"unit":                     a.Unit,
				"target_property_operator": a.TargetPropertyOperator,
			},
		)
		diags.Append(objDiags...)
		elements = append(elements, objVal)
	}

	listVal, listDiags := types.ListValue(stepAssertionElementType, elements)
	diags.Append(listDiags...)
	return listVal, diags
}

func (r *MultistepCheckResource) buildStepsList(ctx context.Context, steps []APIStepModel) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	if len(steps) == 0 {
		return types.ListNull(apiStepElementType), diags
	}

	var elements []attr.Value
	for _, s := range steps {
		stepID := s.ID
		if stepID.IsNull() {
			stepID = types.StringUnknown()
		}

		objVal, objDiags := types.ObjectValue(
			map[string]attr.Type{
				"id":                   types.StringType,
				"name":                 types.StringType,
				"url":                  types.StringType,
				"method":               types.StringType,
				"allow_failure":        types.BoolType,
				"consider_test_failed": types.BoolType,
				"retry_count":          types.Int64Type,
				"min_location_failed":  types.Int64Type,
				"assertion":            types.ListType{ElemType: stepAssertionElementType},
			},
			map[string]attr.Value{
				"id":                   stepID,
				"name":                 s.Name,
				"url":                  s.URL,
				"method":               s.Method,
				"allow_failure":        s.AllowFailure,
				"consider_test_failed": s.ConsiderTestFailed,
				"retry_count":          s.RetryCount,
				"min_location_failed":  s.MinLocationFailed,
				"assertion":            s.Assertions,
			},
		)
		diags.Append(objDiags...)
		elements = append(elements, objVal)
	}

	listVal, listDiags := types.ListValue(apiStepElementType, elements)
	diags.Append(listDiags...)
	return listVal, diags
}

func (r *MultistepCheckResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	if r.client == nil {
		return
	}

	var plan MultistepCheckModel
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

	var config MultistepCheckModel
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

	var stateSteps []APIStepModel
	if !req.State.Raw.IsNull() {
		var state MultistepCheckModel
		if diags := req.State.Get(ctx, &state); !diags.HasError() {
			stateSteps, _ = r.getSteps(ctx, state.Steps)
		}
	}

	var configSteps []APIStepModel
	if !req.Config.Raw.IsNull() {
		var config MultistepCheckModel
		if diags := req.Config.Get(ctx, &config); !diags.HasError() {
			configSteps, _ = r.getSteps(ctx, config.Steps)
		}
	}

	steps, stepDiags := r.getSteps(ctx, plan.Steps)
	resp.Diagnostics.Append(stepDiags...)
	if !resp.Diagnostics.HasError() && len(steps) > 0 {
		var updatedSteps []APIStepModel
		for i, s := range steps {
			stepAssertions, aDiags := r.getStepAssertions(ctx, s.Assertions)
			resp.Diagnostics.Append(aDiags...)

			var updatedAssertions []APIStepAssertionModel
			if len(stepAssertions) > 0 {
				for _, a := range stepAssertions {
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
						if prop == "" {
							resp.Diagnostics.AddError("Missing Header Property", "The 'property' field is required when assertion type is 'header' (e.g., property = 'Content-Type').")
						}
						unit = ""
						targetPropOp = ""
					case "body":
						if op == "jsonpath" || op == "xpath" {
							if prop == "" {
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
						aType = "bodyHash"
						if strings.ToLower(op) != "md5" && strings.ToLower(op) != "sha1" && strings.ToLower(op) != "sha256" {
							resp.Diagnostics.AddError("Invalid Hash Operator", fmt.Sprintf("Unsupported operator '%s' for assertion type 'bodyHash'. Supported operators: md5, sha1, sha256", op))
						}
						prop = ""
						unit = ""
						targetPropOp = ""
					default:
						resp.Diagnostics.AddError("Invalid Assertion Type", fmt.Sprintf("Unsupported assertion type '%s'. Supported types: statusCode, responseTime, header, body, bodyHash", aType))
					}

					updatedAssertions = append(updatedAssertions, APIStepAssertionModel{
						Type:                   types.StringValue(aType),
						Operator:               types.StringValue(op),
						Target:                 types.StringValue(target),
						Property:               types.StringValue(prop),
						Unit:                   types.StringValue(unit),
						TargetPropertyOperator: types.StringValue(targetPropOp),
					})
				}
			} else {
				updatedAssertions = []APIStepAssertionModel{
					{
						Type:                   types.StringValue("statusCode"),
						Operator:               types.StringValue("is"),
						Target:                 types.StringValue("200"),
						Property:               types.StringValue(""),
						Unit:                   types.StringValue(""),
						TargetPropertyOperator: types.StringValue(""),
					},
				}
			}

			aList, aBuildDiags := r.buildStepAssertionList(ctx, updatedAssertions)
			resp.Diagnostics.Append(aBuildDiags...)
			s.Assertions = aList

			if i < len(stateSteps) && !stateSteps[i].ID.IsNull() && !stateSteps[i].ID.IsUnknown() && stateSteps[i].ID.ValueString() != "" {
				s.ID = stateSteps[i].ID
			} else {
				s.ID = types.StringUnknown()
			}

			allowFail := false
			if !s.AllowFailure.IsNull() && !s.AllowFailure.IsUnknown() {
				allowFail = s.AllowFailure.ValueBool()
			}

			if !allowFail {
				if i < len(configSteps) && !configSteps[i].ConsiderTestFailed.IsNull() && !configSteps[i].ConsiderTestFailed.IsUnknown() {
					resp.Diagnostics.AddError(
						"Invalid Configuration",
						fmt.Sprintf("Step %d ('%s'): Attribute 'consider_test_failed' cannot be used when 'allow_failure' is false. It is only applicable when 'allow_failure' is true.", i+1, s.Name.ValueString()),
					)
				}
				s.ConsiderTestFailed = types.BoolValue(false)
			} else {
				if s.ConsiderTestFailed.IsNull() || s.ConsiderTestFailed.IsUnknown() {
					s.ConsiderTestFailed = types.BoolValue(true)
				}
			}

			updatedSteps = append(updatedSteps, s)
		}

		if !resp.Diagnostics.HasError() {
			stepsListVal, buildStepsDiags := r.buildStepsList(ctx, updatedSteps)
			resp.Diagnostics.Append(buildStepsDiags...)
			if !resp.Diagnostics.HasError() {
				plan.Steps = stepsListVal
			}
		}
	}

	if !resp.Diagnostics.HasError() {
		resp.Plan.Set(ctx, &plan)
	}
}

func (r *MultistepCheckResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MultistepCheckModel
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

	steps, stepDiags := r.getSteps(ctx, plan.Steps)
	resp.Diagnostics.Append(stepDiags...)

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

	var apiStepsPayload []APIStepPayload
	for _, s := range steps {
		stepAssertions, sADiags := r.getStepAssertions(ctx, s.Assertions)
		resp.Diagnostics.Append(sADiags...)
		if resp.Diagnostics.HasError() {
			return
		}

		payloadStepAssertions := normalizeStepAssertions(stepAssertions)

		method := "GET"
		if !s.Method.IsNull() && !s.Method.IsUnknown() && s.Method.ValueString() != "" {
			method = s.Method.ValueString()
		}

		sRetryCount := int64(0)
		if !s.RetryCount.IsNull() && !s.RetryCount.IsUnknown() {
			sRetryCount = s.RetryCount.ValueInt64()
		}

		sMinLocFailed := int64(1)
		if !s.MinLocationFailed.IsNull() && !s.MinLocationFailed.IsUnknown() {
			sMinLocFailed = s.MinLocationFailed.ValueInt64()
		}

		allowFail := false
		if !s.AllowFailure.IsNull() && !s.AllowFailure.IsUnknown() {
			allowFail = s.AllowFailure.ValueBool()
		}

		considerFail := false
		if allowFail {
			considerFail = true
			if !s.ConsiderTestFailed.IsNull() && !s.ConsiderTestFailed.IsUnknown() {
				considerFail = s.ConsiderTestFailed.ValueBool()
			}
		}

		apiStepsPayload = append(apiStepsPayload, APIStepPayload{
			Name:               s.Name.ValueString(),
			Type:               "api",
			Subtype:            "http",
			AllowFailure:       allowFail,
			ConsiderTestFailed: considerFail,
			RetryCount:         sRetryCount,
			MinLocationFailed:  sMinLocFailed,
			ExtractedValues:    []interface{}{},
			Assertions:         payloadStepAssertions,
			Request: map[string]interface{}{
				"method":                  method,
				"url":                     s.URL.ValueString(),
				"followRedirects":         true,
				"cookie":                  "",
				"ignoreServerCertificate": false,
				"basicAuth": map[string]string{
					"type":     "basic",
					"username": "",
					"password": "",
					"token":    "",
				},
				"headers": []map[string]string{
					{"name": "", "value": ""},
				},
				"proxy": map[string]interface{}{
					"url": "",
					"headers": []map[string]string{
						{"name": "", "value": ""},
					},
				},
				"body": map[string]string{
					"type":  "",
					"value": "",
				},
			},
		})
	}

	freqSec, _, _ := parseFrequency(plan.Frequency.ValueString(), 60)

	runType, _ := normalizeRunType(plan.RunType.ValueString())

	var minLocFailedPtr *int64
	if runType != "sequential" {
		minLocFailedPtr = &minLocationFailed
	}

	payload := MultistepCheckPayload{
		Name:              plan.Name.ValueString(),
		Type:              "api",
		Subtype:           "multi",
		Frequency:         freqSec,
		MinLocationFailed: minLocFailedPtr,
		RetryCount:        retryCount,
		ChannelIDs:        allAPIChannelIDs,
		RunType:           runType,
		Locations:         locations,
		Tags:              tags,
		Assertions:        []map[string]interface{}{},
		APISteps:          apiStepsPayload,
		Request:           nil,
	}

	var atatusResp MultistepCheckResponse
	if err := r.createCheck(ctx, payload, &atatusResp); err != nil {
		resp.Diagnostics.AddError("API Failure On Create", err.Error())
		return
	}

	checkID := atatusResp.ID
	if checkID == "" {
		checkID = atatusResp.IDAlt
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

	var stateSteps []APIStepModel
	for i, respStep := range atatusResp.APISteps {
		var matchedAssertions types.List
		if i < len(steps) {
			matchedAssertions = steps[i].Assertions
		} else {
			var respAssertionModels []APIStepAssertionModel
			for _, a := range respStep.Assertions {
				respAssertionModels = append(respAssertionModels, mapToStepAssertionModel(a))
			}
			builtAssertions, aDiags := r.buildStepAssertionList(ctx, respAssertionModels)
			resp.Diagnostics.Append(aDiags...)
			matchedAssertions = builtAssertions
		}

		considerFailState := respStep.ConsiderTestFailed
		if !respStep.AllowFailure {
			considerFailState = false
		}

		stateSteps = append(stateSteps, APIStepModel{
			ID:                 types.StringValue(respStep.ID),
			Name:               types.StringValue(respStep.Name),
			URL:                types.StringValue(respStep.Request.URL),
			Method:             types.StringValue(respStep.Request.Method),
			AllowFailure:       types.BoolValue(respStep.AllowFailure),
			ConsiderTestFailed: types.BoolValue(considerFailState),
			RetryCount:         types.Int64Value(respStep.RetryCount),
			MinLocationFailed:  types.Int64Value(respStep.MinLocationFailed),
			Assertions:         matchedAssertions,
		})
	}

	finalStepsList, finalStepsDiags := r.buildStepsList(ctx, stateSteps)
	resp.Diagnostics.Append(finalStepsDiags...)
	plan.Steps = finalStepsList

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MultistepCheckResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MultistepCheckModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var atatusResp MultistepCheckResponse
	if err := r.getCheck(ctx, state.ID.ValueString(), "multi", &atatusResp); err != nil {
		if IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("API Failure On Read", err.Error())
		return
	}

	state.Name = types.StringValue(atatusResp.Name)
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

	r.readChannels(ctx, atatusResp.ChannelIDs, &state.Emails, &state.ChannelIDs, &resp.Diagnostics)

	var stateSteps []APIStepModel
	for _, respStep := range atatusResp.APISteps {
		var assertionModels []APIStepAssertionModel
		for _, a := range respStep.Assertions {
			assertionModels = append(assertionModels, mapToStepAssertionModel(a))
		}
		aList, aBuildDiags := r.buildStepAssertionList(ctx, assertionModels)
		resp.Diagnostics.Append(aBuildDiags...)

		considerFailState := respStep.ConsiderTestFailed
		if !respStep.AllowFailure {
			considerFailState = false
		}

		stateSteps = append(stateSteps, APIStepModel{
			ID:                 types.StringValue(respStep.ID),
			Name:               types.StringValue(respStep.Name),
			URL:                types.StringValue(respStep.Request.URL),
			Method:             types.StringValue(respStep.Request.Method),
			AllowFailure:       types.BoolValue(respStep.AllowFailure),
			ConsiderTestFailed: types.BoolValue(considerFailState),
			RetryCount:         types.Int64Value(respStep.RetryCount),
			MinLocationFailed:  types.Int64Value(respStep.MinLocationFailed),
			Assertions:         aList,
		})
	}

	finalStepsList, finalStepsDiags := r.buildStepsList(ctx, stateSteps)
	resp.Diagnostics.Append(finalStepsDiags...)
	state.Steps = finalStepsList

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MultistepCheckResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MultistepCheckModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state MultistepCheckModel
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

	steps, stepDiags := r.getSteps(ctx, plan.Steps)
	resp.Diagnostics.Append(stepDiags...)

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

	var apiStepsPayload []APIStepPayload
	for _, s := range steps {
		stepAssertions, sADiags := r.getStepAssertions(ctx, s.Assertions)
		resp.Diagnostics.Append(sADiags...)
		if resp.Diagnostics.HasError() {
			return
		}

		payloadStepAssertions := normalizeStepAssertions(stepAssertions)

		method := "GET"
		if !s.Method.IsNull() && !s.Method.IsUnknown() && s.Method.ValueString() != "" {
			method = s.Method.ValueString()
		}

		sRetryCount := int64(0)
		if !s.RetryCount.IsNull() && !s.RetryCount.IsUnknown() {
			sRetryCount = s.RetryCount.ValueInt64()
		}

		sMinLocFailed := int64(1)
		if !s.MinLocationFailed.IsNull() && !s.MinLocationFailed.IsUnknown() {
			sMinLocFailed = s.MinLocationFailed.ValueInt64()
		}

		allowFail := false
		if !s.AllowFailure.IsNull() && !s.AllowFailure.IsUnknown() {
			allowFail = s.AllowFailure.ValueBool()
		}

		considerFail := false
		if allowFail {
			considerFail = true
			if !s.ConsiderTestFailed.IsNull() && !s.ConsiderTestFailed.IsUnknown() {
				considerFail = s.ConsiderTestFailed.ValueBool()
			}
		}

		stepID := ""
		if !s.ID.IsNull() && !s.ID.IsUnknown() {
			stepID = s.ID.ValueString()
		}

		apiStepsPayload = append(apiStepsPayload, APIStepPayload{
			ID:                 stepID,
			Name:               s.Name.ValueString(),
			Type:               "api",
			Subtype:            "http",
			AllowFailure:       allowFail,
			ConsiderTestFailed: considerFail,
			RetryCount:         sRetryCount,
			MinLocationFailed:  sMinLocFailed,
			ExtractedValues:    []interface{}{},
			Assertions:         payloadStepAssertions,
			Request: map[string]interface{}{
				"method":                  method,
				"url":                     s.URL.ValueString(),
				"followRedirects":         true,
				"cookie":                  "",
				"ignoreServerCertificate": false,
				"basicAuth": map[string]string{
					"type":     "basic",
					"username": "",
					"password": "",
					"token":    "",
				},
				"headers": []map[string]string{
					{"name": "", "value": ""},
				},
				"proxy": map[string]interface{}{
					"url": "",
					"headers": []map[string]string{
						{"name": "", "value": ""},
					},
				},
				"body": map[string]string{
					"type":  "",
					"value": "",
				},
			},
		})
	}

	freqSec, _, _ := parseFrequency(plan.Frequency.ValueString(), 60)

	runType, _ := normalizeRunType(plan.RunType.ValueString())

	payload := map[string]interface{}{
		"name":       plan.Name.ValueString(),
		"frequency":  freqSec,
		"retryCount": retryCount,
		"channelIds": allAPIChannelIDs,
		"runType":    runType,
		"locations":  locations,
		"tags":       tags,
		"assertions": []map[string]interface{}{},
		"apiSteps":   apiStepsPayload,
		"request":    nil,
	}
	if runType != "sequential" {
		payload["minLocationFailed"] = minLocationFailed
	}

	if err := r.patchCheck(ctx, state.ID.ValueString(), payload); err != nil {
		resp.Diagnostics.AddError("API Failure On Update", err.Error())
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

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MultistepCheckResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MultistepCheckModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.deleteCheck(ctx, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("API Failure On Delete", err.Error())
	}
}
