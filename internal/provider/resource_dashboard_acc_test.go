package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// Acceptance tests run real terraform (TF_ACC=1) against fakeDashboardAPI;
// they never reach the network.

const dashAddr = "atatus_dashboard_json.test"

var testAccProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"atatus": providerserver.NewProtocol6WithError(New("test")()),
}

func testAccPreCheck(t *testing.T) {
	t.Setenv("CHECKPOINT_DISABLE", "1")
}

// The endpoint is written with a trailing slash on purpose: the provider trims it.
func testAccProviderBlock(f *fakeDashboardAPI) string {
	return fmt.Sprintf(`
provider "atatus" {
  api_key  = %q
  endpoint = "%s/"
}
`, fakeAPIKey, f.URL())
}

func testAccDashboardConfig(f *fakeDashboardAPI, title string) string {
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard_json" "test" {
  dashboard_json = jsonencode({
    title       = %q
    description = "Managed by Terraform"
    widgets = [
      {
        type       = "timeseries"
        title      = "Latency"
        requests   = [{ queries = [{ dataSource = "metrics", query = "avg:latency" }] }]
        definition = { threshold = 0.1 }
        layout     = { x = 0, y = 0, width = 6, height = 3 }
      },
      {
        type   = "note"
        text   = "Runbook"
        layout = { x = 6, y = 0, width = 6, height = 3 }
      },
    ]
  })
}
`, title)
}

func TestAccDashboard_lifecycle(t *testing.T) {
	f := newFakeDashboardAPI(t)
	sameID := statecheck.CompareValue(compare.ValuesSame())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if n := f.boardCount(); n != 0 {
				return fmt.Errorf("%d dashboards left on the server after destroy", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			// 1. Create; the post-apply plan must be empty although the server
			//    added layoutType, widget defaults, audit fields and _ids.
			{
				Config: testAccDashboardConfig(f, "API Health"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dashAddr, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					PostApplyPreRefresh:  []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(dashAddr, tfjsonpath.New("id")),
					statecheck.ExpectKnownValue(dashAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(0)),
					statecheck.ExpectKnownValue(dashAddr, tfjsonpath.New("url"), knownvalue.StringRegexp(regexp.MustCompile(`^http://127\.0\.0\.1:\d+/dashboards/[0-9a-f]{24}$`))),
				},
			},
			// 2. Title change → in-place update, same id, new version.
			{
				Config: testAccDashboardConfig(f, "API Health v2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dashAddr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(dashAddr, tfjsonpath.New("id")),
					statecheck.ExpectKnownValue(dashAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
				},
				Check: func(*terraform.State) error {
					if got := f.board(f.onlyBoardID())["title"]; got != "API Health v2" {
						return fmt.Errorf("server title = %v", got)
					}
					return nil
				},
			},
			// 3. Import. dashboard_json is ignored by the verify only because an
			//    import has no prior document to prune server defaults against:
			//    the imported document keeps every server key (checked below),
			//    so it is semantically the board but not the config's text.
			{
				ResourceName:            dashAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"dashboard_json"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("expected 1 imported instance, got %d", len(states))
					}
					doc := states[0].Attributes["dashboard_json"]
					for _, want := range []string{`"title":"API Health v2"`, `"layoutType":"ordered"`, `"responseFormat":"scalar"`, `"threshold":0.1`} {
						if !strings.Contains(doc, want) {
							return fmt.Errorf("imported dashboard_json lacks %s: %s", want, doc)
						}
					}
					for _, unwanted := range []string{`"_id"`, `"__v"`, `"url"`, `"accountId"`, `"createdAt"`} {
						if strings.Contains(doc, unwanted) {
							return fmt.Errorf("imported dashboard_json kept server-managed key %s: %s", unwanted, doc)
						}
					}
					return nil
				},
			},
			// 4. Edit in the UI → plan shows drift.
			{
				PreConfig: func() {
					f.editOutOfBand(f.onlyBoardID(), func(b map[string]any) { b["title"] = "Edited in the UI" })
				},
				Config:             testAccDashboardConfig(f, "API Health v2"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// 5. Apply restores the Terraform version, in place.
			{
				Config: testAccDashboardConfig(f, "API Health v2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dashAddr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{sameID.AddStateValue(dashAddr, tfjsonpath.New("id"))},
				Check: func(*terraform.State) error {
					if got := f.board(f.onlyBoardID())["title"]; got != "API Health v2" {
						return fmt.Errorf("apply did not restore the title, server has %v", got)
					}
					return nil
				},
			},
			// 6. Deleted in the UI → plan recreates.
			{
				PreConfig:          func() { f.deleteOutOfBand(f.onlyBoardID()) },
				Config:             testAccDashboardConfig(f, "API Health v2"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: testAccDashboardConfig(f, "API Health v2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dashAddr, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})

	if f.putsNoVersion != 0 || f.putsWithVersion == 0 {
		t.Errorf("every PUT must carry expectedVersion: with=%d without=%d", f.putsWithVersion, f.putsNoVersion)
	}
}

// A tabbed board as Export → JSON writes it: widgets carry "id", tabs carry
// their own "id" and tabs[].widgetIds point at the widgets, a group child keeps
// its "_id". The server re-mints widget _ids, tab ids and (on create) child
// _ids, and re-encodes 6.0 as 6, yet the plan stays empty. Written as raw JSON
// (unsorted keys, odd whitespace) to prove text form does not matter either.
func testAccTabbedConfig(f *fakeDashboardAPI, title string) string {
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard_json" "test" {
  dashboard_json = <<-EOT
    {
      "widgets": [
        {"id": "w-latency", "type": "timeseries", "title": "Latency", "layout": {"x": 0, "y": 0, "width": 6.0, "height": 3},
         "requests": [{"queries": [{"dataSource": "metrics", "query": "avg:latency"}]}]},
        {"id": "w-errors",  "type": "query_value", "title": "Errors", "definition": {"precision": 2},
         "requests": [{"queries": [{"dataSource": "metrics", "query": "sum:errors"}]}]},
        {"id": "w-notes",   "type": "group", "title": "Notes",
         "widgets": [{"_id": "64b7f0c2e4b0a1b2c3d4e5f6", "type": "note", "text": "On call"}]}
      ],
      "title":   %q,
      "tabs": [
        {"id": "tab-overview", "name": "Overview", "widgetIds": ["w-latency", "w-errors"]},
        {"id": "tab-notes",    "name": "Notes",    "widgetIds": ["w-notes"]}
      ]
    }
  EOT
}
`, title)
}

func TestAccDashboard_tabs(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccTabbedConfig(f, "Tabbed"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(*terraform.State) error {
					b := f.board(f.onlyBoardID())
					tabs := b["tabs"].([]any)
					first := tabs[0].(map[string]any)["widgetIds"].([]any)
					w0 := b["widgets"].([]any)[0].(map[string]any)["_id"]
					if first[0] != w0 {
						return fmt.Errorf("server did not re-link tabs: tab widgetIds %v, first widget _id %v", first, w0)
					}
					return nil
				},
			},
			// Update re-mints every widget _id; still no diff afterwards.
			{
				Config: testAccTabbedConfig(f, "Tabbed v2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dashAddr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestAccDashboard_dryRunRejectsPlan(t *testing.T) {
	f := newFakeDashboardAPI(t)
	config := testAccProviderBlock(f) + `
resource "atatus_dashboard_json" "test" {
  dashboard_json = jsonencode({
    title   = "Broken"
    widgets = [{ type = "bogus_chart" }]
  })
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`widgets\[0\]: unsupported widget type "bogus_chart"`),
			},
		},
	})
	if n := f.boardCount(); n != 0 {
		t.Fatalf("dry run must not write; server has %d boards", n)
	}
}

