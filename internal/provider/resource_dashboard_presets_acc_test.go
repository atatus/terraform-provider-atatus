package provider

import (
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Template variable presets (`templateVariablePresets`): saved variable
// selections, each naming variables of the same board.

// presetsOf returns the stored presets as name → entry name → values.
func presetsOf(f *fakeDashboardAPI) map[string]map[string][]any {
	out := map[string]map[string][]any{}
	presets, _ := f.board(f.onlyBoardID())["templateVariablePresets"].([]any)
	for _, p := range presets {
		pm, _ := p.(map[string]any)
		name, _ := pm["name"].(string)
		entries := map[string][]any{}
		list, _ := pm["templateVariables"].([]any)
		for _, e := range list {
			em, _ := e.(map[string]any)
			n, _ := em["name"].(string)
			values, _ := em["values"].([]any)
			entries[n] = values
		}
		out[name] = entries
	}
	return out
}

func wantPresets(f *fakeDashboardAPI, want map[string]map[string][]any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := presetsOf(f); !reflect.DeepEqual(got, want) {
			return fmt.Errorf("stored presets = %v, want %v", got, want)
		}
		return nil
	}
}

func testAccPresetsJSONConfig(f *fakeDashboardAPI, presets string) string {
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard_json" "test" {
  dashboard_json = jsonencode({
    title = "Presets"
    templateVariables = [
      { name = "env", prefix = "env", default = ["prod"] },
      { name = "service", prefix = "service" },
    ]%s
    widgets = [{ type = "note", text = "n" }]
  })
}
`, presets)
}

const jsonPresets = `
    templateVariablePresets = [
      { name = "Staging", templateVariables = [{ name = "env", values = ["staging"] }] },
      { name = "Checkout prod", templateVariables = [{ name = "env", values = ["prod"] }, { name = "service", values = ["checkout", "cart"] }] },
      { name = "All defaults", templateVariables = [] },
    ]`

func TestAccDashboardJSON_templateVariablePresets(t *testing.T) {
	f := newFakeDashboardAPI(t)
	all := map[string]map[string][]any{
		"Staging":       {"env": {"staging"}},
		"Checkout prod": {"env": {"prod"}, "service": {"checkout", "cart"}},
		"All defaults":  {},
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			// 1. Round-trip: stored as written, no diff after apply.
			{
				Config: testAccPresetsJSONConfig(f, jsonPresets),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: wantPresets(f, all),
			},
			// 2. A preset changed in the UI is drift.
			{
				PreConfig: func() {
					f.editOutOfBand(f.onlyBoardID(), func(b map[string]any) {
						presets := b["templateVariablePresets"].([]any)
						presets[0].(map[string]any)["templateVariables"] = []any{map[string]any{"name": "env", "values": []any{"qa"}}}
					})
				},
				Config:             testAccPresetsJSONConfig(f, jsonPresets),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// 3. Apply puts the configured presets back.
			{
				Config: testAccPresetsJSONConfig(f, jsonPresets),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dashAddr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: wantPresets(f, all),
			},
			// 4. Left out of the document: the whole-document PUT clears them.
			{
				Config: testAccPresetsJSONConfig(f, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: wantPresets(f, map[string]map[string][]any{}),
			},
		},
	})
}

func TestAccDashboardJSON_templateVariablePresetUnknownVariable(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{{
			Config: testAccPresetsJSONConfig(f, `
    templateVariablePresets = [{ name = "Bad", templateVariables = [{ name = "region", values = ["eu"] }] }]`),
			ExpectError: regexp.MustCompile(`uses 'region', which is not a variable`),
		}},
	})
}

func testAccPresetsTypedConfig(f *fakeDashboardAPI, variable, presets string) string {
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard" "test" {
  title = "Typed presets"

  template_variables = [
    { name = %q, prefix = "env", default = ["prod"] },
    { name = "service", prefix = "service" },
  ]
%s
  widgets = [{ note = { definition = { content = "n" } } }]
}
`, variable, presets)
}

func typedPresets(variable string) string {
	return fmt.Sprintf(`
  template_variable_presets = [
    { name = "Staging", template_variables = [{ name = %q, values = ["staging"] }] },
    { name = "Checkout", template_variables = [{ name = %q, values = ["prod"] }, { name = "service", values = ["checkout"] }] },
  ]
`, variable, variable)
}

func TestAccTypedDashboard_templateVariablePresets(t *testing.T) {
	f := newFakeDashboardAPI(t)
	named := func(variable string) map[string]map[string][]any {
		return map[string]map[string][]any{
			"Staging":  {variable: {"staging"}},
			"Checkout": {variable: {"prod"}, "service": {"checkout"}},
		}
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			// 1. Round-trip.
			{
				Config: testAccPresetsTypedConfig(f, "env", typedPresets("env")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: wantPresets(f, named("env")),
			},
			// 2. Import reads them back as configured.
			{
				ResourceName: typedAddr, ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"version", "url", "layout_type"},
			},
			// 3. A rename in the app (as the React variable panel does it: the
			// variable and every preset entry naming it) is drift against a
			// configuration that still has the old name.
			{
				PreConfig: func() {
					f.editOutOfBand(f.onlyBoardID(), func(b map[string]any) {
						b["templateVariables"].([]any)[0].(map[string]any)["name"] = "stage"
						for _, p := range b["templateVariablePresets"].([]any) {
							for _, e := range p.(map[string]any)["templateVariables"].([]any) {
								if em := e.(map[string]any); em["name"] == "env" {
									em["name"] = "stage"
								}
							}
						}
					})
				},
				Config:             testAccPresetsTypedConfig(f, "env", typedPresets("env")),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// 4. A configuration that follows the rename (the entries renamed
			// with the variable) matches the app's board: nothing to change.
			{
				Config: testAccPresetsTypedConfig(f, "stage", typedPresets("stage")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: wantPresets(f, named("stage")),
			},
			// 5. A rename made in Terraform applies in one update and settles.
			{
				Config: testAccPresetsTypedConfig(f, "environment", typedPresets("environment")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(typedAddr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: wantPresets(f, named("environment")),
			},
			// 6. Removed from the configuration: cleared.
			{
				Config: testAccPresetsTypedConfig(f, "environment", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: wantPresets(f, map[string]map[string][]any{}),
			},
		},
	})
}
