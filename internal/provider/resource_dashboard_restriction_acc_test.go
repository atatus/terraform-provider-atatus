package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const restrictionAddr = "atatus_dashboard_restriction.test"

const (
	teamSRE  = "team:7e0000000000000000000001"
	userBob  = "user:0b0000000000000000000002"
	userCara = "user:0c0000000000000000000003"
)

// Two boards, and a restriction on the one named by `on` ("a" or "b").
func testAccRestrictionConfig(f *fakeDashboardAPI, on string, editors ...string) string {
	list := ""
	for _, e := range editors {
		list += fmt.Sprintf("%q, ", e)
	}
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard_json" "a" {
  dashboard_json = jsonencode({ title = "Restricted A", widgets = [{ type = "note", title = "n", definition = { content = "a" } }] })
}
resource "atatus_dashboard_json" "b" {
  dashboard_json = jsonencode({ title = "Restricted B", widgets = [{ type = "note", title = "n", definition = { content = "b" } }] })
}
resource "atatus_dashboard_restriction" "test" {
  dashboard_id = atatus_dashboard_json.%s.id
  editors      = [%s]
}
`, on, list)
}

func boardRestriction(f *fakeDashboardAPI, id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries, _ := f.boards[id]["restrictedTo"].([]any)
	return len(entries)
}

func TestAccDashboardRestriction_lifecycle(t *testing.T) {
	f := newFakeDashboardAPI(t)
	var boardA, boardB string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			for _, id := range []string{boardA, boardB} {
				if _, still := f.boards[id]; still {
					return fmt.Errorf("board %s still exists", id)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: testAccRestrictionConfig(f, "a", teamSRE, userBob),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(restrictionAddr, "editors.#", "2"),
					resource.TestCheckTypeSetElemAttr(restrictionAddr, "editors.*", teamSRE),
					resource.TestCheckResourceAttrPair(restrictionAddr, "id", "atatus_dashboard_json.a", "id"),
					func(s *terraform.State) error {
						boardA = s.RootModule().Resources["atatus_dashboard_json.a"].Primary.ID
						boardB = s.RootModule().Resources["atatus_dashboard_json.b"].Primary.ID
						if n := boardRestriction(f, boardA); n != 2 {
							return fmt.Errorf("board A holds %d restriction entries, want 2", n)
						}
						return nil
					},
				),
			},
			{
				// The board stores restrictedTo, and atatus_dashboard_json does not see it as drift.
				Config:           testAccRestrictionConfig(f, "a", teamSRE, userBob),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ResourceName: restrictionAddr, ImportState: true, ImportStateVerify: true},
			{
				Config: testAccRestrictionConfig(f, "a", userCara),
				Check:  resource.TestCheckResourceAttr(restrictionAddr, "editors.#", "1"),
			},
			{
				// Lifted in Atatus: the plan puts it back.
				PreConfig: func() { f.setRestrictionOutOfBand(boardA, []any{}) },
				Config:    testAccRestrictionConfig(f, "a", userCara),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(restrictionAddr, plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					if n := boardRestriction(f, boardA); n != 1 {
						return fmt.Errorf("board A holds %d restriction entries after apply, want 1", n)
					}
					return nil
				},
			},
			{
				// Moving it to another board lifts it from the first.
				Config: testAccRestrictionConfig(f, "b", userCara),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(restrictionAddr, plancheck.ResourceActionReplace)},
				},
				Check: func(*terraform.State) error {
					if a, b := boardRestriction(f, boardA), boardRestriction(f, boardB); a != 0 || b != 1 {
						return fmt.Errorf("restriction entries: board A %d, board B %d; want 0 and 1", a, b)
					}
					return nil
				},
			},
		},
	})
}

func TestAccDashboardRestriction_validation(t *testing.T) {
	f := newFakeDashboardAPI(t)
	for name, tc := range map[string]struct {
		editors []string
		want    string
	}{
		"bad principal": {[]string{"role:owner"}, `must be "team:<team id>" or`},
		"empty":         {nil, `editors set must contain at least 1 elements`},
	} {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProviderFactories,
				Steps: []resource.TestStep{{
					Config:      testAccRestrictionConfig(f, "a", tc.editors...),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(regexp.QuoteMeta(tc.want)),
				}},
			})
		})
	}
}

func TestAccDashboardRestriction_importRefusesBadId(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{{
			Config:        testAccRestrictionConfig(f, "a", userBob),
			ResourceName:  restrictionAddr,
			ImportState:   true,
			ImportStateId: "not-an-id",
			ExpectError:   regexp.MustCompile(`Not A Dashboard Id`),
		}},
	})
}