func TestAccDashboard_notJSONObject(t *testing.T) {
	f := newFakeDashboardAPI(t)
	config := testAccProviderBlock(f) + `
resource "atatus_dashboard_json" "test" {
  dashboard_json = jsonencode(["not", "an", "object"])
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(`must be a JSON object`)},
		},
	})
}

// A board changed in the UI between refresh and apply: the PUT's
// expectedVersion is stale, the server answers 409 and nothing is overwritten.
func TestAccDashboard_conflict(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: testAccDashboardConfig(f, "Conflict")},
			{
				PreConfig:   f.setConcurrentEditOnNextPut,
				Config:      testAccDashboardConfig(f, "Conflict v2"),
				ExpectError: regexp.MustCompile(`(?s)changed\s+in\s+Atatus\s+after\s+Terraform.*apply\s+-refresh-only`),
			},
		},
	})
}

// A widget written with its type and title inside "definition" is stored
// differently by create (moved onto the widget) and refused by update, so it
// is rejected at validate time, before any request.
func TestAccDashboard_rejectsDefinitionTypeShape(t *testing.T) {
	f := newFakeDashboardAPI(t)
	config := testAccProviderBlock(f) + `
resource "atatus_dashboard_json" "test" {
  dashboard_json = jsonencode({
    title   = "Wrong shape"
    widgets = [{ definition = { type = "note", title = "Runbook" } }]
  })
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(`widgets\[0\]\.definition\.type: put "type" on the widget itself`)},
		},
	})
	if n := f.boardCount(); n != 0 {
		t.Fatalf("nothing may be written; server has %d boards", n)
	}
}

// The server's update writes only the board fields the body carries, so a
// field removed from dashboard_json keeps its stored value. Apply still
// succeeds (with a warning) and the plan settles; the stored value stays.
// Against an older server, whose PUT was a $set: a board field removed from
// the document stays on the server, and the provider warns instead of
// planning the removal forever.
func TestAccDashboard_removedBoardFieldKeptByServer(t *testing.T) {
	f := newFakeDashboardAPI(t)
	f.legacySetPut = true
	withoutDescription := strings.Replace(testAccDashboardConfig(f, "Fields"), `description = "Managed by Terraform"`, "", 1)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: testAccDashboardConfig(f, "Fields")},
			{
				Config: withoutDescription,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dashAddr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(*terraform.State) error {
					if got := f.board(f.onlyBoardID())["description"]; got != "Managed by Terraform" {
						return fmt.Errorf("fake no longer models the server's $set update: description = %v", got)
					}
					return nil
				},
			},
		},
	})
}

