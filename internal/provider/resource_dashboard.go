package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &DashboardResource{}
	_ resource.ResourceWithConfigure   = &DashboardResource{}
	_ resource.ResourceWithModifyPlan  = &DashboardResource{}
	_ resource.ResourceWithImportState = &DashboardResource{}
)

const dashboardsPath = "/v2/dashboards"

func NewDashboardResource() resource.Resource {
	return &DashboardResource{}
}

// DashboardResource manages one Atatus Custom Dashboard as a single JSON
// document: the file React's "Export → JSON" writes.
type DashboardResource struct {
	client *apiClient
}

type dashboardResourceModel struct {
	ID            types.String       `tfsdk:"id"`
	DashboardJSON dashboardJSONValue `tfsdk:"dashboard_json"`
	URL           types.String       `tfsdk:"url"`
	Version       types.Int64        `tfsdk:"version"`
}

func (r *DashboardResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dashboard_json"
}

func (r *DashboardResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Atatus Custom Dashboard from the JSON document that Export → JSON writes.",
		Attributes: map[string]schema.Attribute{
			"dashboard_json": schema.StringAttribute{
				Description: "The dashboard as a JSON object (Export → JSON in the dashboard, or jsonencode(...)). " +
					"Key order, whitespace and server-managed fields (_id, __v, timestamps, author) in what Atatus returns never show as drift; " +
					"changing only the formatting of the configuration still plans an update, which applies the same board.",
				Required:   true,
				CustomType: dashboardJSONType{},
				Validators: []validator.String{dashboardJSONObjectValidator{}},
			},
			"id": schema.StringAttribute{
				Description:   "The dashboard id.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"url": schema.StringAttribute{
				Description:   "Link that opens the dashboard in Atatus.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"version": schema.Int64Attribute{
				Description: "The server's revision counter for the board (__v). Sent on update so a board edited elsewhere since the last refresh is not overwritten.",
				Computed:    true,
			},
		},
	}
}

func (r *DashboardResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *DashboardResource) requireClient(diags *diag.Diagnostics) bool {
	if r.client == nil {
		diags.AddError("Provider Not Configured", "The atatus provider has not been configured; set api_key (and endpoint, if not the default).")
		return false
	}
	return true
}

func dashboardPath(id string) string {
	return dashboardsPath + "/" + url.PathEscape(id)
}

func (r *DashboardResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return // destroy
	}

	var plan dashboardResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.DashboardJSON.IsNull() {
		return
	}

	if !req.State.Raw.IsNull() {
		var state dashboardResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if !plan.DashboardJSON.IsUnknown() && plan.DashboardJSON.ValueString() == state.DashboardJSON.ValueString() {
			return // nothing to apply, nothing to validate
		}
		// An update re-imports the board: the server bumps __v and may
		// return a new link. A document known only at apply time (it
		// references another resource's computed value) is an update too, so
		// these are unknown from the first plan; the final plan must not turn
		// a known value unknown.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("url"), types.StringUnknown())...)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("version"), types.Int64Unknown())...)
	}

	if plan.DashboardJSON.IsUnknown() {
		return // validated at apply time, once it is known
	}

	if r.client == nil {
		return
	}

	// Server-side validation at plan time; nothing is written.
	err := r.client.doJSON(ctx, http.MethodPost, dashboardsPath+"?dryRun=true", []byte(plan.DashboardJSON.ValueString()), nil)
	switch {
	case err == nil:
	case apiStatusCode(err) == http.StatusBadRequest:
		resp.Diagnostics.AddAttributeError(path.Root("dashboard_json"), "Invalid Dashboard", apiErrorMessage(err))
	case apiStatusCode(err) == http.StatusForbidden:
		// Apply would be refused the same way (key lacks Write, or a
		// non-owner/admin key sets isReadOnly), so fail the plan now.
		resp.Diagnostics.AddAttributeError(path.Root("dashboard_json"), "Dashboard Not Permitted", apiErrorMessage(err))
	default:
		resp.Diagnostics.AddAttributeWarning(path.Root("dashboard_json"), "Dashboard Validation Skipped",
			"Could not validate the dashboard with the Atatus API at plan time; it will be validated on apply. "+err.Error())
	}
}

