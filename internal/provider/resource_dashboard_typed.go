package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// atatus_dashboard: a Custom Dashboard written as typed HCL. Its attributes come
// from the dashboard JSON Schema (dashboard_typed_schema.go); it is converted
// to the same JSON document atatus_dashboard_json sends, through the same
// /v2/dashboards routes (create, dry-run validation at plan time, replace with
// expectedVersion, delete).
//
//   widgets = [{ timeseries = { title = "...", requests = [...], definition = {...} } }]
//   tabs    = [{ name = "Overview", widgets = [1, 2] }]   // 1-based widget positions

var (
	_ resource.Resource                   = &TypedDashboardResource{}
	_ resource.ResourceWithConfigure      = &TypedDashboardResource{}
	_ resource.ResourceWithImportState    = &TypedDashboardResource{}
	_ resource.ResourceWithModifyPlan     = &TypedDashboardResource{}
	_ resource.ResourceWithValidateConfig = &TypedDashboardResource{}
)

func NewTypedDashboardResource() resource.Resource { return &TypedDashboardResource{} }

type TypedDashboardResource struct {
	client *apiClient
}

// The resource's own (non-document) attributes.
var typedComputed = map[string]bool{"id": true, "url": true, "version": true}

func (r *TypedDashboardResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dashboard"
}

func (r *TypedDashboardResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	m, err := loadDashboardModel()
	if err != nil {
		resp.Diagnostics.AddError("Dashboard Schema Error", err.Error())
		return
	}
	attrs := m.Board.objectAttributes()
	widgetList := &dsNode{Kind: dsList, Items: m.Widget, MaxItems: 500,
		Desc: "The dashboard's widgets, in order. Each sets exactly one widget type, e.g. `{ timeseries = { ... } }`."}
	attrs["widgets"] = widgetList.attribute(true)
	attrs["tabs"] = schema.ListNestedAttribute{
		Optional:    true,
		Description: "Tabs. Each names the widgets on it by their 1-based position in `widgets`; every widget is on exactly one tab. Absent: no tabs.",
		Validators:  []validator.List{listvalidator.SizeAtMost(30)},
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Required: true, Description: "Tab name, at most 100 characters.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 100)}},
			"widgets": schema.ListAttribute{Required: true, ElementType: types.Int64Type,
				Description: "Positions (1-based) in `widgets` of the widgets on this tab."},
		}},
	}
	attrs["id"] = schema.StringAttribute{Computed: true, Description: "The dashboard id.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
	attrs["url"] = schema.StringAttribute{Computed: true, Description: "Link that opens the dashboard in Atatus."}
	attrs["version"] = schema.Int64Attribute{Computed: true,
		Description: "The board's version (__v). Sent on update, so a board changed in Atatus since the last refresh is not overwritten."}
	resp.Schema = schema.Schema{
		Description: "Manages an Atatus Custom Dashboard written as typed HCL (dashboard schema " + m.Version + "). " +
			"For a dashboard kept as the JSON document Export → JSON writes, use atatus_dashboard_json.",
		Attributes: attrs,
	}
}

