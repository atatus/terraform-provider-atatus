# Terraform Provider for Atatus

This is the official Terraform Provider to manage resources within the [Atatus](https://www.atatus.com) observability and performance monitoring platform.

This provider enables Infrastructure as Code (IaC) automation for Atatus Synthetic Monitoring and Custom Dashboards, allowing your engineering and DevOps teams to version-control, deploy, and manage global synthetic monitors, dashboards, dashboard groups, powerpacks and dashboard edit restrictions alongside your cloud infrastructure.

## Setup and Authentication

To use this provider, configure your Atatus `api_key`:

```hcl
terraform {
  required_providers {
    atatus = {
      source  = "atatus/atatus"
      version = "~> 1.1" # Any 1.x release from 1.1 on
    }
  }
}

# Configure the Atatus Provider
provider "atatus" {
  api_key = "your_atatus_api_key"
}
```

> **Note:** `api_key` is an Atatus application key (`ak_...`) with Read & Write permission. You can leave it out of the configuration and set the `ATATUS_API_KEY` environment variable instead (and `ATATUS_ENDPOINT` for a self-hosted install). See [docs/index.md](docs/index.md) for retries and the start-up key check.

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
| `atatus_dashboard` | [docs/resources/dashboard.md](docs/resources/dashboard.md) | Manage a Custom Dashboard as typed HCL: every widget type and setting is an attribute. |
| `atatus_dashboard_json` | [docs/resources/dashboard_json.md](docs/resources/dashboard_json.md) | Manage a Custom Dashboard from the JSON document that the dashboard's Export → JSON writes. |
| `atatus_dashboard_group` | [docs/resources/dashboard_group.md](docs/resources/dashboard_group.md) | Manage a dashboard group: a named folder of dashboards, in a set order. |
| `atatus_powerpack` | [docs/resources/powerpack.md](docs/resources/powerpack.md) | Manage a custom powerpack: a reusable group of widgets that dashboards add as linked groups. |
| `atatus_dashboard_restriction` | [docs/resources/dashboard_restriction.md](docs/resources/dashboard_restriction.md) | Restrict who may edit a dashboard to chosen teams and users (plus owners and admins). |

### Data Sources

Look up objects the configuration does not manage, by name (or id):

| Data Source | Guide | Looks up |
|---|---|---|
| `atatus_dashboard` | [docs/data-sources/dashboard.md](docs/data-sources/dashboard.md) | A dashboard by id or exact title (id, url, Spaces, ...). |
| `atatus_dashboards` | [docs/data-sources/dashboards.md](docs/data-sources/dashboards.md) | All dashboards, or those matching a search string. |
| `atatus_dashboard_group` | [docs/data-sources/dashboard_group.md](docs/data-sources/dashboard_group.md) | A dashboard group (folder) by id or exact name, with its dashboards. |
| `atatus_powerpack` | [docs/data-sources/powerpack.md](docs/data-sources/powerpack.md) | A powerpack, yours or from the Atatus catalog, by id or exact name. |
| `atatus_notification_channel` | [docs/data-sources/notification_channel.md](docs/data-sources/notification_channel.md) | A notification channel by exact name, for `channel_ids`. |
| `atatus_project` | [docs/data-sources/project.md](docs/data-sources/project.md) | A monitored project by exact name. |
| `atatus_space` | [docs/data-sources/space.md](docs/data-sources/space.md) | A Space by exact name. |
| `atatus_team` | [docs/data-sources/team.md](docs/data-sources/team.md) | A team by exact name. |
| `atatus_user` | [docs/data-sources/user.md](docs/data-sources/user.md) | An account member by email. |

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

### Managing a Custom Dashboard

Export a board with **Configure → Export Configuration → JSON**, commit the file, and point the resource at it:

```hcl
resource "atatus_dashboard_json" "api_health" {
  dashboard_json = file("${path.module}/dashboards/api-health.json")
}
```

Changes to the file update the board in place. Edits made in the UI show up as drift in `terraform plan`. See [docs/resources/dashboard_json.md](docs/resources/dashboard.md) for import, drift and limits.
