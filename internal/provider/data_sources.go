package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Data sources look existing Atatus objects up by name (or id) through the
// public API's read-only lookups (atatus-server controllers/public-api/lookups.js),
// so a configuration can refer to a dashboard, group, channel, project,
// Space, team or user it does not manage.

// lookupBase is embedded by every data source: the shared client and the
// list/match helpers.
type lookupBase struct {
	client *apiClient
}

func (b *lookupBase) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cfg, ok := req.ProviderData.(*AtatusConfig)
	if !ok {
		resp.Diagnostics.AddError("Configuration Error", fmt.Sprintf("Expected *AtatusConfig, got %T", req.ProviderData))
		return
	}
	b.client = cfg.Client
}

func (b *lookupBase) api() (*apiClient, error) {
	if b.client == nil {
		return nil, errNotConfigured
	}
	return b.client, nil
}

// lookupPageSize is the largest page the lookups serve.
const lookupPageSize = 100

// lookupMaxPages bounds how many pages a list data source reads.
const lookupMaxPages = 50

type lookupPage[T any] struct {
	Data       []T `json:"data"`
	TotalCount int `json:"totalCount"`
}

// listAll reads every page of a paginated lookup (up to lookupMaxPages).
func listAll[T any](ctx context.Context, c *apiClient, p string, query url.Values) ([]T, error) {
	if query == nil {
		query = url.Values{}
	}
	var all []T
	for page := 1; page <= lookupMaxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		query.Set("limit", strconv.Itoa(lookupPageSize))
		var out lookupPage[T]
		if err := c.doJSON(ctx, "GET", p+"?"+query.Encode(), nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if len(out.Data) < lookupPageSize || len(all) >= out.TotalCount {
			return all, nil
		}
	}
	return all, nil
}

// oneMatch returns the single element of matches, or an error that says
// whether nothing or several objects matched; hint tells the user how to
// narrow an ambiguous lookup.
func oneMatch[T any](kind, field, value, hint string, matches []T) (T, error) {
	var zero T
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return zero, fmt.Errorf("no %s with %s %q was found in the Atatus account", kind, field, value)
	default:
		return zero, fmt.Errorf("%d %ss have %s %q; %s", len(matches), kind, field, value, hint)
	}
}

const (
	hintByID   = "look it up by id instead"
	hintByType = "set type to choose between them"
	hintRename = "give them distinct names in Atatus"
)

// idOrName adds the "id" and name attributes that a lookup takes, exactly one
// of which must be set.
func idOrNameAttributes(kind, nameAttr, nameDescription string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Description: "Id of the " + kind + ". Set this or " + nameAttr + ".",
			Optional:    true,
			Computed:    true,
			Validators: []validator.String{
				stringvalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot(nameAttr)),
			},
		},
		nameAttr: schema.StringAttribute{
			Description: nameDescription + " Set this or id.",
			Optional:    true,
			Computed:    true,
			Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
		},
	}
}

func lookupFailed(diags interface{ AddError(string, string) }, kind string, err error) {
	diags.AddError("Atatus "+kind+" Lookup Failed", err.Error())
}

// --- atatus_dashboard ---------------------------------------------------

