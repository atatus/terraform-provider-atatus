package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// A topology map's `definition.filters` (catalogue 1.7.0): a filter-bar string
// the service map narrows on (env:, service:, type:, subtype:, bare words).

func testAccTopologyConfig(f *fakeDashboardAPI, filters string) string {
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard" "test" {
  title = "Topology"
  widgets = [{
    topology_map = {
      title      = "Checkout dependencies"
      definition = { data_source = "service_map", env = "production", filters = %q }
    }
  }]
}
`, filters)
}

func topologyFiltersStored(f *fakeDashboardAPI, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		widgets, _ := f.board(f.onlyBoardID())["widgets"].([]any)
		if len(widgets) != 1 {
			return fmt.Errorf("stored widgets = %d, want 1", len(widgets))
		}
		def, _ := widgets[0].(map[string]any)["definition"].(map[string]any)
		if got := def["filters"]; got != want {
			return fmt.Errorf("stored definition.filters = %v, want %q", got, want)
		}
		return nil
	}
}

func TestAccTypedDashboard_topologyFilters(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccTopologyConfig(f, "env:production type:db checkout"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: topologyFiltersStored(f, "env:production type:db checkout"),
			},
			{
				ResourceName: typedAddr, ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"version", "url", "layout_type"},
			},
			{
				Config: testAccTopologyConfig(f, "service:*checkout* -type:external"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(typedAddr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: topologyFiltersStored(f, "service:*checkout* -type:external"),
			},
		},
	})
}
