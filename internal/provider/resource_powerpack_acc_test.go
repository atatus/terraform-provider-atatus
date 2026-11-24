package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const packAddr = "atatus_powerpack.test"

// testAccPackConfig is a pack named name whose group holds the given widgets
// (HCL objects inside jsonencode), plus extra attribute lines.
func testAccPackConfig(f *fakeDashboardAPI, name, widgets, extra string) string {
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_powerpack" "test" {
  name = %q%s
  group_widget_json = jsonencode({
    title   = "Service health"
    widgets = [%s]
  })
}
`, name, extra, widgets)
}

const (
	packNote    = `{ type = "note", title = "About", definition = { content = "RED metrics" }, layout = { x = 0, y = 0, width = 4, height = 2 } }`
	packNoteTwo = `{ type = "note", title = "Runbook", definition = { content = "See wiki" }, layout = { x = 4, y = 0, width = 4, height = 2 } }`
)

func packID(s *terraform.State) string { return s.RootModule().Resources[packAddr].Primary.ID }

func TestAccPowerpack_lifecycle(t *testing.T) {
	f := newFakeDashboardAPI(t)
	start := f.packs.live()
	extra := `
  description = "RED metrics"
  tags        = ["apm"]
  template_variables = [{ name = "service", prefix = "service", defaults = ["checkout"] }]`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if n := f.packs.live(); n != start {
				return fmt.Errorf("%d powerpacks on the server after destroy, want %d", n, start)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: testAccPackConfig(f, "Service health", packNote, extra),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(packAddr, "id"),
					resource.TestCheckResourceAttr(packAddr, "version", "1"),
					resource.TestCheckResourceAttr(packAddr, "revision", "0"),
					resource.TestCheckResourceAttr(packAddr, "editors.#", "0"),
					resource.TestCheckResourceAttr(packAddr, "template_variables.0.available_values.#", "0"),
				),
			},
			{
				// The server gave the child a powerpackKey; the configuration still matches.
				Config:           testAccPackConfig(f, "Service health", packNote, extra),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				// Import reads the server's group, so it is compared on its own (below).
				ResourceName: packAddr, ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"group_widget_json"},
			},
			{
				// A metadata change: the revision moves, the layout version does not,
				// and stays known in the plan (dashboards referencing it do not change).
				Config: testAccPackConfig(f, "Service health (renamed)", packNote, extra),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectKnownValue(packAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(packAddr, "name", "Service health (renamed)"),
					resource.TestCheckResourceAttr(packAddr, "version", "1"),
					resource.TestCheckResourceAttr(packAddr, "revision", "1"),
				),
			},
			{
				// A layout change moves the version (and syncs the dashboards using it).
				Config: testAccPackConfig(f, "Service health (renamed)", packNote+", "+packNoteTwo, extra),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectUnknownValue(packAddr, tfjsonpath.New("version")),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(packAddr, "version", "2"),
					resource.TestCheckResourceAttr(packAddr, "revision", "2"),
				),
			},
			{
				Config:           testAccPackConfig(f, "Service health (renamed)", packNote+", "+packNoteTwo, extra),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

// Edits made in Atatus show as drift, and applying puts the configuration back.
func TestAccPowerpack_driftAndConflict(t *testing.T) {
	f := newFakeDashboardAPI(t)
	cfg := testAccPackConfig(f, "Drift", packNote, "")
	var id string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check:  func(s *terraform.State) error { id = packID(s); return nil },
			},
			{
				// A layout edit: the version moves, so the server's group is read back.
				PreConfig: func() {
					f.packs.editOutOfBand(id, true, func(p map[string]any) {
						gw := p["groupWidget"].(map[string]any)
						gw["widgets"] = append(gw["widgets"].([]any), map[string]any{
							"type": "note", "title": "Added in the UI", "definition": map[string]any{"content": "x", "powerpackKey": "1"},
							"layout": map[string]any{"x": float64(0), "y": float64(2), "width": float64(4), "height": float64(2)},
						})
					})
				},
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(packAddr, plancheck.ResourceActionUpdate)},
				},
				Check: func(s *terraform.State) error {
					f.packs.mu.Lock()
					defer f.packs.mu.Unlock()
					if n := len(f.packs.packs[id]["groupWidget"].(map[string]any)["widgets"].([]any)); n != 1 {
						return fmt.Errorf("the pack has %d widgets after apply, want the configured 1", n)
					}
					return nil
				},
			},
			{
				// A metadata edit (no new version) shows on its attribute.
				PreConfig: func() {
					f.packs.editOutOfBand(id, false, func(p map[string]any) { p["name"] = "Renamed in the UI" })
				},
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(packAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(packAddr, "name", "Drift"),
			},
			{
				// An edit landing between refresh and apply is refused, not overwritten.
				PreConfig:   func() { f.packs.concurrentEditOnNextPatch = true },
				Config:      testAccPackConfig(f, "Drift 2", packNote, ""),
				ExpectError: regexp.MustCompile(`Powerpack Changed Outside Terraform`),
			},
		},
	})
}

// A pack deleted in Atatus is planned for creation again.
func TestAccPowerpack_deletedOutsideTerraform(t *testing.T) {
	f := newFakeDashboardAPI(t)
	cfg := testAccPackConfig(f, "Gone", packNote, "")
	var id string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg, Check: func(s *terraform.State) error { id = packID(s); return nil }},
			{
				PreConfig: func() { f.packs.editOutOfBand(id, false, func(p map[string]any) { p["deleted"] = true }) },
				Config:    cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(packAddr, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestAccPowerpack_importRefusesCatalogPacks(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        testAccPackConfig(f, "Hosts", packNote, ""),
				ResourceName:  packAddr,
				ImportState:   true,
				ImportStateId: "hosts-overview",
				ExpectError:   regexp.MustCompile(`Not A Custom Powerpack Id`),
			},
		},
	})
}

func TestAccPowerpack_validation(t *testing.T) {
	f := newFakeDashboardAPI(t)
	cases := map[string]struct{ widgets, extra, want string }{
		"no widgets":   {"", "", `a powerpack needs at least one widget`},
		"nested group": {`{ type = "group", layout = { x = 0, y = 0, width = 4, height = 2 } }`, "", `cannot contain groups`},
		"off the grid": {`{ type = "note", layout = { x = 10, y = 0, width = 4, height = 2 } }`, "", `must fit the 12-column grid`},
		"no layout":    {`{ type = "note" }`, "", `needs numeric x, y, width and height`},
		"repeated key": {packNote + `, { type = "note", definition = { powerpackKey = "k" }, layout = { x = 0, y = 2, width = 4, height = 2 } }, { type = "note", definition = { powerpackKey = "k" }, layout = { x = 4, y = 2, width = 4, height = 2 } }`, "", `is used by another widget`},
		"too many tags": {packNote, `
  tags = ["a", "b", "c", "d", "e", "f", "g", "h", "i"]`, `at most 8`},
		"bad editor": {packNote, `
  editors = ["ada"]`, `must be a user id`},
		"padded name": {packNote, "", `must not be empty or start or end with a space`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			packName := "Valid"
			if name == "padded name" {
				packName = " Padded "
			}
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProviderFactories,
				Steps: []resource.TestStep{{
					Config:      testAccPackConfig(f, packName, tc.widgets, tc.extra),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(regexp.QuoteMeta(tc.want)),
				}},
			})
		})
	}
}

func TestGroupWidgetProblems_unknownKey(t *testing.T) {
	doc, err := decodeJSONObject(`{"title":"x","layoutType":"ordered","widgets":[{"type":"note","layout":{"x":0,"y":0,"width":1,"height":1}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	problems := groupWidgetProblems(doc)
	if len(problems) != 1 || !strings.Contains(problems[0], "layoutType") {
		t.Fatalf("problems = %q, want one about layoutType", problems)
	}
}

// A dashboard's linked powerpack instance: Atatus rewrites its children and
// linked version when the pack changes, which is not drift on the dashboard.
// The link itself and a plain group's children still are.
func TestDashboardJSON_powerpackInstanceIgnoresPackContent(t *testing.T) {
	instance := func(version int, children, packID string) string {
		return fmt.Sprintf(`{"title":"B","widgets":[{"type":"group","title":"Health","definition":{"powerpack":{"id":%q,"version":%d}},"widgets":[%s],"layout":{"x":0,"y":0,"width":12,"height":3}}]}`,
			packID, version, children)
	}
	one := `{"type":"note","definition":{"content":"a"}}`
	two := one + `,{"type":"note","definition":{"content":"b"}}`
	pack := "c00000000000000000000001"
	if !dashboardJSONSemanticallyEqual(instance(1, one, pack), instance(2, two, pack)) {
		t.Error("a pack sync (new children, new linked version) reads as a dashboard change")
	}
	if dashboardJSONSemanticallyEqual(instance(1, one, pack), instance(1, one, "c00000000000000000000002")) {
		t.Error("linking another pack does not read as a change")
	}
	plain := func(children string) string {
		return fmt.Sprintf(`{"title":"B","widgets":[{"type":"group","title":"G","widgets":[%s]}]}`, children)
	}
	if dashboardJSONSemanticallyEqual(plain(one), plain(two)) {
		t.Error("a plain group's children are no longer compared")
	}
}

// A dashboard_json that references a computed value (here the pack's version)
// is unknown until apply. Its url and version must be unknown from the first
// plan, or Terraform rejects the final plan as inconsistent.
func TestAccPowerpack_dashboardReferencingVersion(t *testing.T) {
	f := newFakeDashboardAPI(t)
	cfg := func(widgets string) string {
		return testAccPackConfig(f, "Linked", widgets, "") + `
resource "atatus_dashboard_json" "linked" {
  dashboard_json = jsonencode({
    title       = "Uses the pack"
    description = "pack version ${atatus_powerpack.test.version}"
    widgets     = [{ type = "note", title = "n", definition = { content = "x" } }]
  })
}
`
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg(packNote)},
			{
				Config: cfg(packNote + ", " + packNoteTwo),
				Check: resource.TestCheckResourceAttrWith("atatus_dashboard_json.linked", "dashboard_json", func(v string) error {
					if !strings.Contains(v, "pack version 2") {
						return fmt.Errorf("dashboard_json = %s, want the pack's new version 2", v)
					}
					return nil
				}),
			},
			{Config: cfg(packNote + ", " + packNoteTwo), PlanOnly: true},
		},
	})
}
