# `atatus_icmp_check` Resource

The `atatus_icmp_check` resource manages ICMP (Ping) synthetic monitoring checks in Atatus.

Use this resource to monitor network connectivity, ping latency, packet loss percentage, and packets received from configured testing locations. The resource supports measuring average, minimum, and maximum latency, as well as latency standard deviation, to help identify network performance issues. It supports creating, updating, and deleting synthetic ICMP checks through Terraform.

## Example Usage

### Hostname Ping Check with Comprehensive Assertions

```hcl id="icmpex1"
# Resource type: atatus_icmp_check
# Resource name: sample_icmp_check
# 1st argument: Resource type defined by the Atatus Terraform provider.
# 2nd argument: Local resource name, which must be unique within the Terraform module.

resource "atatus_icmp_check" "sample_icmp_check" {
  name    = "Production Ping Check"
  host    = "example.com"
  packets = 10 # Allowed: 1 to 10

  tags = ["env:production", "team:infrastructure"]

  assertion = [
    # 1. Average Latency under 1000ms
    {
      type     = "latency"
      property = "avg"
      operator = "lessThan"
      target   = "1000"
    },

    # 2. Maximum Latency under 1500ms
    {
      type     = "latency"
      property = "max"
      operator = "lessThan"
      target   = "1500"
    },

    # 3. Minimum Latency
    {
      type     = "latency"
      property = "min"
      operator = "lessThanOrEqual"
      target   = "100"
    },

    # 4. Standard Deviation Latency
    {
      type     = "latency"
      property = "stddev"
      operator = "lessThan"
      target   = "300"
    },

    # 5. Zero Packet Loss
    {
      type     = "packetLoss"
      operator = "is"
      target   = "0"
    },

    # 6. Expect all 10 packets received
    {
      type     = "packetsReceived"
      operator = "is"
      target   = "10"
    }
  ]

  locations           = ["CA_TORONTO", "EU_AMSTERDAM", "AP_BANGALORE"]
  run_type            = "parallel"
  frequency           = "1m"
  retry_count         = 0
  min_location_failed = 1

  emails = ["devops@example.com"]
}
```

## Argument Reference

### Required Arguments

- **`name`** - (String) Name of the ICMP synthetic check as displayed in the Atatus dashboard.

- **`host`** - (String) Target domain name (e.g. `example.com`) or IPv4 address (e.g. `8.8.8.8`) without protocol prefix (`http://`).

- **`packets`** - (Number) Number of ICMP echo ping packets to transmit per test run (`1` to `10`, default: `10`).

- **`tags`** - (List of Strings) List of tags (e.g. `["env:production"]`).

## ICMP Assertions Reference

- **`assertion`** - (List of Objects) Assertion rules to validate latency, packet loss, or packets received.

| type | property | operator | target |
|---|---|---|---|
| `latency` | `avg`, `min`, `max`, `stddev` | `is`, `lessThan`, `lessThanOrEqual`, `moreThan`, `moreThanOrEqual` | `"1000"`, `"150"` |
| `packetLoss` | — | `is`, `lessThan`, `lessThanOrEqual`, `moreThan`, `moreThanOrEqual` | `"0"`, `"50"` |
| `packetsReceived` | — | `is`, `lessThan`, `lessThanOrEqual`, `moreThan`, `moreThanOrEqual` | `"10"`, `"5"` |

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

- **`frequency`** - (String) Execution frequency interval (`"1m"`, `"5m"`, `"10m"`, `"15m"`, `"30m"`, `"1h"`, `"6h"`, `"12h"`, `"1d"`). Default: `"1m"`.

- **`retry_count`** - (Number) Number of automatic retries before triggering an alert upon check failure (`0` to `3`). Supported for both `"parallel"` and `"sequential"` runs. Default: `0`.

- **`min_location_failed`** - (Number) Minimum number of failed locations required before alerting (default: `1`).

  **Note:** Only applicable when `run_type` is `"parallel"`. Configuring `min_location_failed` when `run_type = "sequential"` is not allowed and produces a validation error.

- **`emails`** - (List of Strings) List of email addresses to alert directly when the check fails (auto-mapped to Atatus notification channels).

- **`channel_ids`** - (List of Strings) List of specific Atatus notification channel IDs to alert on failure.