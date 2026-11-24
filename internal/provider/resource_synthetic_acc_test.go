package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// One config per check type: the first version is created, imported and
// re-planned; the second changes fields in place.
var syntheticAccCases = []struct {
	resourceType string
	create       string
	update       string
}{
	{
		resourceType: "atatus_http_check",
		create: `
  name   = "API health"
  url    = "https://api.example.com/health"
  emails = ["devops@example.com"]
  assertion = [
    { type = "statusCode", operator = "is", target = "200" },
    { type = "responseTime", operator = "lessThan", target = "800" },
  ]`,
		update: `
  name        = "API health (renamed)"
  url         = "https://api.example.com/health"
  method      = "POST"
  frequency   = "10m"
  retry_count = 2
  channel_ids = ["ch-slack-alerts"]
  emails      = ["devops@example.com", "oncall@example.com"]
  tags        = ["env:production"]`,
	},
	{
		resourceType: "atatus_ssl_check",
		create: `
  name = "TLS certificate"
  host = "www.example.com"
  port = 443`,
		update: `
  name      = "TLS certificate (renamed)"
  host      = "www.example.com"
  port      = 8443
  locations = ["US_NEW_YORK", "EU_FRANKFURT"]`,
	},
	{
		resourceType: "atatus_tcp_check",
		create: `
  name = "Postgres port"
  host = "db.example.com"
  port = 5432`,
		update: `
  name     = "Postgres port (sequential)"
  host     = "db.example.com"
  port     = 5432
  run_type = "sequential"`,
	},
	{
		resourceType: "atatus_dns_check",
		create: `
  name = "DNS"
  host = "www.example.com"`,
		update: `
  name       = "DNS (custom server)"
  host       = "www.example.com"
  dns_server = "8.8.8.8"`,
	},
	{
		resourceType: "atatus_icmp_check",
		create: `
  name    = "Ping"
  host    = "example.com"
  packets = 5`,
		update: `
  name    = "Ping (renamed)"
  host    = "example.com"
  packets = 10
  emails  = ["oncall@example.com"]`,
	},
	{
		resourceType: "atatus_multistep_check",
		create: `
  name = "Login flow"
  step = [
    { name = "Home", url = "https://www.example.com" },
    { name = "Health", url = "https://www.example.com/health", method = "GET" },
  ]`,
		update: `
  name = "Login flow (renamed)"
  step = [
    { name = "Home", url = "https://www.example.com", allow_failure = true },
  ]`,
	},
}

func testAccCheckConfig(f *fakeDashboardAPI, resourceType, body string) string {
	return testAccProviderBlock(f) + fmt.Sprintf(`
resource %q "test" {%s
}
`, resourceType, body)
}

func TestAccSyntheticChecks_lifecycle(t *testing.T) {
	for _, tc := range syntheticAccCases {
		t.Run(tc.resourceType, func(t *testing.T) {
			f := newFakeDashboardAPI(t)
			addr := tc.resourceType + ".test"
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProviderFactories,
				CheckDestroy: func(*terraform.State) error {
					if n := f.checks.count(); n != 0 {
						return fmt.Errorf("%d checks left on the server after destroy", n)
					}
					return nil
				},
				Steps: []resource.TestStep{
					{
						Config: testAccCheckConfig(f, tc.resourceType, tc.create),
						Check:  resource.TestCheckResourceAttrSet(addr, "id"),
					},
					{
						// A second plan right after apply must be empty.
						Config: testAccCheckConfig(f, tc.resourceType, tc.create),
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
						},
					},
					{
						ResourceName:      addr,
						ImportState:       true,
						ImportStateVerify: true,
					},
					{
						Config: testAccCheckConfig(f, tc.resourceType, tc.update),
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
						},
					},
					{
						Config: testAccCheckConfig(f, tc.resourceType, tc.update),
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
						},
					},
					{
						// Import again after the update, which sets more fields
						// (tags, mixed channels, several locations, ...).
						ResourceName:      addr,
						ImportState:       true,
						ImportStateVerify: true,
					},
				},
			})
		})
	}
}

// An email with no matching EMAIL channel fails the plan, before anything is
// created.
func TestAccSyntheticChecks_unknownEmail(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccCheckConfig(f, "atatus_http_check", `
  name   = "API"
  url    = "https://api.example.com"
  emails = ["nobody@example.com"]`),
				ExpectError: regexp.MustCompile(`no email notification channel found for 'nobody@example.com'`),
			},
		},
	})
	if n := f.checks.count(); n != 0 {
		t.Fatalf("%d checks created despite the plan error", n)
	}
}

// retry_count above the server's limit is rejected at validate time.
func TestAccSyntheticChecks_retryCountLimit(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccCheckConfig(f, "atatus_tcp_check", `
  name        = "TCP"
  host        = "db.example.com"
  port        = 5432
  retry_count = 4`),
				ExpectError: regexp.MustCompile(`(?s)retry_count.*between 0 and 3`),
			},
		},
	})
}

// A check deleted outside Terraform is dropped from state on refresh and
// planned for re-creation, not reported as an error.
func TestAccSyntheticChecks_deletedOutsideTerraform(t *testing.T) {
	f := newFakeDashboardAPI(t)
	cfg := testAccCheckConfig(f, "atatus_dns_check", `
  name = "DNS"
  host = "www.example.com"`)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					f.checks.mu.Lock()
					for id := range f.checks.checks {
						delete(f.checks.checks, id)
					}
					f.checks.mu.Unlock()
				},
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("atatus_dns_check.test", plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}
