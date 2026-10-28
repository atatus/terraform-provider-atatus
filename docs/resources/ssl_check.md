# `atatus_ssl_check` Resource

The `atatus_ssl_check` resource manages SSL/TLS certificate and connection monitoring checks in Atatus.

Use this resource to monitor SSL certificate expiration, supported TLS versions, and TLS handshake latency for configured domains and endpoints across testing locations. The resource supports creating, updating, and deleting synthetic SSL/TLS checks through Terraform.

## Example Usage

### Production SSL Certificate & Handshake Check

```hcl id="sslpr01"
# Resource type: atatus_ssl_check
# Resource name: production_ssl
# 1st argument: Resource type defined by the Atatus Terraform provider.
# 2nd argument: Local resource name, which must be unique within the Terraform module.

resource "atatus_ssl_check" "production_ssl" {
  name = "Production SSL Certificate Check"
  host = "www.atatus.com"
  port = 443

  tags = ["env:production", "team:security"]

  assertion = [
    # Ensure certificate expires in more than 30 days
    {
      type     = "certificate"
      operator = "expiresInMoreThan"
      target   = "30"
    },

    # Ensure maximum supported TLS version is at least 1.3
    {
      type     = "maximumTLSVersion"
      operator = "moreThanOrEqual"
      target   = "1.3"
    },

    # Ensure minimum supported TLS version is at least 1.2
    {
      type     = "minimumTLSVersion"
      operator = "moreThanOrEqual"
      target   = "1.2"
    },

    # Validate handshake response latency is under 1000ms
    {
      type     = "responseTime"
      operator = "lessThan"
      target   = "1000"
    }
  ]

  locations           = ["US_NEW_YORK", "EU_FRANKFURT", "AP_BANGALORE"]
  run_type            = "parallel"
  frequency           = "1m"
  min_location_failed = 1
  retry_count         = 0

  emails = ["security@example.com", "devops@example.com"]
}
```

## Argument Reference

### Required Arguments

- **`name`** - (String) Name of the SSL synthetic check as displayed in the Atatus dashboard.

- **`host`** - (String) Hostname or domain name to check for SSL/TLS (e.g. `www.example.com`).

- **`port`** - (Number) Port number for the SSL connection (default: `443`).

- **`tags`** - (List of Strings) List of tags (e.g. `["env:production", "team:security"]`).

## SSL Assertions Reference

- **`assertion`** - (List of Objects) Rules to validate SSL certificate validity, TLS versions, and handshake response times.

| Type (`type`) | Operator (`operator`) | Target (`target`) |
|---|---|---|
| `certificate` | `expiresInMoreThan`, `expiresInLessThan` | `"10"`, `"30"` |
| `maximumTLSVersion` | `moreThanOrEqual`, `is`, `moreThan`, `lessThan` | `"1.3"`, `"1.2"` |
| `minimumTLSVersion` | `moreThan`, `moreThanOrEqual` | `"1.0"`, `"1.1"` |
| `responseTime` | `lessThan` | `"1000"` |

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

- **`emails`** - (List of Strings) Email addresses to alert directly when the check fails (auto-mapped to Atatus notification channels).

- **`channel_ids`** - (List of Strings) Notification channel IDs to alert on failure.