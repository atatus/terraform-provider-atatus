package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// atatus_powerpack manages a custom powerpack (a reusable group of widgets)
// through /v2/powerpacks (atatus-server controllers/public-api/powerpacks.js
// over dashboard-v2/powerpacks.js).
//
// The server rewrites the group it stores (it gives every child a
// powerpackKey, fixes layoutType, normalises filters), so group_widget_json is
// never compared with what the server answers. The pack's `version` moves on
// every change to the layout or the variables instead: while it is the version
// Terraform last wrote, the configured JSON stands; once it moves (an edit in
// Atatus), the server's group replaces it in state and the plan shows the drift.

var (
	_ resource.Resource                = &PowerpackResource{}
	_ resource.ResourceWithConfigure   = &PowerpackResource{}
	_ resource.ResourceWithImportState = &PowerpackResource{}
	_ resource.ResourceWithModifyPlan  = &PowerpackResource{}
)

func NewPowerpackResource() resource.Resource { return &PowerpackResource{} }

type PowerpackResource struct {
	client *apiClient
}

type powerpackModel struct {
	ID                types.String       `tfsdk:"id"`
	Name              types.String       `tfsdk:"name"`
	Description       types.String       `tfsdk:"description"`
	Tags              types.List         `tfsdk:"tags"`
	Editors           types.List         `tfsdk:"editors"`
	TemplateVariables types.List         `tfsdk:"template_variables"`
	GroupWidgetJSON   dashboardJSONValue `tfsdk:"group_widget_json"`
	Version           types.Int64        `tfsdk:"version"`
	Revision          types.Int64        `tfsdk:"revision"`
}

type powerpackVariableModel struct {
	Name            types.String `tfsdk:"name"`
	Prefix          types.String `tfsdk:"prefix"`
	Defaults        types.List   `tfsdk:"defaults"`
	AvailableValues types.List   `tfsdk:"available_values"`
}

var powerpackVariableAttrTypes = map[string]attr.Type{
	"name":             types.StringType,
	"prefix":           types.StringType,
	"defaults":         types.ListType{ElemType: types.StringType},
	"available_values": types.ListType{ElemType: types.StringType},
}

type powerpackVariableDoc struct {
	Name            string   `json:"name"`
	Prefix          string   `json:"prefix"`
	Default         []string `json:"default"`
	AvailableValues []string `json:"availableValues"`
}

// powerpackDoc is a custom pack as the server answers it.
type powerpackDoc struct {
	ID                string                 `json:"id"`
	Source            string                 `json:"source"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description"`
	Tags              []string               `json:"tags"`
	Editors           []string               `json:"editors"`
	TemplateVariables []powerpackVariableDoc `json:"templateVariables"`
	GroupWidget       map[string]any         `json:"groupWidget"`
	Version           int64                  `json:"version"`
	Revision          int64                  `json:"revision"`
}

type powerpackSyncedDashboard struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type powerpackUpdateAnswer struct {
	Powerpack powerpackDoc `json:"powerpack"`
	Synced    struct {
		Dashboards []powerpackSyncedDashboard `json:"dashboards"`
		Skipped    []powerpackSyncedDashboard `json:"skipped"`
	} `json:"synced"`
}

// Server limits (dashboard-v2/helper/powerpacks.js, dashboard-v2/powerpacks.js).
const (
	maxPowerpackDescription = 80
	maxPowerpackTags        = 8
	maxPowerpackTag         = 80
	maxPowerpackEditors     = 50
	maxPowerpackWidgets     = 100
	powerpackGridColumns    = 12
)

var (
	objectIDPattern  = regexp.MustCompile(`^[0-9a-f]{24}$`)
	trimmedPattern   = regexp.MustCompile(`^\S(?:.*\S)?$`)
	groupWidgetKeys  = keySet([]string{"title", "showTitle", "definition", "widgets"})
	nestedGroupTypes = map[string]bool{"group": true, "split_group": true}
)

func (r *PowerpackResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_powerpack"
}