// A board field removed from the document is cleared on the server, and the
// next plan is empty.
func TestAccDashboard_removedBoardFieldCleared(t *testing.T) {
	f := newFakeDashboardAPI(t)
	withoutDescription := strings.Replace(testAccDashboardConfig(f, "Fields"), `description = "Managed by Terraform"`, "", 1)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: testAccDashboardConfig(f, "Fields")},
			{
				Config: withoutDescription,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dashAddr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(*terraform.State) error {
					if got, has := f.board(f.onlyBoardID())["description"]; has {
						return fmt.Errorf("description still stored: %v", got)
					}
					return nil
				},
			},
		},
	})
}

// A board as Export → JSON writes it from a stored board: template variables
// keep their subdocument _id (re-minted by create), and empty objects that the
// server's minimize drops. Create and update must both settle with no diff;
// a spaceIds change plans an update.
func testAccExportedConfig(f *fakeDashboardAPI, title, space string) string {
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard_json" "test" {
  dashboard_json = jsonencode({
    title    = %q
    spaceIds = [%q]
    templateVariables = [
      { _id = "65a000000000000000000001", name = "env", prefix = "env", availableValues = [], default = [] },
    ]
    widgets = [
      { type = "note", text = "Runbook", definition = {} },
      {
        type       = "timeseries"
        title      = "Latency"
        definition = { yaxis = {} }
        requests   = [{ queries = [{ dataSource = "metrics", query = "avg:latency", filters = "env:prod" }] }]
      },
    ]
  })
}
`, title, space)
}

func TestAccDashboard_exportedBoard(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccExportedConfig(f, "Exported", "space-a"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh:  []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(*terraform.State) error {
					v := f.board(f.onlyBoardID())["templateVariables"].([]any)[0].(map[string]any)
					if v["_id"] == "65a000000000000000000001" {
						return fmt.Errorf("fake no longer re-mints templateVariables[]._id on create")
					}
					return nil
				},
			},
			{
				Config: testAccExportedConfig(f, "Exported", "space-b"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dashAddr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(*terraform.State) error {
					got := f.board(f.onlyBoardID())["spaceIds"].([]any)
					if len(got) != 1 || got[0] != "space-b" {
						return fmt.Errorf("spaceIds not updated: %v", got)
					}
					return nil
				},
			},
		},
	})
}

// Numeric widget ids on a tabbed board: the server links them by String(id).
func TestAccDashboard_numericWidgetIDs(t *testing.T) {
	f := newFakeDashboardAPI(t)
	config := testAccProviderBlock(f) + `
resource "atatus_dashboard_json" "test" {
  dashboard_json = jsonencode({
    title = "Numeric ids"
    tabs  = [{ name = "A", widgetIds = [1] }, { name = "B", widgetIds = [2] }]
    widgets = [
      { id = 1, type = "note", text = "a" },
      { id = 2, type = "note", text = "b" },
    ]
  })
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh:  []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// A key the server renames on create is refused before any request.
func TestAccDashboard_rejectsRenamedKey(t *testing.T) {
	f := newFakeDashboardAPI(t)
	config := testAccProviderBlock(f) + `
resource "atatus_dashboard_json" "test" {
  dashboard_json = jsonencode({
    title       = "Snake"
    reflow_type = "auto"
    widgets     = [{ type = "note", text = "x" }]
  })
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(`reflow_type: write it as "reflowType"`)},
		},
	})
	if n := f.boardCount(); n != 0 {
		t.Fatalf("nothing may be written; server has %d boards", n)
	}
}

// A document that drops spaceIds keeps the board's Spaces (an omission never makes a board
// account-wide), and the next plan is empty; "spaceIds": [] clears them.
func TestAccDashboard_spacesKeptOnOmission(t *testing.T) {
	f := newFakeDashboardAPI(t)
	cfg := func(spaces string) string {
		return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard_json" "test" {
  dashboard_json = jsonencode({
    title   = "Spaces"%s
    widgets = [{ type = "note", text = "n" }]
  })
}
`, spaces)
	}
	spacesOf := func() []any {
		got, _ := f.board(f.onlyBoardID())["spaceIds"].([]any)
		return got
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg(`
    spaceIds = ["5p0000000000000000000001"]`)},
			{
				Config: cfg(""),
				Check: func(*terraform.State) error {
					if got := spacesOf(); len(got) != 1 {
						return fmt.Errorf("spaceIds after dropping them from the document = %v, want the Space kept", got)
					}
					return nil
				},
			},
			{Config: cfg(""), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{
				Config: cfg(`
    spaceIds = []`),
				Check: func(*terraform.State) error {
					if got := spacesOf(); len(got) != 0 {
						return fmt.Errorf("spaceIds after setting [] = %v, want none", got)
					}
					return nil
				},
			},
		},
	})
}
