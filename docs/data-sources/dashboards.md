---
page_title: "atatus_dashboards Data Source - terraform-provider-atatus"
subcategory: ""
description: |-
  Lists the account's dashboards, newest first, optionally only those whose title or description contains a search string.
---

# `atatus_dashboards` Data Source

Lists the account's dashboards, newest first, optionally only those whose title or description contains a search string.

## Example Usage

```hcl
data "atatus_dashboards" "payments" {
  search = "payments"
}

output "payment_dashboard_titles" {
  value = [for d in data.atatus_dashboards.payments.dashboards : d.title]
}
```

## Argument Reference

- `search` (Optional) Only dashboards whose title or description contains this text (case-insensitive).

## Attribute Reference

- `dashboards` The matching dashboards, each with `id`, `title`, `description`, `dashboard_type` and `url`.

The lookup sees what the provider's API key can see in its account.
