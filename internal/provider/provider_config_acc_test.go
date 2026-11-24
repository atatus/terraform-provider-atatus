package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const testAccTCPBody = `
resource "atatus_tcp_check" "test" {
  name = "Postgres port"
  host = "db.example.com"
  port = 5432
}
`

// The key and endpoint can come from ATATUS_API_KEY and ATATUS_ENDPOINT.
func TestAccProvider_environmentVariables(t *testing.T) {
	f := newFakeDashboardAPI(t)
	t.Setenv(envAPIKey, fakeAPIKey)
	t.Setenv(envEndpoint, f.URL())
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `provider "atatus" {}` + testAccTCPBody,
				Check:  resource.TestCheckResourceAttrSet("atatus_tcp_check.test", "id"),
			},
		},
	})
}

// With no key anywhere, configuration fails with a message naming both places.
func TestAccProvider_missingAPIKey(t *testing.T) {
	f := newFakeDashboardAPI(t)
	t.Setenv(envAPIKey, "")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fmt.Sprintf(`provider "atatus" { endpoint = %q }`, f.URL()) + testAccTCPBody,
				ExpectError: regexp.MustCompile(`(?s)Missing Atatus API Key.*ATATUS_API_KEY`),
			},
		},
	})
}

// A wrong key fails when the provider starts, before any resource is planned.
func TestAccProvider_validateRejectsBadKey(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`provider "atatus" {
  api_key  = "wrong"
  endpoint = %q
}`, f.URL()) + testAccTCPBody,
				ExpectError: regexp.MustCompile(`Atatus API Key Check Failed`),
			},
		},
	})
}

// validate = false skips the start-up check: the same wrong key is only
// rejected when a resource calls the API.
func TestAccProvider_validateFalseSkipsCheck(t *testing.T) {
	f := newFakeDashboardAPI(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`provider "atatus" {
  api_key  = "wrong"
  endpoint = %q
  validate = false
}`, f.URL()) + testAccTCPBody,
				ExpectError: regexp.MustCompile(`API Failure On Create`),
			},
		},
	})
}