func (r *PowerpackResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a custom Atatus powerpack: a reusable group of widgets that dashboards add as linked groups. " +
			"A change to its layout or variables updates every dashboard that uses it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Id of the powerpack.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the powerpack, without leading or trailing spaces.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(trimmedPattern, "must not be empty or start or end with a space"),
				},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
				Description: fmt.Sprintf("Description of the powerpack, at most %d characters. Default: empty.", maxPowerpackDescription),
				Validators:  []validator.String{stringvalidator.LengthAtMost(maxPowerpackDescription)},
			},
			"tags": schema.ListAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Default:     listdefault.StaticValue(emptyStringList),
				Description: fmt.Sprintf("Tags of the powerpack: at most %d, each at most %d characters. Default: none.", maxPowerpackTags, maxPowerpackTag),
				Validators: []validator.List{
					listvalidator.SizeAtMost(maxPowerpackTags),
					listvalidator.UniqueValues(),
					listvalidator.ValueStringsAre(
						stringvalidator.LengthBetween(1, maxPowerpackTag),
						stringvalidator.RegexMatches(trimmedPattern, "must not be blank or start or end with a space"),
					),
				},
			},
			"editors": schema.ListAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Default:     listdefault.StaticValue(emptyStringList),
				Description: fmt.Sprintf("Ids of the users who may change the powerpack besides its author and the account's owners and admins (at most %d; see `data.atatus_user`). "+
					"Only the author or an admin may change this list. Default: none.", maxPowerpackEditors),
				Validators: []validator.List{
					listvalidator.SizeAtMost(maxPowerpackEditors),
					listvalidator.UniqueValues(),
					listvalidator.ValueStringsAre(stringvalidator.RegexMatches(objectIDPattern, "must be a user id (24 hex characters)")),
				},
			},
			"template_variables": schema.ListNestedAttribute{
				Optional:    true,
				Computed:    true,
				Default:     listdefault.StaticValue(types.ListValueMust(types.ObjectType{AttrTypes: powerpackVariableAttrTypes}, []attr.Value{})),
				Description: "Variables the pack's widgets filter on. A dashboard that adds the pack either pins a variable's value or binds it to its own variable of the same name. Default: none.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name of the variable (letters, digits, `_`, `-` and `.`).",
						},
						"prefix": schema.StringAttribute{
							Required:    true,
							Description: "The attribute the variable filters on, such as `service` or `host`.",
						},
						"defaults": schema.ListAttribute{
							Optional:    true,
							Computed:    true,
							ElementType: types.StringType,
							Default:     listdefault.StaticValue(emptyStringList),
							Description: "Values selected by default. Default: none (all values).",
						},
						"available_values": schema.ListAttribute{
							Optional:    true,
							Computed:    true,
							ElementType: types.StringType,
							Default:     listdefault.StaticValue(emptyStringList),
							Description: "Values the variable may take. Default: none (any value).",
						},
					},
				},
			},
			"group_widget_json": schema.StringAttribute{
				Required:   true,
				CustomType: dashboardJSONType{},
				Validators: []validator.String{groupWidgetJSONValidator{}},
				Description: "The pack's group as a JSON object: `title`, `showTitle`, `definition` (header settings such as `headerStyle`, `backgroundColor`, `titleAlign`) " +
					fmt.Sprintf("and `widgets`, 1 to %d widgets laid out in a %d-column grid (`layout` = `{x, y, width, height}`). ", maxPowerpackWidgets, powerpackGridColumns) +
					"Give each widget a `definition.powerpackKey` to keep its identity when widgets are reordered; Atatus numbers unkeyed widgets by position. " +
					"Key order and whitespace in what Atatus returns never show as drift; changing only the formatting of the configuration still plans an update.",
			},
			"version": schema.Int64Attribute{
				Computed:    true,
				Description: "The layout version dashboards link to. It moves on every change to `group_widget_json` or `template_variables`.",
			},
			"revision": schema.Int64Attribute{
				Computed:    true,
				Description: "Moves on every change to the powerpack. An update made from an older revision is refused, so a change made in Atatus is never silently overwritten.",
			},
		},
	}
}

func (r *PowerpackResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cfg, ok := req.ProviderData.(*AtatusConfig)
	if !ok {
		resp.Diagnostics.AddError("Configuration Error", fmt.Sprintf("Expected *AtatusConfig, got %T", req.ProviderData))
		return
	}
	r.client = cfg.Client
}

