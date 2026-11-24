---
page_title: "atatus_http_check Resource - terraform-provider-atatus"
subcategory: ""
description: |-
  The atatus_http_check resource manages HTTP/API synthetic monitoring checks in Atatus.
---

# `atatus_http_check` Resource

The `atatus_http_check` resource manages HTTP/API synthetic monitoring checks in Atatus.

Use this resource to monitor website availability, API response times, HTTP status codes, response headers, and response body content from configured testing locations. The resource supports creating, updating, and deleting synthetic HTTP checks through Terraform.

## Example Usage

### Production API Healthcheck with Multiple Assertions

```hcl
# Resource type: atatus_http_check
# Resource name: api_health
# 1st argument: Resource type defined by the Atatus Terraform provider.
# 2nd argument: Local resource name, which must be unique within the Terraform module.

resource "atatus_http_check" "api_health" {
  name   = "Production API Healthcheck"
  url    = "https://api.example.com/health"
  method = "GET"

  tags = ["env:production", "team:backend", "tier:critical"]

  assertion = [
    # 1. Validate status code is 200
    {
      type     = "statusCode"
      operator = "is"
      target   = "200"
    },

    # 2. Validate response latency is under 1000ms
    {
      type     = "responseTime"
      operator = "lessThan"
      target   = "1000"
    },
  ]

  locations = ["US_NEW_YORK", "EU_FRANKFURT", "AP_SINGAPORE"]
  run_type  = "parallel"
  frequency = "1m"

  retry_count         = 2
  min_location_failed = 1

  emails = ["devops@example.com", "alerts@example.com"]
}
```

### Bulk Monitors Creation using `for_each`

The Atatus Terraform provider supports creating multiple HTTP checks simultaneously using Terraform's `for_each` meta-argument. This allows you to monitor multiple websites by defining their URLs in a single local map instead of creating separate resources for each website.

#### Example Configuration

The following example creates HTTP checks for 20 websites with the same monitoring configuration.

```hcl
locals {
  websites = {
    google        = "https://www.google.com"
    youtube       = "https://www.youtube.com"
    github        = "https://github.com"
    amazon        = "https://www.amazon.com"
    microsoft     = "https://www.microsoft.com"
    apple         = "https://www.apple.com"
    wikipedia     = "https://www.wikipedia.org"
    reddit        = "https://www.reddit.com"
    linkedin      = "https://www.linkedin.com"
    facebook      = "https://www.facebook.com"
    instagram     = "https://www.instagram.com"
    netflix       = "https://www.netflix.com"
    cloudflare    = "https://www.cloudflare.com"
    stackoverflow = "https://stackoverflow.com"
    terraform     = "https://www.terraform.io"
    dynatrace     = "https://www.dynatrace.com"
    atatus        = "https://www.atatus.com"
    npm           = "https://www.npmjs.com"
    docker        = "https://www.docker.com"
    openai        = "https://openai.com"
  }
}

resource "atatus_http_check" "bulk_websites" {
  for_each = local.websites

  name   = "Bulk HTTP Check - ${each.key}"
  url    = each.value
  method = "GET"

  tags = ["env:production", "test:bulk-http", "website:${each.key}"]

  locations = [
    "US_NEW_YORK",
    "US_SAN_FRANCISCO",
    "CA_TORONTO",
    "EU_FRANKFURT",
    "UK_LONDON",
    "EU_AMSTERDAM",
    "AP_SINGAPORE",
    "AP_SYDNEY",
    "AP_BANGALORE"
  ]

  run_type            = "parallel"
  frequency           = "1m"
  retry_count         = 3
  min_location_failed = 3

  emails = ["devops@example.com"]
}
```

## Argument Reference

### Required Arguments

- **`name`** - (String) The name of the synthetic check as displayed in the Atatus dashboard.

- **`url`** - (String) The fully qualified URL endpoint to monitor (e.g. `https://api.example.com/v1/ping`).

