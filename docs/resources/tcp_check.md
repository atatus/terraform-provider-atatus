---
page_title: "atatus_tcp_check Resource - terraform-provider-atatus"
subcategory: ""
description: |-
  The atatus_tcp_check resource manages TCP port synthetic monitoring checks in Atatus.
---

# `atatus_tcp_check` Resource

The `atatus_tcp_check` resource manages TCP port synthetic monitoring checks in Atatus.

Use this resource to monitor network connectivity and connection latency to configured TCP ports on servers and services, such as PostgreSQL, MySQL, Redis, SSH, SMTP, and HTTP/S. The resource supports creating, updating, and deleting synthetic TCP checks through Terraform.

## Example Usage

### Database TCP Port Connectivity Check

```hcl
# Resource type: atatus_tcp_check
# Resource name: db_tcp_check
# 1st argument: Resource type defined by the Atatus Terraform provider.
# 2nd argument: Local resource name, which must be unique within the Terraform module.

resource "atatus_tcp_check" "db_tcp_check" {
  name = "Production Database TCP Port Check"
  host = "db.example.com"
  port = 443

  tags = ["env:production", "team:database"]

  assertion = [
    {
      type     = "responseTime"
      operator = "lessThan"
      target   = "1000"
    }
  ]

  locations           = ["US_NEW_YORK", "EU_FRANKFURT"]
  runtype             = "parallel"
  frequency           = "5m"
  retry_count         = 0
  min_location_failed = 1

  emails = ["devops@example.com"]
}
```

## Argument Reference

### Required Arguments

- **`name`** - (String) Name of the TCP synthetic check as displayed in the Atatus dashboard.

- **`host`** - (String) Hostname, domain name to connect to (e.g. `db.example.com`).

- **`port`** - (Number) Destination port number for the TCP connection (e.g. `22`, `80`, `443`, `3306`, `5432`, `6379`).

- **`tags`** - (List of Strings) List of tags (e.g. `["env:production", "service:database"]`).

## TCP Assertions Reference

- **`assertion`** - (List of Objects) Rules to validate TCP connection response time.

| type | operator | target |
|---|---|---|
| `responseTime` | `lessThan` | `1000` |

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
terraform import atatus_tcp_check.example 64b7f0c2e4b0a1b2c3d4e5f6
```

After import, `emails` lists the check's email notification channels and `channel_ids` the rest. Run `terraform plan` to see how the imported check differs from your configuration.
