---
page_title: "atatus_dns_check Resource - terraform-provider-atatus"
subcategory: ""
description: |-
  The atatus_dns_check resource manages DNS synthetic monitoring checks in Atatus.
---

# `atatus_dns_check` Resource

The `atatus_dns_check` resource manages DNS synthetic monitoring checks in Atatus.

Use this resource to monitor domain name resolution, DNS response times, and DNS record values such as A, AAAA, CNAME, MX, NS, and TXT records across configured testing locations. The resource supports creating, updating, and deleting synthetic DNS checks through Terraform.

## Example Usage

### DNS Resolution & Record Match Check

```hcl
# resource "atatus_dns_check" "dns_check_sample"
# 1st argument: Resource type defined by the Atatus Terraform provider.
# 2nd argument: Local resource name, which must be unique within the Terraform module.

resource "atatus_dns_check" "dns_check_sample" {
  name       = "Atatus DNS Resolution Check"
  host       = "www.atatus.com"
  dns_server = "8.8.8.8"
  port       = 53
  frequency  = "5m"
  locations  = ["US_NEW_YORK", "EU_FRANKFURT"]
  emails     = ["devops@example.com"]
  tags       = ["env:production", "team:networking"]

  retry_count         = 0
  min_location_failed = 1

  assertion = [
    # 1. Response Time Latency
    {
      type     = "responseTime"
      operator = "lessThan"
      target   = "1000"
    },

    # 2. At Least One Record Match (e.g. A record)
    {
      type     = "atLeastOneRecord"
      property = "A"
      operator = "contains"
      target   = "104"
    },

    # 3. Every Available Record Match (e.g. AAAA record with Regex)
    {
      type     = "everyAvailableRecord"
      property = "AAAA"
      operator = "matchesRegex"
      target   = "2606:.*"
    }
  ]
}
```

## Argument Reference

### Required Arguments

- **`name`** - (String) Name of the DNS synthetic check as displayed in the Atatus dashboard.

- **`host`** - (String) Domain name or hostname to query (e.g. `example.com` or `www.atatus.com`).

- **`dns_server`** - (String) Custom DNS Nameserver IP address to query (e.g. `8.8.8.8`, `1.1.1.1`, `9.9.9.9`). Defaults to empty for system default resolver.

- **`port`** - (Number) DNS port number (default: `53`).

- **`tags`** - (List of Strings) List of tags (e.g. `["env:production", "team:networking"]`).

## DNS Assertions Reference

- **`assertion`** - (List of Objects) Rules to validate DNS resolution latency and record values.

| type | property | operator | target |
|---|---|---|---|
| `responseTime` | — | `lessThan` | `"1000"` |
| `atLeastOneRecord` | A, AAAA, CNAME, MX, NS, TXT | `is`, `contains`, `matchesRegex`, `doesNotMatchRegex` | value |
| `everyAvailableRecord` | A, AAAA, CNAME, MX, NS, TXT | `is`, `contains`, `matchesRegex`, `doesNotMatchRegex` | value |

## 🌍 Supported Location Codes Reference

- **`locations`** - (List of Strings) Array of global region codes where the check is executed from. Default: `["US_NEW_YORK"]`.

When specifying testing locations in the `locations` attribute of any synthetic check, use the following region codes:

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

- **`emails`** - (List of Strings) Email addresses to alert directly when the check fails (auto-mapped to Atatus notification channels).

- **`channel_ids`** - (List of Strings) Notification channel IDs to alert on failure.




## Import

An existing check is imported by its id, the 24-character hex value in the check's URL in Atatus:

```shell
terraform import atatus_dns_check.example 64b7f0c2e4b0a1b2c3d4e5f6
```

After import, `emails` lists the check's email notification channels and `channel_ids` the rest. Run `terraform plan` to see how the imported check differs from your configuration.
