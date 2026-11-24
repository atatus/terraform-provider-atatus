package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const typedAddr = "atatus_dashboard.test"

func testAccTypedConfig(f *fakeDashboardAPI, title, firstWidget string) string {
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard" "test" {
  title       = %q
  description = "Managed by Terraform"

  template_variables = [{ name = "service", prefix = "service", default = ["checkout"] }]

  widgets = [
    %s,
    {
      note = {
        title      = "About"
        definition = { content = "**RED** metrics", background_color = "#fff" }
      }
    },
    {
      group = {
        title      = "Errors"
        definition = { header_style = "emphasis" }
        widgets = [{
          query_value = {
            title = "Error rate"
            requests = [{
              queries  = [{ name = "q", data_source = "logs", compute = { aggregation = "count", metric_name = "logs.count", bucket = 0 } }]
              formulas = [{ formula = "q" }]
            }]
            definition = { precision = 2, text_align = "left" }
          }
        }]
      }
    },
  ]

  tabs = [
    { name = "Overview", widgets = [1, 2] },
    { name = "Errors", widgets = [3] },
  ]
}
`, title, firstWidget)
}

const typedTimeseries = `{
      timeseries = {
        title  = "CPU"
        layout = { x = 0, y = 0, width = 6, height = 3 }
        requests = [{
          display_type = "line"
          queries      = [{ name = "q", data_source = "metrics", query = "avg:system.cpu.user{*} by {host}" }]
          sort         = { count = 10, order_by = [{ type = "formula", index = 0, order = "desc" }] }
        }]
        definition = {
          legend_mode = "compact"
          yaxis       = { min_json = jsonencode(0), scale = "log" }
          markers     = [{ type = "line", value = 90, severity = "error" }]
        }
      }
    }`

const typedToplist = `{
      toplist = {
        title    = "Top hosts"
        requests = [{ queries = [{ name = "q", data_source = "metrics", query = "avg:system.cpu.user{*} by {host}" }] }]
        definition = { toplist = { display = "flat" } }
      }
    }`

func TestAccTypedDashboard_lifecycle(t *testing.T) {
	f := newFakeDashboardAPI(t)
	f.reorderOnGet = true // the order Atatus lists widgets in is not the configuration's
	var id string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if _, still := f.boards[id]; still {
				return fmt.Errorf("dashboard %s still exists", id)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: testAccTypedConfig(f, "Service health", typedTimeseries),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(typedAddr, "id"),
					resource.TestCheckResourceAttrSet(typedAddr, "url"),
					func(s *terraform.State) error {
						id = s.RootModule().Resources[typedAddr].Primary.ID
						f.mu.Lock()
						defer f.mu.Unlock()
						b := f.boards[id]
						ws, _ := b["widgets"].([]any)
						if len(ws) != 3 {
							return fmt.Errorf("stored %d widgets, want 3", len(ws))
						}
						w0 := ws[0].(map[string]any)
						req := w0["requests"].([]any)[0].(map[string]any)
						if w0["type"] != "timeseries" || req["responseFormat"] != "timeseries" || req["displayType"] != "line" {
							return fmt.Errorf("first widget stored as %v", w0)
						}
						def := w0["definition"].(map[string]any)
						if def["legend_mode"] != "compact" || def["yaxis"].(map[string]any)["min"] != float64(0) {
							return fmt.Errorf("definition stored as %v", def)
						}
						group := ws[2].(map[string]any)
						child := group["widgets"].([]any)[0].(map[string]any)
						if child["type"] != "query_value" || child["requests"].([]any)[0].(map[string]any)["responseFormat"] != "scalar" {
							return fmt.Errorf("group child stored as %v", child)
						}
						tabs, _ := b["tabs"].([]any)
						if len(tabs) != 2 {
							return fmt.Errorf("stored %d tabs, want 2", len(tabs))
						}
						return nil
					},
				),
			},
			{
				Config:           testAccTypedConfig(f, "Service health", typedTimeseries),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				// A change made in Atatus to a configured value is drift; applying restores it.
				PreConfig: func() {
					f.mu.Lock()
					defer f.mu.Unlock()
					f.boards[id]["title"] = "Renamed in the UI"
					w0 := f.boards[id]["widgets"].([]any)[0].(map[string]any)
					w0["definition"].(map[string]any)["legend_mode"] = "none"
					// A key the configuration never set is not drift.
					w0["definition"].(map[string]any)["autoscale"] = false
				},
				Config: testAccTypedConfig(f, "Service health", typedTimeseries),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(typedAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(typedAddr, "title", "Service health"),
			},
			{
				// Atatus rewrites a query's bucket on save; that is not drift.
				PreConfig: func() {
					f.mu.Lock()
					defer f.mu.Unlock()
					g := f.boards[id]["widgets"].([]any)[2].(map[string]any)
					q := g["widgets"].([]any)[0].(map[string]any)["requests"].([]any)[0].(map[string]any)["queries"].([]any)[0].(map[string]any)
					q["compute"].(map[string]any)["bucket"] = float64(86400)
				},
				Config:           testAccTypedConfig(f, "Service health", typedTimeseries),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				// Another widget type in the first position.
				Config: testAccTypedConfig(f, "Service health v2", typedToplist),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(typedAddr, "widgets.0.toplist.title", "Top hosts"),
					resource.TestCheckNoResourceAttr(typedAddr, "widgets.0.timeseries.title"),
				),
			},
			{
				Config:           testAccTypedConfig(f, "Service health v2", typedToplist),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

// Import reads every setting Atatus returns; the configuration it implies plans no change.
func TestAccTypedDashboard_import(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: testAccTypedConfig(f, "Imported", typedTimeseries)},
			{
				ResourceName: typedAddr, ImportState: true, ImportStateVerify: true,
				// Import reads every setting Atatus returns, its defaults included: the board's layout
				// type, and each request's response format, right-axis flag and default compute bucket.
				ImportStateVerifyIgnore: []string{"version", "url", "layout_type",
					"widgets.0.timeseries.requests.0.on_right_yaxis", "widgets.0.timeseries.requests.0.response_format",
					"widgets.0.timeseries.requests.0.queries.0.compute",
					"widgets.2.group.widgets.0.query_value.requests.0.on_right_yaxis",
					"widgets.2.group.widgets.0.query_value.requests.0.response_format"},
			},
		},
	})
}

func TestAccTypedDashboard_validation(t *testing.T) {
	f := newFakeDashboardAPI(t)
	board := func(widgets, tabs string) string {
		return testAccProviderBlock(f) + fmt.Sprintf(`
