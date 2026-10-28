---
page_title: "Provider: Atatus"
subcategory: ""
description: |-
  The Atatus provider is used to interact with Atatus Observability and Synthetic Monitoring.
---

# Atatus Provider

The Atatus provider allows managing synthetic monitoring checks in the [Atatus](https://www.atatus.com) observability platform using Terraform.

## Example Usage

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


## Authentication

By default, authentication is performed via API Key using the `X-API-KEY` HTTP header (`auth_type = "header"`).

```hcl
provider "atatus" {
  api_key = "your_atatus_api_key"
}
```

If you use session cookie authentication, set `auth_type = "cookie"`:

```hcl
provider "atatus" {
  api_key   = "your_session_cookie"
  auth_type = "cookie"
}
```

## Schema

### Required

- `api_key` (String, Sensitive) Atatus API Key or session cookie string.

### Optional

- `account_id` (String) Your Atatus Account ID (e.g., `1234567890abcdef12345678`). Optional for v2 API.
- `auth_type` (String) Authentication type: `'header'` (default) or `'cookie'`.
