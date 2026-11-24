package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// atatus_dashboard_restriction manages who may edit a dashboard besides the
// account's owners and admins, through /v2/dashboards/:id/restriction
// (atatus-server controllers/public-api/dashboard-restriction.js over
// dashboard-v2/restriction.js). It is kept apart from the dashboard's content
// (atatus_dashboard_json never sends or compares `restrictedTo`), so the two
// can be owned by different people or configurations.

var (
	_ resource.Resource                = &DashboardRestrictionResource{}
	_ resource.ResourceWithConfigure   = &DashboardRestrictionResource{}
	_ resource.ResourceWithImportState = &DashboardRestrictionResource{}
)

func NewDashboardRestrictionResource() resource.Resource { return &DashboardRestrictionResource{} }

type DashboardRestrictionResource struct {
	client *apiClient
}

type dashboardRestrictionModel struct {
	ID          types.String `tfsdk:"id"`
	DashboardID types.String `tfsdk:"dashboard_id"`
	Editors     types.Set    `tfsdk:"editors"`
}

type restrictionEntry struct {
	PrincipalType string `json:"principalType"`
	PrincipalID   string `json:"principalId"`
	Relation      string `json:"relation,omitempty"`
}

type restrictionDoc struct {
	DashboardID  string             `json:"dashboardId"`
	RestrictedTo []restrictionEntry `json:"restrictedTo"`
}

// The server takes at most this many entries (dashboard-v2/restriction.js MAX_ENTRIES).
const maxRestrictionEntries = 50

var principalPattern = regexp.MustCompile(`^(team|user):[0-9a-f]{24}$`)

func (r *DashboardRestrictionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dashboard_restriction"
}

func (r *DashboardRestrictionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Restricts who may edit an Atatus dashboard: only the listed teams and users, plus the account's owners and admins. " +
			"Destroying it lifts the restriction.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "The dashboard id (the restriction has no id of its own).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"dashboard_id": schema.StringAttribute{
				Required:      true,
				Description:   "Id of the dashboard to restrict. Changing it moves the restriction: the old dashboard's is lifted.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.RegexMatches(objectIDPattern, "must be a dashboard id (24 hex characters)")},
			},
			"editors": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: fmt.Sprintf("Who may edit the dashboard, as `team:<id>` or `user:<id>` (see `data.atatus_team` and `data.atatus_user`), 1 to %d entries.", maxRestrictionEntries),
				Validators: []validator.Set{
					setvalidator.SizeBetween(1, maxRestrictionEntries),
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(principalPattern, `must be "team:<team id>" or "user:<user id>"`)),
				},
			},
		},
	}
}

func (r *DashboardRestrictionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *DashboardRestrictionResource) api() (*apiClient, error) {
	if r.client == nil {
		return nil, errNotConfigured
	}
	return r.client, nil
}

func restrictionPath(dashboardID string) string { return dashboardPath(dashboardID) + "/restriction" }

func restrictionEntries(ctx context.Context, editors types.Set, diags *diag.Diagnostics) []restrictionEntry {
	var principals []string
	diags.Append(editors.ElementsAs(ctx, &principals, false)...)
	sort.Strings(principals)
	entries := make([]restrictionEntry, 0, len(principals))
	for _, p := range principals {
		kind, id, _ := strings.Cut(p, ":")
		entries = append(entries, restrictionEntry{PrincipalType: kind, PrincipalID: id, Relation: "editor"})
	}
	return entries
}

func editorsSet(ctx context.Context, entries []restrictionEntry, diags *diag.Diagnostics) types.Set {
	principals := make([]string, 0, len(entries))
	for _, e := range entries {
		principals = append(principals, e.PrincipalType+":"+e.PrincipalID)
	}
	set, d := types.SetValueFrom(ctx, types.StringType, principals)
	diags.Append(d...)
	return set
}

// put replaces the dashboard's restriction with the plan's and stores what the server answered.
func (r *DashboardRestrictionResource) put(ctx context.Context, plan dashboardRestrictionModel, diags *diag.Diagnostics) (dashboardRestrictionModel, bool) {
	c, err := r.api()
	if err != nil {
		diags.AddError("API Failure", err.Error())
		return plan, false
	}
	entries := restrictionEntries(ctx, plan.Editors, diags)
	if diags.HasError() {
		return plan, false
	}
	var doc restrictionDoc
	if err := c.doJSON(ctx, "PUT", restrictionPath(plan.DashboardID.ValueString()), map[string]any{"restrictedTo": entries}, &doc); err != nil {
		diags.AddError("Could Not Restrict The Dashboard", err.Error())
		return plan, false
	}
	plan.ID = plan.DashboardID
	plan.Editors = editorsSet(ctx, doc.RestrictedTo, diags)
	return plan, !diags.HasError()
}

func (r *DashboardRestrictionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dashboardRestrictionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state, ok := r.put(ctx, plan, &resp.Diagnostics); ok {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	}
}

func (r *DashboardRestrictionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dashboardRestrictionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.api()
	if err != nil {
		resp.Diagnostics.AddError("API Failure On Read", err.Error())
		return
	}
	dashboardID := state.DashboardID.ValueString()
	if dashboardID == "" {
		dashboardID = state.ID.ValueString() // import
	}
	var doc restrictionDoc
	if err := c.doJSON(ctx, "GET", restrictionPath(dashboardID), nil, &doc); err != nil {
		if IsNotFound(err) {
			resp.State.RemoveResource(ctx) // the dashboard is gone, and its restriction with it
			return
		}
		resp.Diagnostics.AddError("API Failure On Read", err.Error())
		return
	}
	state.ID = types.StringValue(dashboardID)
	state.DashboardID = types.StringValue(dashboardID)
	// A restriction lifted in Atatus reads as an empty set, which the plan restores.
	state.Editors = editorsSet(ctx, doc.RestrictedTo, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DashboardRestrictionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dashboardRestrictionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state, ok := r.put(ctx, plan, &resp.Diagnostics); ok {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	}
}

func (r *DashboardRestrictionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dashboardRestrictionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.api()
	if err != nil {
		resp.Diagnostics.AddError("API Failure On Delete", err.Error())
		return
	}
	if err := c.doJSON(ctx, "DELETE", restrictionPath(state.DashboardID.ValueString()), nil, nil); err != nil && !IsNotFound(err) {
		resp.Diagnostics.AddError("API Failure On Delete", err.Error())
	}
}

// ImportState takes the dashboard id.
func (r *DashboardRestrictionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !objectIDPattern.MatchString(req.ID) {
		resp.Diagnostics.AddError("Not A Dashboard Id", fmt.Sprintf("%q is not a dashboard id (24 hex characters).", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("dashboard_id"), req.ID)...)
}
