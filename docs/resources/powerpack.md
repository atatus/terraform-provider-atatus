---
page_title: "atatus_powerpack Resource - terraform-provider-atatus"
subcategory: ""
description: |-
  The atatus_powerpack resource manages a custom powerpack: a reusable group of widgets that dashboards add as linked groups.
---

# `atatus_powerpack` Resource

The `atatus_powerpack` resource manages a custom powerpack: a reusable group of widgets that dashboards add as linked groups. When the pack's layout or variables change, Atatus updates every dashboard that uses it.

Powerpacks from the Atatus catalog cannot be managed. Refer to them with the [`atatus_powerpack` data source](../data-sources/powerpack.md).

## Example Usage

```hcl
data "atatus_user" "ada" {
  email = "ada@example.com"
}

resource "atatus_powerpack" "service_health" {
  name        = "Service health"
  description = "Throughput and errors for one service"
  tags        = ["apm"]
  editors     = [data.atatus_user.ada.id]

  template_variables = [{
    name     = "service"
    prefix   = "service"
    defaults = ["checkout"]
  }]

  group_widget_json = jsonencode({
    title     = "Service health"
    showTitle = true
    widgets = [
      {
        type       = "note"
        title      = "About"
        definition = { powerpackKey = "about", content = "Throughput and errors for $service" }
        layout     = { x = 0, y = 0, width = 4, height = 2 }
      },
    ]
  })
}
```

## Argument Reference

- `name` (String, Required) Name of the powerpack, without leading or trailing spaces.
- `group_widget_json` (String, Required) The pack's group as a JSON object:
  - `title` and `showTitle`;
  - `definition`, the header settings: `headerStyle` (`default`, `emphasis` or `banner`), `backgroundColor`, `bannerImg` and `titleAlign`;
  - `widgets`: 1 to 100 widgets, each with a `layout` of `{x, y, width, height}` inside a 12-column grid.

  A pack cannot contain groups or split graphs. Key order and whitespace in what Atatus returns never show as drift; changing only the formatting of the configuration still plans an update.
- `description` (String, Optional) Description of the powerpack, at most 80 characters. Default: empty.
- `tags` (List of String, Optional) At most 8 tags, each at most 80 characters. Default: none.
- `editors` (List of String, Optional) Ids of up to 50 users who may change the pack, besides its author and the account's owners and admins. Only the author or an admin may change this list. Default: none.
- `template_variables` (List of Object, Optional) Variables the pack's widgets filter on. Default: none. Each has:
  - `name` (String, Required) Name of the variable: letters, digits, `_`, `-` and `.`.
  - `prefix` (String, Required) The attribute it filters on, such as `service` or `host`.
  - `defaults` (List of String, Optional) Values selected by default. Default: none, meaning all values.
  - `available_values` (List of String, Optional) Values the variable may take. Default: none, meaning any value.

## Attribute Reference

- `id` (String) Id of the powerpack.
- `version` (Number) The layout version dashboards link to. It moves on every change to `group_widget_json` or `template_variables`.
- `revision` (Number) Moves on every change to the powerpack.

## How changes work

- **The configuration is the whole powerpack.** Every update sends all its settings.
- **Widget identity.** Give each widget a `definition.powerpackKey`. Atatus matches a pack's widgets to the widgets on each dashboard by that key. A widget without a key is numbered by its position, so reordering keyless widgets changes which is which.
- **Dashboards follow the pack.** A change to `group_widget_json` or `template_variables` updates every dashboard that uses the pack.
  - A read-only dashboard is updated only when the API key's creator is an owner or admin. Otherwise Terraform warns and lists the dashboards left on the old version; an owner or admin can update them from the dashboard.
  - A change to only the name, description, tags or editors leaves dashboards untouched.
- **Changes made in Atatus are drift.**
  - Atatus rewrites the group it stores: it adds a `powerpackKey` to each widget and fills in defaults. So Terraform does not compare `group_widget_json` with what Atatus returns; it watches `version` instead.
  - While `version` is still the one Terraform wrote, your configuration stands.
  - When someone changes the layout in Atatus, the next plan shows the pack's current group, and `terraform apply` puts your configuration back.
  - Changes to the name, description, tags, editors and variables show directly.
- **Concurrent edits are refused.** An update names the revision Terraform last read. If the powerpack changed in Atatus after that, the update fails with "Powerpack Changed Outside Terraform" instead of overwriting the change. Run `terraform plan` to see the difference and apply again, or `terraform apply -refresh-only` to accept it.
- **Deleting a powerpack** keeps it on the dashboards that use it, where it shows as deleted. If it was deleted outside Terraform, the next plan creates a new one.

## Using a powerpack on a dashboard

A dashboard uses a powerpack through a `group` widget whose `definition.powerpack` names it. In `atatus_dashboard_json`, take the pack's widgets and version from the resource:

```hcl
locals {
  health = jsondecode(atatus_powerpack.service_health.group_widget_json)
}

resource "atatus_dashboard_json" "checkout" {
  dashboard_json = jsonencode({
    title         = "Checkout"
    dashboardType = "custom_timeboard"
    layoutType    = "ordered"
    widgets = [{
      type       = "group"
      title      = local.health.title
      definition = { powerpack = { id = atatus_powerpack.service_health.id, version = atatus_powerpack.service_health.version } }
      widgets    = local.health.widgets
      layout     = { x = 0, y = 0, width = 12, height = 3 }
    }]
  })
}
```

The powerpack owns the group's `widgets` and `definition.powerpack.version`. When the pack changes, Atatus updates them on the dashboard, and the dashboard resource does not count that as drift. The link, the group's title and layout, and any pinned variables stay with the dashboard.

## Import

A powerpack is imported by its id, the 24-character hex value Atatus shows for a custom pack:

```shell
terraform import atatus_powerpack.service_health 64b7f0c2e4b0a1b2c3d4e5f6
```

The imported `group_widget_json` is the group as Atatus stores it, including each widget's `powerpackKey`. If your configuration describes the same group (with the same keys), the plan after import is clean. Otherwise copy the imported group into your configuration (`terraform state show`).