- **`method`** - (String) HTTP request method (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `HEAD`, `OPTIONS`). Default: `"GET"`.

- **`tags`** - (List of Strings) List of metadata tags to group and filter monitors in the dashboard (e.g. `["env:production", "service:api"]`).

## Assertion Block Reference

- **`assertion`** - (List of Objects) Rules used to determine whether a check test passes or fails.

Each `assertion` object supports the following attributes.

## HTTP Assertions Matrix

| type | Notes | Property / Key | operator | target |
|---|---|---|---|---|
| `statusCode` | — | — | `is`, `isNot` | `"200"` |
| `responseTime` | — | — | `lessThan` | `"1000"` (ms) |
| `header` | — | Header name | `is`, `isNot`, `contains`, `doesNotContain`, `matchesRegex`, `doesNotMatchesRegex` | String value |
| `body` | — | — | `is`, `isNot`, `contains`, `doesNotContain`, `matchesRegex`, `doesNotMatchesRegex` | Comparison value |
| `body` | `jsonpath`, `xpath` | JSONPath / XPath | `is`, `isNot`, `contains`, `doesNotContain`, `matchesRegex`, `doesNotMatchesRegex`, `moreThan`, `lessThan` | Comparison value |
| `bodyhash` | — | — | `md5`, `sha1`, `sha256` | Hash string |

## 🌍 Supported Location Codes Reference

- **`locations`** - (List of Strings) Array of global region codes where the check is executed from. Default: `["US_NEW_YORK"]`.

When specifying testing locations in the `locations` attribute of any synthetic check, use the following region codes.

| Region Code | Location / City | Geographic Area |
|---|---|---|
| `US_NEW_YORK` | New York, USA | North America |
| `US_SAN_FRANCISCO` | San Francisco, USA | North America |
| `CA_TORONTO` | Toronto, Canada | North America |
| `UK_LONDON` | London, United Kingdom | Europe |
| `EU_FRANKFURT` | Frankfurt, Germany | Europe |
| `EU_AMSTERDAM` | Amsterdam, Netherlands | Europe |
| `AP_BANGALORE` | Bangalore, India | Asia Pacific |
| `AP_SINGAPORE` | Singapore | Asia Pacific |
| `AP_SYDNEY` | Sydney, Australia | Asia Pacific |


## Common Settings Reference (Applicable to all check types)

- **`run_type`** - (String) Execution strategy across selected testing locations (`"parallel"` or `"sequential"`). Default: `"parallel"`. When set to `"sequential"`, locations run in sequence and `min_location_failed` cannot be configured.

- **`frequency`** - (String) Execution frequency interval (`"1m"`, `"5m"`, `"10m"`, `"15m"`, `"30m"`, `"1h"`, `"6h"`, `"12h"`, `"1d"`). Default: `"5m"`.

- **`retry_count`** - (Number) Number of automatic retries before triggering an alert upon check failure (`0` to `3`). Supported for both `"parallel"` and `"sequential"` runs. Default: `0`.

- **`min_location_failed`** - (Number) Minimum number of locations that must fail before triggering an incident (default: `1`).

  **Note:** Only applicable when `run_type` is `"parallel"`. Configuring `min_location_failed` when `run_type = "sequential"` is not allowed and produces a validation error.

- **`emails`** - (List of Strings) List of notification email addresses to alert when the check fails. Emails are automatically mapped to configured Atatus notification channels.

- **`channel_ids`** - (List of Strings) List of specific Atatus notification channel IDs (e.g. Slack, PagerDuty, Webhook channels).
## Import

An existing check is imported by its id, the 24-character hex value in the check's URL in Atatus:

```shell
terraform import atatus_http_check.example 64b7f0c2e4b0a1b2c3d4e5f6
```

After import, `emails` lists the check's email notification channels and `channel_ids` the rest. Run `terraform plan` to see how the imported check differs from your configuration.
