package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccDataSources_lookups(t *testing.T) {
	f := newFakeDashboardAPI(t)
	config := testAccDashboardConfig(f, "Looked up") + `
data "atatus_dashboard" "by_title" {
  title = "Looked up"
  depends_on = [atatus_dashboard_json.test]
}

data "atatus_dashboard" "by_id" {
  id = atatus_dashboard_json.test.id
}

data "atatus_dashboards" "search" {
  search     = "looked"
  depends_on = [atatus_dashboard_json.test]
}

data "atatus_dashboard_group" "by_name" {
  name = "Platform"
}

data "atatus_dashboard_group" "by_id" {
  id = "a00000000000000000000001"
}

data "atatus_powerpack" "custom" {
  name = "Service health"
}

data "atatus_powerpack" "preset" {
  id = "hosts-overview"
}

data "atatus_notification_channel" "slack" {
  name = "#alerts"
}

data "atatus_project" "apm" {
  name = "checkout"
  type = "apm"
}

data "atatus_space" "payments" {
  name = "Payments"
}

data "atatus_team" "sre" {
  name = "SRE"
}

data "atatus_user" "ada" {
  email = "ADA@example.com"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.atatus_dashboard.by_title", "id", dashAddr, "id"),
					resource.TestCheckResourceAttrPair("data.atatus_dashboard.by_title", "url", dashAddr, "url"),
					resource.TestCheckResourceAttr("data.atatus_dashboard.by_id", "title", "Looked up"),
					resource.TestCheckResourceAttr("data.atatus_dashboards.search", "dashboards.#", "1"),
					resource.TestCheckResourceAttr("data.atatus_dashboards.search", "dashboards.0.title", "Looked up"),
					resource.TestCheckResourceAttr("data.atatus_dashboard_group.by_name", "id", "a00000000000000000000001"),
					resource.TestCheckResourceAttr("data.atatus_dashboard_group.by_name", "dashboard_ids.#", "2"),
					resource.TestCheckResourceAttr("data.atatus_dashboard_group.by_id", "dashboard_ids.1", "b00000000000000000000002"),
					resource.TestCheckResourceAttr("data.atatus_dashboard_group.by_id", "space_ids.0", "5p0000000000000000000001"),
					resource.TestCheckResourceAttr("data.atatus_powerpack.custom", "id", "c00000000000000000000001"),
					resource.TestCheckResourceAttr("data.atatus_powerpack.custom", "version", "3"),
					resource.TestCheckResourceAttr("data.atatus_powerpack.preset", "source", "preset"),
					resource.TestCheckResourceAttr("data.atatus_notification_channel.slack", "id", "ch-slack-alerts"),
					resource.TestCheckResourceAttr("data.atatus_notification_channel.slack", "type", "SLACK"),
					resource.TestCheckResourceAttr("data.atatus_project.apm", "id", "p00000000000000000000001"),
					resource.TestCheckResourceAttr("data.atatus_project.apm", "sub_type", "Node.js"),
					resource.TestCheckResourceAttr("data.atatus_space.payments", "id", "5p0000000000000000000001"),
					resource.TestCheckResourceAttr("data.atatus_team.sre", "id", "7e0000000000000000000001"),
					resource.TestCheckResourceAttr("data.atatus_user.ada", "id", "u00000000000000000000001"),
					resource.TestCheckResourceAttr("data.atatus_user.ada", "role", "owner"),
				),
			},
		},
	})
}

func TestAccDataSources_matchErrors(t *testing.T) {
	f := newFakeDashboardAPI(t)
	cases := []struct {
		config string
		want   string
	}{
		{`data "atatus_dashboard" "x" { title = "Nope" }`, `no dashboard with title "Nope"`},
		{`data "atatus_dashboard_group" "x" { name = "Twin" }`, `2 dashboard groups have name "Twin"; look it up by id instead`},
		{`data "atatus_project" "x" { name = "checkout" }`, `2 projects have name "checkout"; set type to choose between them`},
		{`data "atatus_dashboard_group" "x" { id = "a00000000000000000000009" }`, `Dashboard group not found`},
		{`data "atatus_dashboard" "x" {}`, `Invalid Attribute Combination`},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProviderFactories,
				Steps: []resource.TestStep{{
					Config:      testAccProviderBlock(f) + tc.config,
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}