func (r *TypedDashboardResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// --- the whole resource value -------------------------------------------------

func objectOf(ctx context.Context, s tfsdk.State) (basetypes.ObjectValue, diag.Diagnostics) {
	return rawObject(ctx, s.Schema.Type(), s.Raw)
}

func rawObject(ctx context.Context, t attr.Type, raw tftypes.Value) (basetypes.ObjectValue, diag.Diagnostics) {
	var diags diag.Diagnostics
	v, err := t.ValueFromTerraform(ctx, raw)
	if err != nil {
		diags.AddError("Value Conversion Error", err.Error())
		return basetypes.ObjectValue{}, diags
	}
	obj, ok := v.(basetypes.ObjectValue)
	if !ok {
		diags.AddError("Value Conversion Error", fmt.Sprintf("expected an object, got %T", v))
	}
	return obj, diags
}

func withAttrs(obj basetypes.ObjectValue, set map[string]attr.Value) basetypes.ObjectValue {
	attrs := obj.Attributes()
	out := make(map[string]attr.Value, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	for k, v := range set {
		out[k] = v
	}
	return types.ObjectValueMust(obj.AttributeTypes(context.Background()), out)
}

// documentOf is the dashboard document the resource value describes.
func documentOf(m *dashboardModel, obj basetypes.ObjectValue) (map[string]any, error) {
	attrs := obj.Attributes()
	doc, err := m.Board.objectToJSON(attrs)
	if err != nil {
		return nil, err
	}
	tabbed := !absent(attrs["tabs"])
	widgets := []any{}
	if wl, ok := attrs["widgets"].(basetypes.ListValue); ok && !absent(wl) {
		for i, w := range wl.Elements() {
			val, _, err := m.Widget.toJSON(w)
			if err != nil {
				return nil, fmt.Errorf("widgets[%d]: %w", i, err)
			}
			wm, _ := val.(map[string]any)
			if wm == nil {
				return nil, fmt.Errorf("widgets[%d]: empty widget", i)
			}
			if tabbed {
				wm["id"] = fmt.Sprintf("w%d", i+1)
			}
			fillResponseFormats(m, wm)
			widgets = append(widgets, wm)
		}
	}
	doc["widgets"] = widgets
	if tabbed {
		tabs := []any{}
		for _, t := range attrs["tabs"].(basetypes.ListValue).Elements() {
			ta := t.(basetypes.ObjectValue).Attributes()
			ids := []any{}
			for _, p := range ta["widgets"].(basetypes.ListValue).Elements() {
				ids = append(ids, fmt.Sprintf("w%d", p.(basetypes.Int64Value).ValueInt64()))
			}
			tabs = append(tabs, map[string]any{"name": ta["name"].(basetypes.StringValue).ValueString(), "widgetIds": ids})
		}
		doc["tabs"] = tabs
	}
	return doc, nil
}

// fillResponseFormats gives each request without one its widget type's
// response format (a split graph: the format of the type it repeats).
func fillResponseFormats(m *dashboardModel, w map[string]any) {
	typ, _ := w["type"].(string)
	rf := m.ResponseFormats[typ]
	if def, ok := w["definition"].(map[string]any); ok && rf == "" {
		if split, ok := def["split"].(map[string]any); ok {
			src, _ := split["sourceType"].(string)
			rf = m.ResponseFormats[src]
		}
	}
	if reqs, ok := w["requests"].([]any); ok && rf != "" {
		for _, r := range reqs {
			if rm, ok := r.(map[string]any); ok {
				if _, has := rm["responseFormat"]; !has {
					rm["responseFormat"] = rf
				}
			}
		}
	}
	if children, ok := w["widgets"].([]any); ok {
		for _, c := range children {
			if cm, ok := c.(map[string]any); ok {
				fillResponseFormats(m, cm)
			}
		}
	}
}

// stateFromServer reads the API's dashboard into the resource value, guided by
// prior (the configuration-shaped state), or in full on import (fill).
// order is the widget ids in configuration order, as the last write answered
// them (Atatus may list a board's widgets in another order); widgets it does
// not name follow, in the server's order. The ids used are returned.
func stateFromServer(m *dashboardModel, raw json.RawMessage, prior basetypes.ObjectValue, fill bool, order []string) (basetypes.ObjectValue, []string, []string, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return prior, nil, nil, fmt.Errorf("could not read the dashboard: %w", err)
	}
	id, _ := doc["_id"].(string)
	url, _ := doc["url"].(string)
	version, _ := doc["__v"].(float64)
	serverWidgets := orderWidgets(doc["widgets"], order)
	doc["widgets"] = serverWidgets
	var ids []string
	position := map[string]int64{}
	for i, w := range serverWidgets {
		if wm, ok := w.(map[string]any); ok {
			if wid, ok := wm["_id"].(string); ok {
				position[wid] = int64(i + 1)
				ids = append(ids, wid)
			}
		}
	}
	serverTabs, _ := doc["tabs"].([]any)
	stripServerKeys(doc)
	delete(doc, "tabs") // read below, as positions
	stripGroupChildServerKeys(doc)

	rc := &readCtx{}
	if fill {
		rc.dropped = map[string]bool{}
	}
	priorAttrs := map[string]attr.Value{}
	if !prior.IsNull() && !prior.IsUnknown() {
		priorAttrs = prior.Attributes()
	}
	boardDoc := make(map[string]any, len(doc))
	for k, v := range doc {
		if k != "widgets" {
			boardDoc[k] = v
		}
	}
	board := m.Board.objectFromJSON(boardDoc, priorAttrs, fill, "dashboard", rc)
	out := map[string]attr.Value{}
	if bo, ok := board.(basetypes.ObjectValue); ok && !bo.IsNull() {
		for k, v := range bo.Attributes() {
			out[k] = v
		}
	} else {
		for k, v := range nullAttrs(m.Board) {
			out[k] = v
		}
	}
	widgetList := &dsNode{Kind: dsList, Items: m.Widget}
	widgets := widgetList.fromJSON(serverWidgets, priorAttrs["widgets"], fill, "widgets", rc)
	if widgets.IsNull() {
		widgets = types.ListValueMust(m.Widget.attrType(), []attr.Value{}) // widgets is required
	}
	out["widgets"] = widgets

	tabType := types.ObjectType{AttrTypes: map[string]attr.Type{"name": types.StringType, "widgets": types.ListType{ElemType: types.Int64Type}}}
	out["tabs"] = types.ListNull(tabType)
	if len(serverTabs) > 0 && (fill || !absent(priorAttrs["tabs"])) {
		tabs := []attr.Value{}
		for _, t := range serverTabs {
			tm, _ := t.(map[string]any)
			name, _ := tm["name"].(string)
			ids, _ := tm["widgetIds"].([]any)
			positions := []attr.Value{}
			for _, wid := range ids {
				if p, ok := position[fmt.Sprint(wid)]; ok {
					positions = append(positions, types.Int64Value(p))
				}
			}
			tabs = append(tabs, types.ObjectValueMust(tabType.AttrTypes, map[string]attr.Value{
				"name": types.StringValue(name), "widgets": types.ListValueMust(types.Int64Type, positions)}))
		}
		out["tabs"] = types.ListValueMust(tabType, tabs)
	}
	out["id"] = types.StringValue(id)
	out["url"] = types.StringValue(url)
	out["version"] = types.Int64Value(int64(version))

	var dropped []string
	for k := range rc.dropped {
		dropped = append(dropped, k)
	}
	sort.Strings(dropped)
	attrTypes := prior.AttributeTypes(context.Background())
	return types.ObjectValueMust(attrTypes, out), dropped, ids, nil
}

func orderWidgets(raw any, order []string) []any {
	list, _ := raw.([]any)
	if len(order) == 0 {
		return list
	}
	byID := map[string]any{}
	for _, w := range list {
		if wm, ok := w.(map[string]any); ok {
			if wid, ok := wm["_id"].(string); ok {
				byID[wid] = w
			}
		}
	}
	out := make([]any, 0, len(list))
	used := map[string]bool{}
	for _, wid := range order {
		if w, ok := byID[wid]; ok && !used[wid] {
			out = append(out, w)
			used[wid] = true
		}
	}
	for _, w := range list {
		wm, _ := w.(map[string]any)
		wid, _ := wm["_id"].(string)
		if wid == "" || !used[wid] {
			out = append(out, w)
		}
	}
	return out
}

const widgetOrderKey = "widget_ids"

// privateSetter is what Create, Read and Update responses offer to keep private state.
type privateSetter interface {
	SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics
}

func saveWidgetOrder(ctx context.Context, p privateSetter, ids []string) diag.Diagnostics {
	b, _ := json.Marshal(ids)
	return p.SetKey(ctx, widgetOrderKey, b)
}

// --- plan-time checks -----------------------------------------------------------

func (r *TypedDashboardResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var widgets types.List
	var tabs types.List
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("widgets"), &widgets)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("tabs"), &tabs)...)
	if resp.Diagnostics.HasError() || widgets.IsUnknown() || tabs.IsNull() || tabs.IsUnknown() {
		return
	}
	n := len(widgets.Elements())
	seen := map[int64]string{}
	for ti, t := range tabs.Elements() {
		to, ok := t.(basetypes.ObjectValue)
		if !ok || to.IsUnknown() {
			return
		}
		name := to.Attributes()["name"].(basetypes.StringValue).ValueString()
		list, ok := to.Attributes()["widgets"].(basetypes.ListValue)
		if !ok || list.IsUnknown() {
			return
		}
		for _, p := range list.Elements() {
			pv, ok := p.(basetypes.Int64Value)
			if !ok || pv.IsUnknown() {
				return
			}
			pos := pv.ValueInt64()
			at := path.Root("tabs").AtListIndex(ti).AtName("widgets")
			if pos < 1 || pos > int64(n) {
				resp.Diagnostics.AddAttributeError(at, "Invalid Widget Position",
					fmt.Sprintf("Tab %q names widget %d, but there are %d widgets (positions start at 1).", name, pos, n))
				continue
			}
			if other, dup := seen[pos]; dup {
				resp.Diagnostics.AddAttributeError(at, "Widget On Two Tabs",
					fmt.Sprintf("Widget %d is on tab %q and on tab %q; a widget is on exactly one tab.", pos, other, name))
			}
			seen[pos] = name
		}
	}
	var missing []string
	for i := 1; i <= n; i++ {
		if _, ok := seen[int64(i)]; !ok {
			missing = append(missing, fmt.Sprint(i))
		}
	}
	if len(missing) > 0 && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddAttributeError(path.Root("tabs"), "Widget On No Tab",
			"On a tabbed dashboard every widget is on a tab (Atatus would move it to the first one). Not on any tab: "+strings.Join(missing, ", ")+".")
	}
}

