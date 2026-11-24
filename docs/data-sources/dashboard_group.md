---
page_title: "atatus_dashboard_group Data Source - terraform-provider-atatus"
subcategory: ""
description: |-
  Looks up a dashboard group (a folder of dashboards) by id or exact name.
---

# `atatus_dashboard_group` Data Source

Looks up a dashboard group (a folder of dashboards) by id or exact name.

## Example Usage

```hcl
data "atatus_dashboard_group" "platform" {
  name = "Platform"
}
```

## Argument Reference

- `id` (Optional) Id of the group. Set this or `name`.
- `name` (Optional) Exact name. It must match exactly one group.

## Attribute Reference

- `description` Description of the group.
- `dashboard_ids` Ids of the group's dashboards, in the group's order.
- `space_ids` Ids of the Spaces the group is labelled with.

A name lookup fails when nothing matches, and when more than one object matches; look it up by `id` instead.

The lookup sees what the provider's API key can see in its account.
