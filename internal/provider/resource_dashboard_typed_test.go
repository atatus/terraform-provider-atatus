package provider

import (
	"context"
	"math/big"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// An imported dashboard's numbers are float64s (3.1 is 3.100000000000000088...), while the
// configuration Terraform generates from them holds the decimal 3.1: the raw plan and state differ
// though the dashboard does not. ModifyPlan must then plan nothing — keep url and version — rather
// than an update.
func TestTypedDashboard_modifyPlanIgnoresNumberPrecision(t *testing.T) {
	ctx := context.Background()
	r := NewTypedDashboardResource().(*TypedDashboardResource)
	sresp := &resource.SchemaResponse{}
	r.Schema(ctx, resource.SchemaRequest{}, sresp)
	sch := sresp.Schema
	objType := sch.Type().(basetypes.ObjectType)
	m, _ := loadDashboardModel()

	attrs := map[string]attr.Value{}
	for name, at := range objType.AttrTypes {
		attrs[name] = nullOf(at)
	}
	note := m.Widget.Variants["note"]
	layoutType := note.prop("layout").Node.attrType().(basetypes.ObjectType)
	layout := types.ObjectValueMust(layoutType.AttrTypes, map[string]attr.Value{
		"x": types.Float64Value(6.2), "y": types.Float64Value(0.3), "width": types.Float64Value(5.8), "height": types.Float64Value(3.1)})
	noteAttrs := nullAttrs(note)
	noteAttrs["layout"] = layout
	union := map[string]attr.Value{}
	for name, v := range m.Widget.Variants {
		union[name] = nullOf(v.attrType())
	}
	union["note"] = types.ObjectValueMust(note.objectAttrTypes(), noteAttrs)
	widget := types.ObjectValueMust(m.Widget.attrType().(basetypes.ObjectType).AttrTypes, union)
	attrs["title"] = types.StringValue("Fractions")
	attrs["widgets"] = types.ListValueMust(m.Widget.attrType(), []attr.Value{widget})
	attrs["id"] = types.StringValue("6b0000000000000000000001")
	attrs["url"] = types.StringValue("https://app.example/d/1")
	attrs["version"] = types.Int64Value(4)
	stateRaw, err := types.ObjectValueMust(objType.AttrTypes, attrs).ToTerraformValue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The configuration's numbers are exact decimals; the framework has marked url and version unknown.
	planRaw, err := tftypes.Transform(stateRaw, func(p *tftypes.AttributePath, v tftypes.Value) (tftypes.Value, error) {
		if len(p.Steps()) == 1 {
			if name, ok := p.Steps()[0].(tftypes.AttributeName); ok && (name == "url" || name == "version") {
				return tftypes.NewValue(v.Type(), tftypes.UnknownValue), nil
			}
		}
		if v.Type().Is(tftypes.Number) && v.IsKnown() && !v.IsNull() {
			var f big.Float
			if err := v.As(&f); err != nil {
				return v, err
			}
			dec, _, err := big.ParseFloat(f.Text('g', 15), 10, 512, big.ToNearestEven)
			if err != nil {
				return v, err
			}
			return tftypes.NewValue(tftypes.Number, dec), nil
		}
		return v, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if planRaw.Equal(stateRaw) {
		t.Fatal("test setup: the plan's numbers should differ from the state's in precision")
	}

	plan := tfsdk.Plan{Schema: sch, Raw: planRaw}
	req := resource.ModifyPlanRequest{Plan: plan, State: tfsdk.State{Schema: sch, Raw: stateRaw}, Config: tfsdk.Config{Schema: sch, Raw: planRaw}}
	resp := &resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var url types.String
	var version types.Int64
	resp.Plan.GetAttribute(ctx, path.Root("url"), &url)
	resp.Plan.GetAttribute(ctx, path.Root("version"), &version)
	if url.IsUnknown() || version.IsUnknown() || version.ValueInt64() != 4 {
		t.Fatalf("url/version should keep their state values; got url=%v version=%v", url, version)
	}
}
