# Terraform Provider for Atatus

This is the official Terraform Provider to manage resources within the [Atatus](https://www.atatus.com) observability and performance monitoring platform.

This provider enables Infrastructure as Code (IaC) automation for Atatus Synthetic Monitoring, allowing your engineering and DevOps teams to version-control, deploy, and manage global synthetic monitors alongside your cloud infrastructure.

## Setup and Authentication

To use this provider, configure your Atatus `api_key`:

```hcl
terraform {
  required_providers {
    atatus = {
      source  = "atatus/atatus"
      version = "0.1.0"      # Constrains to compatible versions (e.g., 0.1.x)
    }
  }
}

# Configure the Atatus Provider
provider "atatus" {
  api_key = "your_atatus_api_key"
}
```

> **Note:** By default, `auth_type` is `"header"`, which authenticates using your API key via the `X-API-KEY` header. If using cookie-based session authentication, set `auth_type = "cookie"` and provide the session token in `api_key`.

## Supported Resources & Documentation

Detailed documentation and examples for each synthetic check resource are available in their dedicated guides.

| Resource | Dedicated Guide | Description |
|---|---|---|
| `atatus_http_check` | [docs/resources/http_check.md](docs/resources/http_check.md) | Monitor HTTP/API endpoints, uptime, response codes, headers, response latency, and JSONPath/XPath payloads. |
| `atatus_ssl_check` | [docs/resources/ssl_check.md](docs/resources/ssl_check.md) | Monitor SSL/TLS certificate validity, expiration warnings, minimum/maximum TLS versions, and handshake response times. |
| `atatus_tcp_check` | [docs/resources/tcp_check.md](docs/resources/tcp_check.md) | Verify TCP port connectivity and network latency for services such as PostgreSQL, MySQL, Redis, SSH, and web servers. |
| `atatus_dns_check` | [docs/resources/dns_check.md](docs/resources/dns_check.md) | Monitor DNS resolution speeds and validate DNS record sets (A, AAAA, CNAME, MX, NS, TXT). |
| `atatus_icmp_check` | [docs/resources/icmp_check.md](docs/resources/icmp_check.md) | Monitor network ping latency (average, minimum, maximum, standard deviation), packet loss, and packets received from global locations. |
| `atatus_multistep_check` | [docs/resources/multistep_check.md](docs/resources/multistep_check.md) | Monitor multi-step API transactions and sequential user journeys with custom assertions per step. |

## 🌍 Supported Location Codes Reference

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

## Quick Example

### Creating an HTTP Synthetic Check

```hcl
resource "atatus_http_check" "api_uptime" {
  name      = "Production API Health"
  url       = "https://api.example.com/health"
  method    = "GET"
  frequency = "1m"
  locations = ["US_NEW_YORK", "EU_FRANKFURT"]
  emails    = ["devops@example.com"]

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
}
```