resource "atatus_dashboard" "test" {
  title   = "V"
  widgets = [%s]
  %s
}
`, widgets, tabs)
	}
	note := `{ note = { definition = { content = "n" } } }`
	cases := map[string]struct{ config, want string }{
		"two widget types":    {board(`{ note = { definition = { content = "n" } }, free_text = { text = "t" } }`, ""), `sets exactly one widget type`},
		"no widget type":      {board(`{}`, ""), `sets exactly one widget type`},
		"tab out of range":    {board(note, `tabs = [{ name = "A", widgets = [2] }]`), `there are 1 widgets`},
		"widget on no tab":    {board(note+", "+note, `tabs = [{ name = "A", widgets = [1] }]`), `Not on any tab: 2`},
		"widget on two tabs":  {board(note, `tabs = [{ name = "A", widgets = [1] }, { name = "B", widgets = [1] }]`), `a widget is on exactly one tab`},
		"bad enum":            {board(`{ timeseries = { requests = [{ display_type = "bars", queries = [{ name = "q", data_source = "metrics" }] }] } }`, ""), `display_type`},
		"missing data source": {board(`{ timeseries = { requests = [{ queries = [{ name = "q" }] }] } }`, ""), `data_source`},
		"uppercase sort":      {board(`{ table = { requests = [{ queries = [{ name = "q", data_source = "metrics" }], sort = { order_by = [{ order = "DESC" }] } }] } }`, ""), `order`},
		"bad json":            {board(`{ timeseries = { requests = [{ queries = [{ name = "q", data_source = "metrics" }] }], definition = { yaxis = { min_json = "{oops" } } } }`, ""), `Invalid JSON`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProviderFactories,
				Steps:                    []resource.TestStep{{Config: tc.config, PlanOnly: true, ExpectError: regexp.MustCompile(tc.want)}},
			})
		})
	}
}