func (r *PowerpackResource) api() (*apiClient, error) {
	if r.client == nil {
		return nil, errNotConfigured
	}
	return r.client, nil
}

// ModifyPlan keeps `version` known when the update leaves the layout and the
// variables as they are, so dashboards that reference it (to link an
// instance) do not plan an update for a rename or a new tag.
func (r *PowerpackResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state powerpackModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || plan.GroupWidgetJSON.IsUnknown() || plan.TemplateVariables.IsUnknown() {
		return
	}
	if dashboardJSONSemanticallyEqual(plan.GroupWidgetJSON.ValueString(), state.GroupWidgetJSON.ValueString()) &&
		plan.TemplateVariables.Equal(state.TemplateVariables) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("version"), state.Version)...)
	}
}

func powerpackPath(id string) string { return "/v2/powerpacks/" + url.PathEscape(id) }

// powerpackBody is the whole pack as the configuration describes it.
func powerpackBody(ctx context.Context, m powerpackModel, diags *diag.Diagnostics) map[string]any {
	tags, d := stringsFromList(ctx, m.Tags, []string{})
	diags.Append(d...)
	editors, d := stringsFromList(ctx, m.Editors, []string{})
	diags.Append(d...)

	var vars []powerpackVariableModel
	if !m.TemplateVariables.IsNull() && !m.TemplateVariables.IsUnknown() {
		diags.Append(m.TemplateVariables.ElementsAs(ctx, &vars, false)...)
	}
	variables := make([]map[string]any, 0, len(vars))
	for _, v := range vars {
		defaults, d := stringsFromList(ctx, v.Defaults, []string{})
		diags.Append(d...)
		available, d := stringsFromList(ctx, v.AvailableValues, []string{})
		diags.Append(d...)
		variables = append(variables, map[string]any{
			"name": v.Name.ValueString(), "prefix": v.Prefix.ValueString(),
			"default": defaults, "availableValues": available,
		})
	}

	var groupWidget map[string]any
	if err := json.Unmarshal([]byte(m.GroupWidgetJSON.ValueString()), &groupWidget); err != nil {
		diags.AddAttributeError(path.Root("group_widget_json"), "Invalid group_widget_json", err.Error())
	}
	return map[string]any{
		"name":              m.Name.ValueString(),
		"description":       m.Description.ValueString(),
		"tags":              tags,
		"editors":           editors,
		"templateVariables": variables,
		"groupWidget":       groupWidget,
	}
}

func powerpackVariables(ctx context.Context, docs []powerpackVariableDoc, diags *diag.Diagnostics) types.List {
	values := make([]attr.Value, 0, len(docs))
	for _, v := range docs {
		obj, d := types.ObjectValue(powerpackVariableAttrTypes, map[string]attr.Value{
			"name":             types.StringValue(v.Name),
			"prefix":           types.StringValue(v.Prefix),
			"defaults":         stringList(ctx, v.Default, diags),
			"available_values": stringList(ctx, v.AvailableValues, diags),
		})
		diags.Append(d...)
		values = append(values, obj)
	}
	list, d := types.ListValue(types.ObjectType{AttrTypes: powerpackVariableAttrTypes}, values)
	diags.Append(d...)
	return list
}

// serverGroupWidgetJSON is the server's group as group_widget_json writes it:
// layoutType (always "ordered") and empty objects (an unset "definition")
// left out, keys sorted — the form jsonencode() of the same group produces,
// so an imported pack plans no change against a configuration that matches.
func serverGroupWidgetJSON(gw map[string]any) (string, error) {
	out := map[string]any{}
	for k, v := range gw {
		if groupWidgetKeys[k] {
			out[k] = v
		}
	}
	dropEmptyObjects(out)
	return marshalCanonical(out)
}

