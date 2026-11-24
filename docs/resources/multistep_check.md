---
page_title: "atatus_multistep_check Resource - terraform-provider-atatus"
subcategory: ""
description: |-
  The atatus_multistep_check resource manages multistep API synthetic monitoring checks in Atatus.
---

# `atatus_multistep_check` Resource

The `atatus_multistep_check` resource manages multistep API synthetic monitoring checks in Atatus.

Use this resource to monitor complex, sequential API workflows by executing multiple requests in a defined order and validating responses at each step. The resource supports creating, updating, and deleting synthetic multistep API checks through Terraform.

## Example Usage

### Multi-Step API Workflow (Healthcheck and Profile Retrieval)

```hcl
# Resource type: atatus_multistep_check
# Resource name: demo_mult_check
# 1st argument: Resource type defined by the Atatus Terraform provider.
# 2nd argument: Local resource name, which must be unique within the Terraform module.

resource "atatus_multistep_check" "demo_mult_check" {
  name = "Testing mult check"
  tags = ["env:production", "tier:critical"]

  step = [
    # Step 1: Verify service health (Critical step: halts check run on failure)
    {
      name   = "Verify Service Health"
      url    = "https://www.instagram.com"
      method = "GET"

      assertion = [
        {
          type     = "statusCode"
          operator = "is"
          target   = "200"
        },
        {
          type     = "responseTime"
          operator = "lessThan"
          target   = "1000"
        }
      ]

      allow_failure = false
      retry_count   = 1
    },

    # Step 2: Fetch user profile (Non-blocking step: continues even on failure)
    {
      name   = "atatus check"
      url    = "https://www.atatus.com"
      method = "GET"

      assertion = [
        {
          type     = "statusCode"
          operator = "is"
          target   = "200"
        }
      ]

      allow_failure        = true
      consider_test_failed = false
    }
  ]

  locations           = ["US_NEW_YORK", "EU_FRANKFURT", "AP_BANGALORE"]
  run_type            = "parallel"
  frequency           = "1m"
  min_location_failed = 1

  emails = ["devops@example.com"]
}
```

## Argument Reference

### Top-Level Check Arguments

#### Required Arguments

- **`name`** - (String) Name of the multistep synthetic check as displayed in the Atatus dashboard.

- **`tags`** - (List of Strings) List of tags (e.g. `["env:production"]`).

- **`step`** - (List of Objects) Ordered sequence of API step objects to execute sequentially.

Each object in the `step` list supports the following attributes.

#### Required Arguments in HTTP Check Steps

- **`name`** - (String) Descriptive name of the individual step.

- **`method`** - (String) HTTP Method: `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `HEAD`, `OPTIONS`. Default: `"GET"`.

- **`url`** - (String) URL endpoint to call for this step.

#### Step Execution & Alert Arguments

- **`allow_failure`** - (Boolean) Whether subsequent steps continue executing if this step fails. Default: `false`.

- **`consider_test_failed`** - (Boolean) Whether failure in this step marks the entire synthetic test run as failed.

  **Note:** This field is only applicable when `allow_failure = true`. When `allow_failure = false`, this field must not be specified because any failure immediately halts and fails the check run. Default: `true` (when `allow_failure = true`).

- **`retry_count`** - (Number) Retries for this specific step before considering it failed. Default: `0` (optional, automatically defaults to `0` if omitted).

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

- **`min_location_failed`** - (Number) Minimum number of failed locations required before alerting (default: `1`).

  **Note:** Only applicable when `run_type` is `"parallel"`. Configuring `min_location_failed` when `run_type = "sequential"` is not allowed and produces a validation error.

- **`emails`** - (List of Strings) List of email addresses to alert directly when the check fails (auto-mapped to Atatus notification channels).

- **`channel_ids`** - (List of Strings) List of specific Atatus notification channel IDs to alert on failure.
## Import

An existing check is imported by its id, the 24-character hex value in the check's URL in Atatus:

```shell
terraform import atatus_multistep_check.example 64b7f0c2e4b0a1b2c3d4e5f6
```

After import, `emails` lists the check's email notification channels and `channel_ids` the rest. Run `terraform plan` to see how the imported check differs from your configuration.
