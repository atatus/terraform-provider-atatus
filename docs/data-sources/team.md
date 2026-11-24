---
page_title: "atatus_team Data Source - terraform-provider-atatus"
subcategory: ""
description: |-
  Looks up an Atatus team by exact name.
---

# `atatus_team` Data Source

Looks up an Atatus team by exact name.

## Example Usage

```hcl
data "atatus_team" "sre" {
  name = "SRE"
}
```

## Argument Reference

- `name` (Required) Exact name of the team.

## Attribute Reference

- `id` Id of the team.
- `description` Description of the team.

The lookup fails when no team, or more than one, has that name.

The lookup sees what the provider's API key can see in its account.