func (r *TypedDashboardResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return // destroy
	}
	if !req.State.Raw.IsNull() {
		if req.Plan.Raw.Equal(req.State.Raw) {
			return
		}
		if r.sameDocument(ctx, req.Plan, req.State) {
			// The framework marks the computed attributes unknown on any raw difference;
			// the dashboard is unchanged, so they keep their values and nothing is planned.
			var url types.String
			var version types.Int64
			resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("url"), &url)...)
			resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("version"), &version)...)
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("url"), url)...)
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("version"), version)...)
			return
		}
		// An update replaces the board: its version moves and its link may change.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("url"), types.StringUnknown())...)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("version"), types.Int64Unknown())...)
	} else {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("url"), types.StringUnknown())...)
	}
	if r.client == nil {
		return
	}
	// Server-side validation at plan time, once the document is known; nothing is written.
	m, err := loadDashboardModel()
	if err != nil {
		return
	}
	obj, d := rawObject(ctx, req.Plan.Schema.Type(), req.Plan.Raw)
	if d.HasError() || !documentKnown(obj) {
		return
	}
	doc, err := documentOf(m, obj)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Dashboard", err.Error())
		return
	}
	err = r.client.doJSON(ctx, http.MethodPost, dashboardsPath+"?dryRun=true", doc, nil)
	switch {
	case err == nil:
	case apiStatusCode(err) == http.StatusBadRequest:
		resp.Diagnostics.AddError("Invalid Dashboard", apiErrorMessage(err))
	case apiStatusCode(err) == http.StatusForbidden:
		resp.Diagnostics.AddError("Dashboard Not Permitted", apiErrorMessage(err))
	default:
		resp.Diagnostics.AddWarning("Dashboard Validation Skipped",
			"Could not validate the dashboard with the Atatus API at plan time; it will be validated on apply. "+err.Error())
	}
}