func apiErrorMessage(err error) string {
	if e, ok := err.(*apiError); ok {
		return e.Message
	}
	return err.Error()
}

func (r *DashboardResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if !r.requireClient(&resp.Diagnostics) {
		return
	}
	var plan dashboardResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var raw json.RawMessage
	if err := r.client.doJSON(ctx, http.MethodPost, dashboardsPath, []byte(plan.DashboardJSON.ValueString()), &raw); err != nil {
		resp.Diagnostics.AddError("Error Creating Dashboard", err.Error())
		return
	}

	resp.Diagnostics.Append(applyServerDashboard(raw, plan.DashboardJSON.ValueString(), &plan)...)
	if plan.ID.IsUnknown() {
		return // no id came back: nothing Terraform could track
	}
	// Saved even if the response could not be fully read, so the created
	// board is tracked (and destroyable) rather than orphaned.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *DashboardResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !r.requireClient(&resp.Diagnostics) {
		return
	}
	var state dashboardResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var raw json.RawMessage
	err := r.client.doJSON(ctx, http.MethodGet, dashboardPath(state.ID.ValueString()), nil, &raw)
	if IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Dashboard", err.Error())
		return
	}

	prior := ""
	if !state.DashboardJSON.IsNull() && !state.DashboardJSON.IsUnknown() {
		prior = state.DashboardJSON.ValueString()
	}
	resp.Diagnostics.Append(applyServerDashboard(raw, prior, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DashboardResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if !r.requireClient(&resp.Diagnostics) {
		return
	}
	var plan, state dashboardResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, err := decodeJSONObject(plan.DashboardJSON.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("dashboard_json"), "Invalid dashboard_json", err.Error())
		return
	}
	if !state.Version.IsNull() && !state.Version.IsUnknown() {
		body["expectedVersion"] = state.Version.ValueInt64()
	}

	var raw json.RawMessage
	err = r.client.doJSON(ctx, http.MethodPut, dashboardPath(state.ID.ValueString()), body, &raw)
	if IsConflict(err) {
		resp.Diagnostics.AddError("Dashboard Changed Outside Terraform",
			fmt.Sprintf("Dashboard %s was changed in Atatus after Terraform last read it (version %d), so it was not overwritten. "+
				"Run \"terraform apply -refresh-only\" to accept the current board, or run \"terraform plan\" again to see the difference and re-apply. Server said: %s",
				state.ID.ValueString(), state.Version.ValueInt64(), apiErrorMessage(err)))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Updating Dashboard", err.Error())
		return
	}

	if kept := boardKeysKeptByServer(state.DashboardJSON.ValueString(), plan.DashboardJSON.ValueString(), raw); len(kept) > 0 {
		resp.Diagnostics.AddAttributeWarning(path.Root("dashboard_json"), "Removed Board Settings Were Not Cleared",
			fmt.Sprintf("These board settings were removed from dashboard_json but Atatus kept their current values: %s. "+
				"An update only writes the settings the document contains, so leaving one out does not clear it, and Terraform "+
				"does not track it once it is gone from the document. To change one, set it explicitly in dashboard_json "+
				"(for example \"description\": \"\").", strings.Join(kept, ", ")))
	}
	if spacesKeptOnOmission(state.DashboardJSON.ValueString(), plan.DashboardJSON.ValueString(), raw) {
		resp.Diagnostics.AddAttributeWarning(path.Root("dashboard_json"), "Dashboard Spaces Kept",
			"spaceIds was removed from dashboard_json, so the board keeps its current Spaces: leaving them out never makes a board "+
				"visible account-wide. Terraform no longer tracks them. To make the board account-wide, set \"spaceIds\": [].")
	}

	plan.ID = state.ID
	resp.Diagnostics.Append(applyServerDashboard(raw, plan.DashboardJSON.ValueString(), &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *DashboardResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !r.requireClient(&resp.Diagnostics) {
		return
	}
	var state dashboardResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.doJSON(ctx, http.MethodDelete, dashboardPath(state.ID.ValueString()), nil, nil)
	if err != nil && !IsNotFound(err) {
		resp.Diagnostics.AddError("Error Deleting Dashboard", err.Error())
	}
}

func (r *DashboardResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// boardKeysKeptByServer lists the top-level board keys that were in the prior
// document, are not in the planned one, and are still on the board after the
// update. The server's update writes only the board fields the body carries
// (a $set), so removing a field from the document leaves its stored value in
// place; pruning would then hide it. widgets and tabs are always replaced.
func boardKeysKeptByServer(prior, planned string, raw json.RawMessage) []string {
	priorDoc, err := decodeJSONObject(prior)
	if err != nil {
		return nil
	}
	plannedDoc, err := decodeJSONObject(planned)
	if err != nil {
		return nil
	}
	server, err := decodeJSONObject(string(raw))
	if err != nil {
		return nil
	}
	stripServerKeys(server)
	var kept []string
	for k := range priorDoc {
		// spaceIds is kept by design when a document leaves it out (spacesKeptOnOmission).
		if k == "widgets" || k == "tabs" || k == "spaceIds" {
			continue
		}
		if _, inPlan := plannedDoc[k]; inPlan {
			continue
		}
		if _, onServer := server[k]; onServer {
			kept = append(kept, k)
		}
	}
	sort.Strings(kept)
	return kept
}

// spacesKeptOnOmission: the prior document set spaceIds, the planned one does not, and the board still
// carries Spaces. Atatus keeps a board's Spaces when a document leaves them out (so an omission never makes
// a board account-wide); `"spaceIds": []` clears them.
func spacesKeptOnOmission(prior, planned string, raw json.RawMessage) bool {
	priorDoc, err1 := decodeJSONObject(prior)
	plannedDoc, err2 := decodeJSONObject(planned)
	server, err3 := decodeJSONObject(string(raw))
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	_, hadSpaces := priorDoc["spaceIds"]
	_, plansSpaces := plannedDoc["spaceIds"]
	kept, _ := server["spaceIds"].([]any)
	return hadSpaces && !plansSpaces && len(kept) > 0
}

// applyServerDashboard copies id/url/version from a full dashboard object into
// m and sets m.DashboardJSON: prior is kept when the server's canonical form is
// semantically equal to it (so Terraform sees no change), otherwise the
// server's canonical document is stored and the difference shows as drift.
// prior is "" on import, when every key the server returns is kept.
func applyServerDashboard(raw json.RawMessage, prior string, m *dashboardResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	server, err := decodeJSONObject(string(raw))
	if err != nil {
		diags.AddError("Unexpected Dashboard Response", "The Atatus API returned a dashboard that is "+err.Error())
		return diags
	}

	if id, ok := server["_id"].(string); ok && id != "" {
		m.ID = types.StringValue(id)
	} else if m.ID.IsUnknown() || m.ID.IsNull() {
		diags.AddError("Unexpected Dashboard Response", "The Atatus API response has no dashboard _id.")
		return diags
	}

	if u, ok := server["url"].(string); ok {
		m.URL = types.StringValue(u)
	} else {
		m.URL = types.StringNull()
	}

	m.Version = types.Int64Null()
	if n, ok := server["__v"].(json.Number); ok {
		if v, err := n.Int64(); err == nil {
			m.Version = types.Int64Value(v)
		}
	}

	var priorDoc map[string]any
	if strings.TrimSpace(prior) != "" {
		priorDoc, err = decodeJSONObject(prior)
		if err != nil {
			priorDoc = nil // state from an older/broken write: keep everything
		}
	}

	canonical, err := marshalCanonical(canonicalizeServerDashboard(server, priorDoc))
	if err != nil {
		diags.AddError("Unexpected Dashboard Response", "Could not encode the dashboard: "+err.Error())
		return diags
	}

	if priorDoc != nil && dashboardJSONSemanticallyEqual(prior, canonical) {
		m.DashboardJSON = newDashboardJSONValue(prior)
	} else {
		m.DashboardJSON = newDashboardJSONValue(canonical)
	}
	return diags
}
