package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// atatus_dashboard_group manages a dashboard group (a folder of dashboards)
// through /v2/dashboard-groups (atatus-server controllers/public-api/dashboard-groups.js).
// The group's dashboards are an ordered list; the server keeps that order.

var (
	_ resource.Resource                = &DashboardGroupResource{}
	_ resource.ResourceWithConfigure   = &DashboardGroupResource{}
	_ resource.ResourceWithImportState = &DashboardGroupResource{}
)

func NewDashboardGroupResource() resource.Resource { return &DashboardGroupResource{} }

type DashboardGroupResource struct {
	client *apiClient
}

type dashboardGroupModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Description  types.String `tfsdk:"description"`
	Order        types.Int64  `tfsdk:"order"`
	DashboardIDs types.List   `tfsdk:"dashboard_ids"`
	SpaceIDs     types.List   `tfsdk:"space_ids"`
	Version      types.Int64  `tfsdk:"version"`
}

// groupDoc decodes a group as both the write routes (dashboards as ids) and
// the single read (dashboards as {_id, title}) answer it.
type groupDoc struct {
	ID          string        `json:"_id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Order       float64       `json:"order"`
	Dashboards  []groupMember `json:"dashboards"`
	SpaceIDs    []string      `json:"spaceIds"`
	Version     int64         `json:"__v"`
}

var emptyStringList = types.ListValueMust(types.StringType, []attr.Value{})

func (r *DashboardGroupResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dashboard_group"
}

func (r *DashboardGroupResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Atatus dashboard group: a named folder of dashboards, in a set order.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Id of the group.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the group.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
				Description: "Description of the group. Default: empty.",
			},
			"order": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(0),
				Description: "Position of the group among the account's groups (lowest first). Default: 0.",
			},
			"dashboard_ids": schema.ListAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Default:     listdefault.StaticValue(emptyStringList),
				Validators:  []validator.List{listvalidator.UniqueValues()},
				Description: "Ids of the group's dashboards, in the order the group shows them. Each dashboard may appear once. Default: none.",
			},
			"space_ids": schema.ListAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Default:     listdefault.StaticValue(emptyStringList),
				Validators:  []validator.List{listvalidator.UniqueValues()},
				Description: "Ids of the Spaces the group is labelled with. Default: none (visible account-wide).",
			},
			"version": schema.Int64Attribute{
				Computed:    true,
				Description: "The group's version, which every change moves on. An update made from an older version is refused, so a change made in Atatus is never silently overwritten.",
			},
		},
	}
}

func (r *DashboardGroupResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *DashboardGroupResource) api() (*apiClient, error) {
	if r.client == nil {
		return nil, errNotConfigured
	}
	return r.client, nil
}

func groupPath(id string) string { return "/v2/dashboard-groups/" + url.PathEscape(id) }

// groupBody is the whole group as the configuration describes it.
func groupBody(ctx context.Context, m dashboardGroupModel, diags *diag.Diagnostics) map[string]any {
	dashboards, d := stringsFromList(ctx, m.DashboardIDs, []string{})
	diags.Append(d...)
	spaces, d := stringsFromList(ctx, m.SpaceIDs, []string{})
	diags.Append(d...)
	return map[string]any{
		"name":        m.Name.ValueString(),
		"description": m.Description.ValueString(),
		"order":       m.Order.ValueInt64(),
		"dashboards":  dashboards,
		"spaceIds":    spaces,
	}
}

// applyGroup copies the server's group into the model.
func applyGroup(ctx context.Context, doc groupDoc, m *dashboardGroupModel, diags *diag.Diagnostics) {
	ids := make([]string, 0, len(doc.Dashboards))
	for _, member := range doc.Dashboards {
		ids = append(ids, member.ID)
	}
	m.ID = types.StringValue(doc.ID)
	m.Name = types.StringValue(doc.Name)
	m.Description = types.StringValue(doc.Description)
	m.Order = types.Int64Value(int64(doc.Order))
	m.DashboardIDs = stringList(ctx, ids, diags)
	m.SpaceIDs = stringList(ctx, doc.SpaceIDs, diags)
	m.Version = types.Int64Value(doc.Version)
}

func (r *DashboardGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dashboardGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.api()
	if err != nil {
		resp.Diagnostics.AddError("API Failure On Create", err.Error())
		return
	}
	body := groupBody(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	var doc groupDoc
	if err := c.doJSON(ctx, "POST", "/v2/dashboard-groups", body, &doc); err != nil {
		resp.Diagnostics.AddError("API Failure On Create", err.Error())
		return
	}
	// The write answers what it stored; take the id and version from it and
	// keep the planned values (the server stores them as sent).
	plan.ID = types.StringValue(doc.ID)
	plan.Version = types.Int64Value(doc.Version)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *DashboardGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dashboardGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.api()
	if err != nil {
		resp.Diagnostics.AddError("API Failure On Read", err.Error())
		return
	}
	var doc groupDoc
	if err := c.doJSON(ctx, "GET", groupPath(state.ID.ValueString()), nil, &doc); err != nil {
		if IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("API Failure On Read", err.Error())
		return
	}
	applyGroup(ctx, doc, &state, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DashboardGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state dashboardGroupModel
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
	body := groupBody(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	body["expectedVersion"] = state.Version.ValueInt64()
	var doc groupDoc
	if err := c.doJSON(ctx, "PUT", groupPath(state.ID.ValueString()), body, &doc); err != nil {
		if IsConflict(err) {
			resp.Diagnostics.AddError(
				"Dashboard Group Changed Outside Terraform",
				"The group was changed in Atatus after Terraform last read it, so the update was refused rather than overwrite that change. "+
					"Run `terraform plan` to see the difference and apply again, or `terraform apply -refresh-only` to accept the group as it is.\n\n"+err.Error(),
			)
			return
		}
		if IsNotFound(err) {
			resp.Diagnostics.AddError(
				"Dashboard Group Deleted Outside Terraform",
				"The group was deleted in Atatus after Terraform last read it. Run `terraform apply` again to create it anew.\n\n"+err.Error(),
			)
			return
		}
		resp.Diagnostics.AddError("API Failure On Update", err.Error())
		return
	}
	plan.ID = state.ID
	plan.Version = types.Int64Value(doc.Version)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *DashboardGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dashboardGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.api()
	if err != nil {
		resp.Diagnostics.AddError("API Failure On Delete", err.Error())
		return
	}
	if err := c.doJSON(ctx, "DELETE", groupPath(state.ID.ValueString()), nil, nil); err != nil && !IsNotFound(err) {
		resp.Diagnostics.AddError("API Failure On Delete", err.Error())
	}
}

func (r *DashboardGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
