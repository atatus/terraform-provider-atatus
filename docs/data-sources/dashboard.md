---
page_title: "atatus_dashboard Data Source - terraform-provider-atatus"
subcategory: ""
description: |-
  Looks up an existing Atatus dashboard by id or exact title.
---

# `atatus_dashboard` Data Source

Looks up an existing Atatus dashboard by id or exact title.

## Example Usage

```hcl
data "atatus_dashboard" "checkout" {
  title = "Checkout overview"
}

output "checkout_dashboard_url" {
  value = data.atatus_dashboard.checkout.url
}
```

## Argument Reference

- `id` (Optional) Id of the dashboard. Set this or `title`.
- `title` (Optional) Exact title. It must match exactly one dashboard; when several share a title, look it up by `id`.

## Attribute Reference

- `description` Description of the dashboard.
- `dashboard_type` `custom_timeboard` or `custom_screenboard`.
- `layout_type` `ordered` or `free`.
- `space_ids` Ids of the Spaces the dashboard is labelled with.
- `is_read_only` Whether only owners and admins may edit it.
- `url` Link to the dashboard in Atatus.

A name lookup fails when nothing matches, and when more than one object matches; look it up by `id` instead.

The lookup sees what the provider's API key can see in its account.
