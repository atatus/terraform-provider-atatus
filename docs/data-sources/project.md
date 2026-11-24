---
page_title: "atatus_project Data Source - terraform-provider-atatus"
subcategory: ""
description: |-
  Looks up a monitored project (APM, Browser, ...) by exact name.
---

# `atatus_project` Data Source

Looks up a monitored project (APM, Browser, ...) by exact name.

## Example Usage

```hcl
data "atatus_project" "checkout" {
  name = "checkout"
  type = "APM"
}
```

## Argument Reference

- `name` (Required) Exact name of the project.
- `type` (Optional) Project type (for example `APM` or `Browser`), to pick between projects that share a name. Case-insensitive.

## Attribute Reference

- `id` Id of the project.
- `sub_type` The project's agent or platform (for example `Node.js`).

The lookup fails when no project has that name, or when several do and `type` does not single one out.

The lookup sees what the provider's API key can see in its account.
