---
page_title: "atatus_powerpack Data Source - terraform-provider-atatus"
subcategory: ""
description: |-
  Looks up a powerpack (a reusable group of widgets), yours or one from the Atatus catalog, by id or exact name.
---

# `atatus_powerpack` Data Source

Looks up a powerpack (a reusable group of widgets), yours or one from the Atatus catalog, by id or exact name.

## Example Usage

```hcl
data "atatus_powerpack" "hosts" {
  name = "Hosts overview"
}
```

## Argument Reference

- `id` (Optional) Id of the powerpack: a 24-character hex id for one made in your account, a catalog id (for example `hosts-overview`) for an Atatus one. Set this or `name`.
- `name` (Optional) Exact name. It must match exactly one powerpack.

## Attribute Reference

- `description` Description of the powerpack.
- `source` `custom` (made in your account) or `preset` (from the Atatus catalog).
- `tags` The powerpack's tags.
- `version` Layout version of the powerpack.

A name lookup fails when nothing matches, and when more than one object matches; look it up by `id` instead.

The lookup sees what the provider's API key can see in its account.
