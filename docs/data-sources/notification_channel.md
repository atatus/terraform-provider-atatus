---
page_title: "atatus_notification_channel Data Source - terraform-provider-atatus"
subcategory: ""
description: |-
  Looks up a notification channel by exact name, for a synthetic check's `channel_ids`.
---

# `atatus_notification_channel` Data Source

Looks up a notification channel by exact name, for a synthetic check's `channel_ids`.

## Example Usage

```hcl
data "atatus_notification_channel" "alerts" {
  name = "#alerts"
  type = "SLACK"
}

resource "atatus_http_check" "api" {
  name        = "API health"
  url         = "https://api.example.com/health"
  channel_ids = [data.atatus_notification_channel.alerts.id]
}
```

## Argument Reference

- `name` (Required) Exact name of the channel.
- `type` (Optional) Channel type (for example `EMAIL` or `SLACK`), to pick between channels that share a name. Case-insensitive.

## Attribute Reference

- `id` Id of the channel.

The lookup fails when no channel has that name, or when several do and `type` does not single one out.

The lookup sees what the provider's API key can see in its account.
