---
page_title: "Provider: Atatus"
description: |-
  The Atatus provider is used to interact with Atatus Observability and Synthetic Monitoring.
---

# Atatus Provider

The Atatus provider allows managing synthetic monitoring checks and Custom Dashboards in the [Atatus](https://www.atatus.com) observability platform using Terraform.

## Example Usage

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
  api_key = var.atatus_api_key
}
```

## Authentication

The provider authenticates with an Atatus **application key** (`ak_...`) that has **Read & Write** permission, created under Settings → API Keys. A read-only key cannot create or change anything, and a write-only key cannot read objects back.

To keep the key out of your configuration, leave `api_key` unset and export it instead:

```shell
export ATATUS_API_KEY="ak_..."
```

When the provider starts it checks the key with one read of the account's notification channels, so a wrong key or endpoint fails before Terraform plans anything. Set `validate = false` to skip that check.

### What a key can reach

- **A key acts as the person who created it.** In an account that uses Spaces and roles, Terraform can see and change only what that person can in the app. A key made by an owner reaches everything.
- **A key can be limited to Spaces** (Settings → API Keys → Spaces). It then reaches only the dashboards, dashboard groups and other saved items that carry one of its Spaces or no Space at all.
  - Everything it creates is labelled with its Spaces, unless you set `space_ids` / `spaceIds` to some of them.
  - It cannot make anything visible account-wide (`space_ids = []`) or label it with another Space. That fails with a 403.
  - With `atatus_dashboard_group`, set `space_ids` to one of the key's Spaces, because its default is account-wide.
- **Changes to a key apply within a minute.** This covers its Spaces, and changes to its creator's access.
- **Give a pipeline its own key.** Use a Space-limited key per team, so one team's configuration cannot change another team's dashboards.

## API Endpoint

Requests go to `https://www.atatus.com` unless you set `endpoint` (or `ATATUS_ENDPOINT`):

```hcl
provider "atatus" {
  endpoint = "https://atatus.example.internal"
}
```

## Retries

Requests the API throttles (HTTP 429) are retried, after the server's `Retry-After` when it sends one, otherwise with exponential backoff. Server errors (HTTP 5xx) and network errors are retried only for reads, updates and deletes; a create is never repeated after the server received it, so a retry cannot create a duplicate. Tune this with `http_retry_enabled`, `http_retry_max_retries` and `http_retry_timeout`.

## Schema

### Optional

- `api_key` (String, Sensitive) Atatus application key (`ak_...`) with Read & Write permission. Required unless the `ATATUS_API_KEY` environment variable is set.
- `endpoint` (String) Base URL of the Atatus API. Defaults to `https://www.atatus.com`, or the `ATATUS_ENDPOINT` environment variable. Must start with `http://` or `https://`; a trailing slash is ignored. Set it to use another Atatus installation, such as a staging or self-hosted one. All resources send their requests to this URL.
- `validate` (Boolean) Check the API key when the provider starts. Default: `true`.
- `http_retry_enabled` (Boolean) Retry throttled requests and safe-to-repeat failed requests. Default: `true`.
- `http_retry_max_retries` (Number) Most retries for one request, `1` to `5`. Default: `3`.
- `http_retry_timeout` (Number) Seconds one request may take, including retries and the waits between them. Default: `60`.
- `account_id` (String, Deprecated) Not used: the API key identifies the account. Remove it from the provider block.
- `auth_type` (String, Deprecated) The public API accepts only API keys sent as a header, so `"cookie"` cannot authenticate. Remove it from the provider block.