type dashboardLookupDoc struct {
	ID            string   `json:"_id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	DashboardType string   `json:"dashboardType"`
	LayoutType    string   `json:"layoutType"`
	SpaceIDs      []string `json:"spaceIds"`
	IsReadOnly    bool     `json:"isReadOnly"`
	URL           string   `json:"url"`
}

type dashboardDataModel struct {
	ID            types.String `tfsdk:"id"`
	Title         types.String `tfsdk:"title"`
	Description   types.String `tfsdk:"description"`
	DashboardType types.String `tfsdk:"dashboard_type"`
	LayoutType    types.String `tfsdk:"layout_type"`
	SpaceIDs      types.List   `tfsdk:"space_ids"`
	IsReadOnly    types.Bool   `tfsdk:"is_read_only"`
	URL           types.String `tfsdk:"url"`
}

type DashboardDataSource struct{ lookupBase }

func NewDashboardDataSource() datasource.DataSource { return &DashboardDataSource{} }

func (d *DashboardDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dashboard"
}

func (d *DashboardDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := idOrNameAttributes("dashboard", "title", "Exact title of the dashboard; it must match exactly one dashboard.")
	attrs["description"] = schema.StringAttribute{Computed: true, Description: "Description of the dashboard."}
	attrs["dashboard_type"] = schema.StringAttribute{Computed: true, Description: "custom_timeboard or custom_screenboard."}
	attrs["layout_type"] = schema.StringAttribute{Computed: true, Description: "ordered or free."}
	attrs["space_ids"] = schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Ids of the Spaces the dashboard is labelled with."}
	attrs["is_read_only"] = schema.BoolAttribute{Computed: true, Description: "Whether only owners and admins may edit the dashboard."}
	attrs["url"] = schema.StringAttribute{Computed: true, Description: "Link to the dashboard in Atatus."}
	resp.Schema = schema.Schema{
		Description: "Looks up an existing Atatus dashboard by id or exact title.",
		Attributes:  attrs,
	}
}

func (d *DashboardDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data dashboardDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.api()
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Dashboard", err)
		return
	}

	var doc dashboardLookupDoc
	if isKnown(data.ID) && data.ID.ValueString() != "" {
		err = c.doJSON(ctx, "GET", "/v2/dashboards/"+url.PathEscape(data.ID.ValueString()), nil, &doc)
	} else {
		var matches []dashboardLookupDoc
		matches, err = listAll[dashboardLookupDoc](ctx, c, "/v2/dashboards", url.Values{"title": {data.Title.ValueString()}})
		if err == nil {
			doc, err = oneMatch("dashboard", "title", data.Title.ValueString(), hintByID, matches)
		}
	}
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Dashboard", err)
		return
	}

	data.ID = types.StringValue(doc.ID)
	data.Title = types.StringValue(doc.Title)
	data.Description = types.StringValue(doc.Description)
	data.DashboardType = types.StringValue(doc.DashboardType)
	data.LayoutType = types.StringValue(doc.LayoutType)
	data.SpaceIDs = stringList(ctx, doc.SpaceIDs, &resp.Diagnostics)
	data.IsReadOnly = types.BoolValue(doc.IsReadOnly)
	data.URL = types.StringValue(doc.URL)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// stringList is a list value that is empty (not null) for a nil slice.
func stringList(ctx context.Context, values []string, diags *diag.Diagnostics) types.List {
	if values == nil {
		values = []string{}
	}
	list, d := types.ListValueFrom(ctx, types.StringType, values)
	diags.Append(d...)
	return list
}

// --- atatus_dashboards --------------------------------------------------

type dashboardsDataModel struct {
	Search     types.String `tfsdk:"search"`
	Dashboards types.List   `tfsdk:"dashboards"`
}

var dashboardSummaryType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"id":             types.StringType,
	"title":          types.StringType,
	"description":    types.StringType,
	"dashboard_type": types.StringType,
	"url":            types.StringType,
}}

type DashboardsDataSource struct{ lookupBase }

func NewDashboardsDataSource() datasource.DataSource { return &DashboardsDataSource{} }

func (d *DashboardsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dashboards"
}

func (d *DashboardsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the account's dashboards, newest first, optionally only those whose title or description contains a search string.",
		Attributes: map[string]schema.Attribute{
			"search": schema.StringAttribute{
				Optional:    true,
				Description: "Only dashboards whose title or description contains this text (case-insensitive).",
			},
			"dashboards": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The matching dashboards.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":             schema.StringAttribute{Computed: true},
					"title":          schema.StringAttribute{Computed: true},
					"description":    schema.StringAttribute{Computed: true},
					"dashboard_type": schema.StringAttribute{Computed: true},
					"url":            schema.StringAttribute{Computed: true},
				}},
			},
		},
	}
}

func (d *DashboardsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data dashboardsDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.api()
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Dashboards", err)
		return
	}
	query := url.Values{}
	if s := data.Search.ValueString(); s != "" {
		query.Set("search", s)
	}
	docs, err := listAll[dashboardLookupDoc](ctx, c, "/v2/dashboards", query)
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Dashboards", err)
		return
	}
	type summary struct {
		ID            string `tfsdk:"id"`
		Title         string `tfsdk:"title"`
		Description   string `tfsdk:"description"`
		DashboardType string `tfsdk:"dashboard_type"`
		URL           string `tfsdk:"url"`
	}
	rows := make([]summary, 0, len(docs))
	for _, doc := range docs {
		rows = append(rows, summary{doc.ID, doc.Title, doc.Description, doc.DashboardType, doc.URL})
	}
	list, diags := types.ListValueFrom(ctx, dashboardSummaryType, rows)
	resp.Diagnostics.Append(diags...)
	data.Dashboards = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- atatus_dashboard_group ---------------------------------------------

// The list answers the raw group (dashboards as ids); the single-group read
// answers dashboards as objects. Both decode here.
type groupLookupDoc struct {
	ID          string        `json:"_id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	SpaceIDs    []string      `json:"spaceIds"`
	Dashboards  []groupMember `json:"dashboards"`
}

