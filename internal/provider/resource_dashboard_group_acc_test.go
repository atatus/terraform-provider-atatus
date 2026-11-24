package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const groupAddr = "atatus_dashboard_group.test"

func testAccGroupConfig(f *fakeDashboardAPI, body string) string {
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard_group" "test" {%s
}
`, body)
}

func TestAccDashboardGroup_lifecycle(t *testing.T) {
	f := newFakeDashboardAPI(t)
	start := f.groups.count()
	created := `
  name          = "Payments"
  dashboard_ids = ["b00000000000000000000001", "b00000000000000000000002"]`
	updated := `
  name          = "Payments (renamed)"
  description   = "Boards the payments team owns"
  order         = 3
  dashboard_ids = ["b00000000000000000000002", "b00000000000000000000001", "b00000000000000000000003"]
  space_ids     = ["5p0000000000000000000001"]`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if n := f.groups.count(); n != start {
				return fmt.Errorf("%d groups on the server after destroy, want %d", n, start)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: testAccGroupConfig(f, created),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(groupAddr, "id"),
					resource.TestCheckResourceAttr(groupAddr, "version", "0"),
					resource.TestCheckResourceAttr(groupAddr, "description", ""),
					resource.TestCheckResourceAttr(groupAddr, "dashboard_ids.1", "b00000000000000000000002"),
				),
			},
			{
				Config:           testAccGroupConfig(f, created),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ResourceName: groupAddr, ImportState: true, ImportStateVerify: true},
			{
				Config: testAccGroupConfig(f, updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(groupAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(groupAddr, "version", "1"),
					resource.TestCheckResourceAttr(groupAddr, "dashboard_ids.0", "b00000000000000000000002"),
					resource.TestCheckResourceAttr(groupAddr, "space_ids.0", "5p0000000000000000000001"),
				),
			},
			{
				// Removing settings from the configuration puts them back to
				// their defaults on the server.
				Config: testAccGroupConfig(f, created),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(groupAddr, "description", ""),
					resource.TestCheckResourceAttr(groupAddr, "order", "0"),
					resource.TestCheckResourceAttr(groupAddr, "space_ids.#", "0"),
					resource.TestCheckResourceAttr(groupAddr, "dashboard_ids.#", "2"),
				),
			},
			{ResourceName: groupAddr, ImportState: true, ImportStateVerify: true},
		},
	})
}

// A change made in Atatus shows as drift, and applying puts the configuration back.
func TestAccDashboardGroup_driftAndConflict(t *testing.T) {
	f := newFakeDashboardAPI(t)
	cfg := testAccGroupConfig(f, `
  name = "Drift"`)
	var id string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[groupAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					f.groups.editOutOfBand(id, func(g map[string]any) { g["name"] = "Renamed in the UI" })
				},
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(groupAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(groupAddr, "name", "Drift"),
			},
			{
				// An edit landing between refresh and apply is refused, not overwritten.
				PreConfig: func() { f.groups.concurrentEditOnNextPut = true },
				Config: testAccGroupConfig(f, `
  name = "Drift 2"`),
				ExpectError: regexp.MustCompile(`Dashboard Group Changed Outside Terraform`),
			},
			{
				// A group deleted between refresh and apply is reported as deleted, not as changed.
				PreConfig: func() { f.groups.deleteOnNextPut = true },
				Config: testAccGroupConfig(f, `
  name = "Drift 3"`),
				ExpectError: regexp.MustCompile(`Dashboard Group Deleted Outside Terraform`),
			},
		},
	})
}

func TestAccDashboardGroup_rejectsRepeatedDashboards(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{{
			Config: testAccGroupConfig(f, `
  name          = "Twice"
  dashboard_ids = ["b00000000000000000000001", "b00000000000000000000001"]`),
			ExpectError: regexp.MustCompile(`Duplicate List Value`),
		}},
	})
}