// applyPowerpack copies the server's pack into the model. The configured
// group_widget_json is kept while the server's layout version is the one in
// the model (keepLayout); otherwise the server's group replaces it.
func applyPowerpack(ctx context.Context, doc powerpackDoc, m *powerpackModel, keepLayout bool, diags *diag.Diagnostics) {
	m.ID = types.StringValue(doc.ID)
	m.Name = types.StringValue(doc.Name)
	m.Description = types.StringValue(doc.Description)
	m.Tags = stringList(ctx, doc.Tags, diags)
	m.Editors = stringList(ctx, doc.Editors, diags)
	m.TemplateVariables = powerpackVariables(ctx, doc.TemplateVariables, diags)
	if !keepLayout {
		s, err := serverGroupWidgetJSON(doc.GroupWidget)
		if err != nil {
			diags.AddError("Unreadable Powerpack", "Could not encode the powerpack's group: "+err.Error())
			return
		}
		m.GroupWidgetJSON = newDashboardJSONValue(s)
	}
	m.Version = types.Int64Value(doc.Version)
	m.Revision = types.Int64Value(doc.Revision)
}

func (r *PowerpackResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan powerpackModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.api()
	if err != nil {
		resp.Diagnostics.AddError("API Failure On Create", err.Error())
		return
	}
	body := powerpackBody(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	var doc powerpackDoc
	if err := c.doJSON(ctx, "POST", "/v2/powerpacks", body, &doc); err != nil {
		resp.Diagnostics.AddError("API Failure On Create", err.Error())
		return
	}
	// The planned values are what the server stored; take the id and counters.
	plan.ID = types.StringValue(doc.ID)
	plan.Version = types.Int64Value(doc.Version)
	plan.Revision = types.Int64Value(doc.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PowerpackResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state powerpackModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.api()
	if err != nil {
		resp.Diagnostics.AddError("API Failure On Read", err.Error())
		return
	}
	var doc powerpackDoc
	if err := c.doJSON(ctx, "GET", powerpackPath(state.ID.ValueString()), nil, &doc); err != nil {
		if IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("API Failure On Read", err.Error())
		return
	}
	if doc.Source != "" && doc.Source != "custom" {
		resp.Diagnostics.AddError("Not A Custom Powerpack",
			fmt.Sprintf("%q is a powerpack from the Atatus catalog, which cannot be managed. Use the atatus_powerpack data source to refer to it.", state.ID.ValueString()))
		return
	}
	keepLayout := !state.GroupWidgetJSON.IsNull() && !state.GroupWidgetJSON.IsUnknown() &&
		!state.Version.IsNull() && state.Version.ValueInt64() == doc.Version
	applyPowerpack(ctx, doc, &state, keepLayout, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PowerpackResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state powerpackModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.api()
	if err != nil {
		resp.Diagnostics.AddError("API Failure On Update", err.Error())
		return
	}
	body := powerpackBody(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	body["expectedRevision"] = state.Revision.ValueInt64()
	var answer powerpackUpdateAnswer
	if err := c.doJSON(ctx, "PATCH", powerpackPath(state.ID.ValueString()), body, &answer); err != nil {
		switch {
		case IsConflict(err):
			resp.Diagnostics.AddError(
				"Powerpack Changed Outside Terraform",
				"The powerpack was changed in Atatus after Terraform last read it, so the update was refused rather than overwrite that change. "+
					"Run `terraform plan` to see the difference and apply again, or `terraform apply -refresh-only` to accept the powerpack as it is.\n\n"+err.Error(),
			)
		case IsNotFound(err):
			resp.Diagnostics.AddError(
				"Powerpack Deleted Outside Terraform",
				"The powerpack was deleted in Atatus after Terraform last read it. Run `terraform apply` again to create it anew.\n\n"+err.Error(),
			)
		default:
			resp.Diagnostics.AddError("API Failure On Update", err.Error())
		}
		return
	}
	if skipped := answer.Synced.Skipped; len(skipped) > 0 {
		names := make([]string, 0, len(skipped))
		for _, d := range skipped {
			names = append(names, fmt.Sprintf("%q (%s)", d.Title, d.ID))
		}
		resp.Diagnostics.AddWarning(
			"Some Dashboards Still Show The Old Powerpack",
			"The powerpack was updated, but these read-only dashboards were not, because the API key's creator is not an owner or admin: "+
				strings.Join(names, ", ")+". An owner or admin can update them from the dashboard (Update to latest).",
		)
	}
	plan.ID = state.ID
	plan.Version = types.Int64Value(answer.Powerpack.Version)
	plan.Revision = types.Int64Value(answer.Powerpack.Revision)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PowerpackResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state powerpackModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.api()
	if err != nil {
		resp.Diagnostics.AddError("API Failure On Delete", err.Error())
		return
	}
	if err := c.doJSON(ctx, "DELETE", powerpackPath(state.ID.ValueString()), nil, nil); err != nil && !IsNotFound(err) {
		resp.Diagnostics.AddError("API Failure On Delete", err.Error())
	}
}

func (r *PowerpackResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !objectIDPattern.MatchString(req.ID) {
		resp.Diagnostics.AddError("Not A Custom Powerpack Id",
			fmt.Sprintf("%q is not the id of a custom powerpack (24 hex characters). Powerpacks from the Atatus catalog cannot be managed; use the atatus_powerpack data source to refer to them.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// groupWidgetJSONValidator refuses a group_widget_json the server would refuse,
// so the mistake shows at plan time: not an object, unknown keys, no widgets or
// too many, a nested group, a layout outside the grid, a repeated powerpackKey.
type groupWidgetJSONValidator struct{}

func (groupWidgetJSONValidator) Description(ctx context.Context) string {
	return "must be a JSON object {title, showTitle, definition, widgets} with 1 to 100 widgets laid out in a 12-column grid"
}

func (v groupWidgetJSONValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (groupWidgetJSONValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	doc, err := decodeJSONObject(req.ConfigValue.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid group_widget_json", "group_widget_json "+err.Error())
		return
	}
	for _, problem := range groupWidgetProblems(doc) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid group_widget_json", problem)
	}
}

func groupWidgetProblems(doc map[string]any) []string {
	var problems []string
	for _, k := range sortedKeys(doc) {
		if !groupWidgetKeys[k] {
			problems = append(problems, fmt.Sprintf("%s: not a powerpack group key; use title, showTitle, definition and widgets", k))
		}
	}
	widgets, ok := doc["widgets"].([]any)
	if !ok || len(widgets) == 0 {
		return append(problems, "widgets: a powerpack needs at least one widget")
	}
	if len(widgets) > maxPowerpackWidgets {
		problems = append(problems, fmt.Sprintf("widgets: a powerpack can have at most %d widgets", maxPowerpackWidgets))
	}
	keys := map[string]bool{}
	for i, w := range widgets {
		at := fmt.Sprintf("widgets[%d]", i)
		wm, ok := w.(map[string]any)
		if !ok {
			problems = append(problems, at+": must be a widget object")
			continue
		}
		if typ, _ := wm["type"].(string); nestedGroupTypes[typ] {
			problems = append(problems, at+".type: a powerpack cannot contain groups or split graphs")
		}
		if p := gridLayoutProblem(wm["layout"]); p != "" {
			problems = append(problems, at+".layout: "+p)
		}
		if def, _ := wm["definition"].(map[string]any); def != nil {
			if key, has := def["powerpackKey"]; has {
				s, _ := key.(string)
				if s == "" {
					problems = append(problems, at+".definition.powerpackKey: must be non-empty text")
				} else if keys[s] {
					problems = append(problems, fmt.Sprintf("%s.definition.powerpackKey: %q is used by another widget", at, s))
				}
				keys[s] = true
			}
		}
	}
	return problems
}

func gridLayoutProblem(raw any) string {
	l, _ := raw.(map[string]any)
	num := func(k string) (float64, bool) {
		n, ok := l[k].(json.Number)
		if !ok {
			return 0, false
		}
		f, err := n.Float64()
		return f, err == nil
	}
	x, okX := num("x")
	y, okY := num("y")
	w, okW := num("width")
	h, okH := num("height")
	if !okX || !okY || !okW || !okH {
		return "needs numeric x, y, width and height"
	}
	if x < 0 || y < 0 || w < 1 || h < 1 || x+w > powerpackGridColumns {
		return fmt.Sprintf("must fit the %d-column grid (x ≥ 0, y ≥ 0, width and height ≥ 1, x + width ≤ %d)", powerpackGridColumns, powerpackGridColumns)
	}
	return ""
}