// sameDocument: the plan describes the document the state does. A number written
// in configuration keeps more precision than the same number read back into state
// (0.1 as a decimal vs as a float64), so the raw values can differ while the
// dashboard does not; nothing is then planned.
func (r *TypedDashboardResource) sameDocument(ctx context.Context, plan tfsdk.Plan, state tfsdk.State) bool {
	m, err := loadDashboardModel()
	if err != nil {
		return false
	}
	po, d := rawObject(ctx, plan.Schema.Type(), plan.Raw)
	if d.HasError() || !documentKnown(po) {
		return false
	}
	so, d := objectOf(ctx, state)
	if d.HasError() {
		return false
	}
	pd, err1 := documentOf(m, po)
	sd, err2 := documentOf(m, so)
	return err1 == nil && err2 == nil && canonicalJSON(pd) == canonicalJSON(sd)
}

// documentKnown: every document attribute (all but id, url, version) is known.
func documentKnown(obj basetypes.ObjectValue) bool {
	for k, v := range obj.Attributes() {
		if typedComputed[k] {
			continue
		}
		tv, err := v.ToTerraformValue(context.Background())
		if err != nil || !tv.IsFullyKnown() {
			return false
		}
	}
	return true
}

// --- CRUD -------------------------------------------------------------------------

func (r *TypedDashboardResource) prepare(ctx context.Context, plan tfsdk.Plan, diags *diag.Diagnostics) (*dashboardModel, basetypes.ObjectValue, map[string]any, bool) {
	if r.client == nil {
		diags.AddError("API Failure", errNotConfigured.Error())
		return nil, basetypes.ObjectValue{}, nil, false
	}
	m, err := loadDashboardModel()
	if err != nil {
		diags.AddError("Dashboard Schema Error", err.Error())
		return nil, basetypes.ObjectValue{}, nil, false
	}
	obj, d := rawObject(ctx, plan.Schema.Type(), plan.Raw)
	diags.Append(d...)
	if diags.HasError() {
		return nil, basetypes.ObjectValue{}, nil, false
	}
	doc, err := documentOf(m, obj)
	if err != nil {
		diags.AddError("Invalid Dashboard", err.Error())
		return nil, basetypes.ObjectValue{}, nil, false
	}
	return m, obj, doc, true
}

