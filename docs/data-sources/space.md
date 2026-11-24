---
page_title: "atatus_space Data Source - terraform-provider-atatus"
subcategory: ""
description: |-
  Looks up an Atatus Space by exact name, for example to label a dashboard with its `spaceIds`.
---

# `atatus_space` Data Source

Looks up an Atatus Space by exact name, for example to label a dashboard with its `spaceIds`.

## Example Usage

```hcl
data "atatus_space" "payments" {
  name = "Payments"
}
```

## Argument Reference

- `name` (Required) Exact name of the Space.

## Attribute Reference

- `id` Id of the Space.
- `description` Description of the Space.

The lookup fails when no Space, or more than one, has that name.

The lookup sees what the provider's API key can see in its account.