type groupMember struct{ ID string }

func (m *groupMember) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if strings.HasPrefix(s, "\"") {
		id, err := strconv.Unquote(s)
		m.ID = id
		return err
	}
	var obj struct {
		ID string `json:"_id"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	m.ID = obj.ID
	return nil
}

type dashboardGroupDataModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Description  types.String `tfsdk:"description"`
	DashboardIDs types.List   `tfsdk:"dashboard_ids"`
	SpaceIDs     types.List   `tfsdk:"space_ids"`
}

type DashboardGroupDataSource struct{ lookupBase }

func NewDashboardGroupDataSource() datasource.DataSource { return &DashboardGroupDataSource{} }

func (d *DashboardGroupDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dashboard_group"
}

func (d *DashboardGroupDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := idOrNameAttributes("dashboard group", "name", "Exact name of the dashboard group; it must match exactly one group.")
	attrs["description"] = schema.StringAttribute{Computed: true, Description: "Description of the group."}
	attrs["dashboard_ids"] = schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Ids of the group's dashboards, in the group's order."}
	attrs["space_ids"] = schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Ids of the Spaces the group is labelled with."}
	resp.Schema = schema.Schema{
		Description: "Looks up an existing dashboard group (a folder of dashboards) by id or exact name.",
		Attributes:  attrs,
	}
}

func (d *DashboardGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data dashboardGroupDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.api()
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Dashboard Group", err)
		return
	}
	var doc groupLookupDoc
	if isKnown(data.ID) && data.ID.ValueString() != "" {
		err = c.doJSON(ctx, "GET", "/v2/dashboard-groups/"+url.PathEscape(data.ID.ValueString()), nil, &doc)
	} else {
		var matches []groupLookupDoc
		matches, err = listAll[groupLookupDoc](ctx, c, "/v2/dashboard-groups", url.Values{"name": {data.Name.ValueString()}})
		if err == nil {
			doc, err = oneMatch("dashboard group", "name", data.Name.ValueString(), hintByID, matches)
		}
	}
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Dashboard Group", err)
		return
	}
	ids := make([]string, 0, len(doc.Dashboards))
	for _, m := range doc.Dashboards {
		ids = append(ids, m.ID)
	}
	data.ID = types.StringValue(doc.ID)
	data.Name = types.StringValue(doc.Name)
	data.Description = types.StringValue(doc.Description)
	data.DashboardIDs = stringList(ctx, ids, &resp.Diagnostics)
	data.SpaceIDs = stringList(ctx, doc.SpaceIDs, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- atatus_powerpack ---------------------------------------------------

type powerpackLookupDoc struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Tags        []string `json:"tags"`
	Version     int64    `json:"version"`
}

type powerpackDataModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Source      types.String `tfsdk:"source"`
	Tags        types.List   `tfsdk:"tags"`
	Version     types.Int64  `tfsdk:"version"`
}

type PowerpackDataSource struct{ lookupBase }

func NewPowerpackDataSource() datasource.DataSource { return &PowerpackDataSource{} }

func (d *PowerpackDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_powerpack"
}

func (d *PowerpackDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := idOrNameAttributes("powerpack", "name", "Exact name of the powerpack, yours or one from the Atatus catalog; it must match exactly one.")
	attrs["description"] = schema.StringAttribute{Computed: true, Description: "Description of the powerpack."}
	attrs["source"] = schema.StringAttribute{Computed: true, Description: "custom (made in your account) or preset (from the Atatus catalog)."}
	attrs["tags"] = schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "The powerpack's tags."}
	attrs["version"] = schema.Int64Attribute{Computed: true, Description: "Layout version of the powerpack."}
	resp.Schema = schema.Schema{
		Description: "Looks up a powerpack (a reusable group of widgets) by id or exact name.",
		Attributes:  attrs,
	}
}

func (d *PowerpackDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data powerpackDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.api()
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Powerpack", err)
		return
	}
	var doc powerpackLookupDoc
	if isKnown(data.ID) && data.ID.ValueString() != "" {
		err = c.doJSON(ctx, "GET", "/v2/powerpacks/"+url.PathEscape(data.ID.ValueString()), nil, &doc)
	} else {
		name := data.Name.ValueString()
		var all []powerpackLookupDoc
		all, err = listAll[powerpackLookupDoc](ctx, c, "/v2/powerpacks", url.Values{"q": {strings.ToLower(name)}})
		if err == nil {
			var matches []powerpackLookupDoc
			for _, p := range all {
				if p.Name == name || p.DisplayName == name {
					matches = append(matches, p)
				}
			}
			doc, err = oneMatch("powerpack", "name", name, hintByID, matches)
		}
	}
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Powerpack", err)
		return
	}
	data.ID = types.StringValue(doc.ID)
	data.Name = types.StringValue(doc.Name)
	data.Description = types.StringValue(doc.Description)
	data.Source = types.StringValue(doc.Source)
	data.Tags = stringList(ctx, doc.Tags, &resp.Diagnostics)
	data.Version = types.Int64Value(doc.Version)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- atatus_space and atatus_team ---------------------------------------

type namedLookupDoc struct {
	ID          string `json:"_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type namedDataModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

// namedDataSource looks an object up by exact name on a list route that
// takes ?name= (Spaces, teams).
type namedDataSource struct {
	lookupBase
	typeSuffix string
	kind       string
	listPath   string
}

func NewSpaceDataSource() datasource.DataSource {
	return &namedDataSource{typeSuffix: "_space", kind: "Space", listPath: "/v2/spaces"}
}

func NewTeamDataSource() datasource.DataSource {
	return &namedDataSource{typeSuffix: "_team", kind: "team", listPath: "/v2/teams"}
}

func (d *namedDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + d.typeSuffix
}

func (d *namedDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up an Atatus " + d.kind + " by exact name.",
		Attributes: map[string]schema.Attribute{
			"name":        schema.StringAttribute{Required: true, Description: "Exact name of the " + d.kind + ".", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"id":          schema.StringAttribute{Computed: true, Description: "Id of the " + d.kind + "."},
			"description": schema.StringAttribute{Computed: true, Description: "Description of the " + d.kind + "."},
		},
	}
}

func (d *namedDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data namedDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.api()
	if err != nil {
		lookupFailed(&resp.Diagnostics, d.kind, err)
		return
	}
	matches, err := listAll[namedLookupDoc](ctx, c, d.listPath, url.Values{"name": {data.Name.ValueString()}})
	var doc namedLookupDoc
	if err == nil {
		doc, err = oneMatch(d.kind, "name", data.Name.ValueString(), hintRename, matches)
	}
	if err != nil {
		lookupFailed(&resp.Diagnostics, d.kind, err)
		return
	}
	data.ID = types.StringValue(doc.ID)
	data.Description = types.StringValue(doc.Description)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- atatus_user --------------------------------------------------------

type userLookupDoc struct {
	ID        string `json:"_id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Email     string `json:"email"`
	Role      string `json:"role"`
}

type userDataModel struct {
	ID        types.String `tfsdk:"id"`
	Email     types.String `tfsdk:"email"`
	FirstName types.String `tfsdk:"first_name"`
	LastName  types.String `tfsdk:"last_name"`
	Role      types.String `tfsdk:"role"`
}

type UserDataSource struct{ lookupBase }

func NewUserDataSource() datasource.DataSource { return &UserDataSource{} }

func (d *UserDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *UserDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a member of the Atatus account by email.",
		Attributes: map[string]schema.Attribute{
			"email":      schema.StringAttribute{Required: true, Description: "Email of the member (case-insensitive).", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"id":         schema.StringAttribute{Computed: true, Description: "Id of the user."},
			"first_name": schema.StringAttribute{Computed: true},
			"last_name":  schema.StringAttribute{Computed: true},
			"role":       schema.StringAttribute{Computed: true, Description: "owner, admin or user."},
		},
	}
}

func (d *UserDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data userDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.api()
	if err != nil {
		lookupFailed(&resp.Diagnostics, "User", err)
		return
	}
	matches, err := listAll[userLookupDoc](ctx, c, "/v2/users", url.Values{"email": {data.Email.ValueString()}})
	var doc userLookupDoc
	if err == nil {
		doc, err = oneMatch("user", "email", data.Email.ValueString(), hintRename, matches)
	}
	if err != nil {
		lookupFailed(&resp.Diagnostics, "User", err)
		return
	}
	data.ID = types.StringValue(doc.ID)
	data.FirstName = types.StringValue(doc.FirstName)
	data.LastName = types.StringValue(doc.LastName)
	data.Role = types.StringValue(doc.Role)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- atatus_notification_channel and atatus_project ---------------------

type NotificationChannelDataSource struct{ lookupBase }

func NewNotificationChannelDataSource() datasource.DataSource {
	return &NotificationChannelDataSource{}
}

type channelDataModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
	Type types.String `tfsdk:"type"`
}

func (d *NotificationChannelDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_notification_channel"
}

func (d *NotificationChannelDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a notification channel by exact name, for a check's channel_ids.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Required: true, Description: "Exact name of the channel.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"type": schema.StringAttribute{Optional: true, Computed: true, Description: "Channel type (for example EMAIL or SLACK), to pick between channels that share a name. Case-insensitive."},
			"id":   schema.StringAttribute{Computed: true, Description: "Id of the channel."},
		},
	}
}

func (d *NotificationChannelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data channelDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.api()
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Notification Channel", err)
		return
	}
	channels, err := c.notificationChannels(ctx)
	var ch NotificationChannel
	if err == nil {
		var matches []NotificationChannel
		for _, candidate := range channels {
			if candidate.Name != data.Name.ValueString() {
				continue
			}
			if isKnown(data.Type) && !strings.EqualFold(candidate.Type, data.Type.ValueString()) {
				continue
			}
			matches = append(matches, candidate)
		}
		ch, err = oneMatch("notification channel", "name", data.Name.ValueString(), hintByType, matches)
	}
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Notification Channel", err)
		return
	}
	data.ID = types.StringValue(ch.ID)
	data.Type = types.StringValue(ch.Type)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

type ProjectDataSource struct{ lookupBase }

func NewProjectDataSource() datasource.DataSource { return &ProjectDataSource{} }

type projectDataModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Type    types.String `tfsdk:"type"`
	SubType types.String `tfsdk:"sub_type"`
}

func (d *ProjectDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *ProjectDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a monitored project (APM, Browser, ...) by exact name.",
		Attributes: map[string]schema.Attribute{
			"name":     schema.StringAttribute{Required: true, Description: "Exact name of the project.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"type":     schema.StringAttribute{Optional: true, Computed: true, Description: "Project type (for example APM or Browser), to pick between projects that share a name. Case-insensitive."},
			"id":       schema.StringAttribute{Computed: true, Description: "Id of the project."},
			"sub_type": schema.StringAttribute{Computed: true, Description: "The project's agent or platform (for example Node.js)."},
		},
	}
}

func (d *ProjectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data projectDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.api()
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Project", err)
		return
	}
	var out struct {
		Data []struct {
			ID      string `json:"_id"`
			Name    string `json:"name"`
			Type    string `json:"type"`
			SubType string `json:"subType"`
		} `json:"data"`
	}
	err = c.doJSON(ctx, "GET", "/v2/projects", nil, &out)
	if err == nil {
		type project = struct {
			ID, Name, Type, SubType string
		}
		var matches []project
		for _, p := range out.Data {
			if p.Name != data.Name.ValueString() {
				continue
			}
			if isKnown(data.Type) && !strings.EqualFold(p.Type, data.Type.ValueString()) {
				continue
			}
			matches = append(matches, project{p.ID, p.Name, p.Type, p.SubType})
		}
		var p project
		p, err = oneMatch("project", "name", data.Name.ValueString(), hintByType, matches)
		if err == nil {
			data.ID = types.StringValue(p.ID)
			data.Type = types.StringValue(p.Type)
			data.SubType = types.StringValue(p.SubType)
		}
	}
	if err != nil {
		lookupFailed(&resp.Diagnostics, "Project", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