type dashboardAnswer struct {
	ID      string  `json:"_id"`
	URL     string  `json:"url"`
	Version float64 `json:"__v"`
	Widgets []struct {
		ID string `json:"_id"`
	} `json:"widgets"`
}

func (a dashboardAnswer) widgetIDs() []string {
	ids := make([]string, 0, len(a.Widgets))
	for _, w := range a.Widgets {
		ids = append(ids, w.ID)
	}
	return ids
}

func (r *TypedDashboardResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	_, obj, doc, ok := r.prepare(ctx, req.Plan, &resp.Diagnostics)
	if !ok {
		return
	}
	var ans dashboardAnswer
	if err := r.client.doJSON(ctx, http.MethodPost, dashboardsPath, doc, &ans); err != nil {
		resp.Diagnostics.AddError("Error Creating Dashboard", err.Error())
		return
	}
	if ans.ID == "" {
		resp.Diagnostics.AddError("Error Creating Dashboard", "Atatus answered without a dashboard id.")
		return
	}
	// The configuration is what Atatus stored; a difference shows on the next refresh.
	state := withAttrs(obj, map[string]attr.Value{
		"id": types.StringValue(ans.ID), "url": types.StringValue(ans.URL), "version": types.Int64Value(int64(ans.Version))})
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	resp.Diagnostics.Append(saveWidgetOrder(ctx, resp.Private, ans.widgetIDs())...)
}

func (r *TypedDashboardResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("API Failure On Read", errNotConfigured.Error())
		return
	}
	m, err := loadDashboardModel()
	if err != nil {
		resp.Diagnostics.AddError("Dashboard Schema Error", err.Error())
		return
	}
	prior, d := objectOf(ctx, req.State)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := prior.Attributes()["id"].(basetypes.StringValue)
	var raw json.RawMessage
	err = r.client.doJSON(ctx, http.MethodGet, dashboardPath(id.ValueString()), nil, &raw)
	if IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Dashboard", err.Error())
		return
	}
	// `title` is required, so a state without one is an import: read everything.
	title, _ := prior.Attributes()["title"].(basetypes.StringValue)
	fill := title.IsNull()
	var order []string
	if b, d := req.Private.GetKey(ctx, widgetOrderKey); !d.HasError() && len(b) > 0 {
		_ = json.Unmarshal(b, &order)
	}
	state, dropped, ids, err := stateFromServer(m, raw, prior, fill, order)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Dashboard", err.Error())
		return
	}
	if len(dropped) > 0 {
		shown := dropped
		if len(shown) > 20 {
			shown = append(shown[:20:20], fmt.Sprintf("... and %d more", len(dropped)-20))
		}
		resp.Diagnostics.AddWarning("Dashboard Settings Not Imported",
			"These settings of the dashboard have no attribute in atatus_dashboard (schema "+m.Version+"), so they were not imported and "+
				"would be removed by the next apply. Keep this dashboard with atatus_dashboard_json to preserve them:\n  "+strings.Join(shown, "\n  "))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	resp.Diagnostics.Append(saveWidgetOrder(ctx, resp.Private, ids)...)
}

func (r *TypedDashboardResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	_, obj, doc, ok := r.prepare(ctx, req.Plan, &resp.Diagnostics)
	if !ok {
		return
	}
	prior, d := objectOf(ctx, req.State)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := prior.Attributes()["id"].(basetypes.StringValue).ValueString()
	if v, ok := prior.Attributes()["version"].(basetypes.Int64Value); ok && !absent(v) {
		doc["expectedVersion"] = v.ValueInt64()
	}
	var ans dashboardAnswer
	err := r.client.doJSON(ctx, http.MethodPut, dashboardPath(id), doc, &ans)
	switch {
	case IsConflict(err):
		resp.Diagnostics.AddError("Dashboard Changed Outside Terraform",
			fmt.Sprintf("Dashboard %s was changed in Atatus after Terraform last read it, so it was not overwritten. "+
				"Run \"terraform apply -refresh-only\" to accept the current board, or \"terraform plan\" to see the difference and apply again. Server said: %s",
				id, apiErrorMessage(err)))
		return
	case IsNotFound(err):
		resp.Diagnostics.AddError("Dashboard Deleted Outside Terraform",
			"Dashboard "+id+" was deleted in Atatus after Terraform last read it. Run \"terraform apply\" again to create it anew.")
		return
	case err != nil:
		resp.Diagnostics.AddError("Error Updating Dashboard", err.Error())
		return
	}
	state := withAttrs(obj, map[string]attr.Value{
		"id": types.StringValue(id), "url": types.StringValue(ans.URL), "version": types.Int64Value(int64(ans.Version))})
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	resp.Diagnostics.Append(saveWidgetOrder(ctx, resp.Private, ans.widgetIDs())...)
}

func (r *TypedDashboardResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("API Failure On Delete", errNotConfigured.Error())
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.doJSON(ctx, http.MethodDelete, dashboardPath(id.ValueString()), nil, nil); err != nil && !IsNotFound(err) {
		resp.Diagnostics.AddError("Error Deleting Dashboard", err.Error())
	}
}

func (r *TypedDashboardResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- validators ---------------------------------------------------------------------

// exactlyOneWidgetType: a widget sets one widget type.
type exactlyOneWidgetType struct{}

func (exactlyOneWidgetType) Description(ctx context.Context) string {
	return "sets exactly one widget type"
}
func (v exactlyOneWidgetType) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (exactlyOneWidgetType) ValidateObject(ctx context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var set []string
	for name, v := range req.ConfigValue.Attributes() {
		if !v.IsNull() {
			set = append(set, name)
		}
	}
	sort.Strings(set)
	if len(set) != 1 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Widget",
			fmt.Sprintf("A widget sets exactly one widget type (e.g. `{ timeseries = { ... } }`); this one sets %d: %s.", len(set), strings.Join(set, ", ")))
	}
}

// jsonStringValidator: a *_json attribute holds valid JSON.
type jsonStringValidator struct{}

func (jsonStringValidator) Description(ctx context.Context) string {
	return "must be valid JSON (jsonencode)"
}
func (v jsonStringValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (jsonStringValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if !json.Valid([]byte(req.ConfigValue.ValueString())) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid JSON", "Must be a JSON value; write it with jsonencode(...).")
	}
}
