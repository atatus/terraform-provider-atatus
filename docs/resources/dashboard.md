---
page_title: "atatus_dashboard Resource - terraform-provider-atatus"
subcategory: ""
description: |-
  The atatus_dashboard resource manages an Atatus Custom Dashboard written as typed HCL.
---

# `atatus_dashboard` Resource

The `atatus_dashboard` resource manages an Atatus Custom Dashboard written as typed HCL: every widget type, setting and query is an attribute, so `terraform validate` checks names and values as you write them, and plans show exactly which setting changes.

The attributes come from the dashboard schema Atatus publishes (`GET /v2/dashboards/schema`), pinned in the provider. Every widget type Atatus has is available.

To keep a dashboard as the JSON document Export → JSON writes, use [`atatus_dashboard_json`](dashboard_json.md) instead. It also covers settings this resource has no attribute for (see Import).

## Example Usage

```hcl
data "atatus_team" "sre" {
  name = "SRE"
}

resource "atatus_dashboard" "checkout" {
  title       = "Checkout service"
  description = "Owned by the payments team"

  template_variables = [
    { name = "service", prefix = "service", default = ["checkout"] },
  ]

  # Saved selections viewers apply from the variable bar's Presets menu.
  template_variable_presets = [
    { name = "Cart", template_variables = [{ name = "service", values = ["cart"] }] },
  ]

  widgets = [
    {
      timeseries = {
        title  = "Requests per minute"
        layout = { x = 0, y = 0, width = 6, height = 3 }
        requests = [{
          display_type = "line"
          queries = [{
            name        = "q"
            data_source = "metrics"
            query       = "sum:http.requests{service:$service} by {status}"
          }]
          formulas = [{ formula = "q", alias = "Requests" }]
        }]
        definition = {
          legend_mode = "compact"
          markers     = [{ type = "line", value = 1000, severity = "warning", label = "SLO" }]
        }
      }
    },
    {
      query_value = {
        title = "Error rate"
        requests = [{
          queries  = [{ name = "e", data_source = "logs", compute = { aggregation = "count", metric_name = "logs.count" } }]
          formulas = [{ formula = "e" }]
        }]
        definition = { precision = 2 }
      }
    },
    {
      group = {
        title      = "Notes"
        definition = { header_style = "emphasis" }
        widgets = [
          { note = { definition = { content = "**Runbook:** https://wiki.example.com/checkout" } } },
        ]
      }
    },
  ]

  tabs = [
    { name = "Overview", widgets = [1, 2] },
    { name = "Notes", widgets = [3] },
  ]
}

resource "atatus_dashboard_restriction" "checkout" {
  dashboard_id = atatus_dashboard.checkout.id
  editors      = ["team:${data.atatus_team.sre.id}"]
}
```

## Argument Reference

The board:

- `title` (String, Required) Dashboard title.
- `widgets` (List of Object, Required) The widgets, in order, at most 500. Each widget sets exactly one widget type, e.g. `{ timeseries = { ... } }`.
- `description`, `dashboard_type` (`custom_timeboard` or `custom_screenboard`), `layout_type` (`ordered` with a timeboard, `free` with a screenboard), `reflow_type`, `is_global_time_selectable`, `time_dur` (default time range, e.g. `1h`), `is_read_only`, `space_ids` (Optional). Leaving `space_ids` out keeps the board's current Spaces; `space_ids = []` makes it visible account-wide.
- `template_variables` (List of Object, Optional) Variables the widgets filter on: `name`, `prefix` (the attribute it filters on), `default`, `available_values`.
- `template_variable_presets` (List of Object, Optional) Saved variable selections, at most 50: each `{ name, template_variables = [{ name, values }] }`. Applying a preset sets each variable it lists to its `values` (an empty list means all values) and every variable it does not list back to its saved `default`, so a preset always shows the same view. Names are unique (compared without surrounding spaces), and every entry must name a variable in `template_variables`; renaming a variable means renaming it in the presets too. Removing a variable in Atatus also removes it from the presets.
- `tabs` (List of Object, Optional) Each tab is `{ name, widgets }`, where `widgets` lists 1-based positions in `widgets`. On a tabbed dashboard every widget is on exactly one tab; `terraform validate` checks this.

A widget (one per entry of `widgets`) has the widget type as its only key. Its object holds the widget's own settings:

- `title`, `title_size`, `title_align`, `description`, `time` (its own time range), `layout` (`{ x, y, width, height }` on the 12-column grid; absent on an ordered board places it after the others).
- `requests` (types that query data) — each `{ queries, formulas, sort, style, display_type, on_right_yaxis }`; a query is `{ name, data_source, query | metric_name | compute, filters, group_by, ... }`. `response_format` is filled in from the widget type when left out. A `list_stream` or `point_plot` request is `{ query = { data_source, type, columns, sort, ... } }`.
- `definition` — the type's settings, e.g. `legend_mode` and `markers` for a timeseries, `pie` for a sunburst, `content` for a note.
- `widgets` (a `group` only) — the widgets inside it (any type but `group`).

A `topology_map` (service map) narrows with `definition.filters`, a filter string such as `"env:production type:db checkout"`. The service map records only environments, node names and node types, so only these keys are accepted; any other key fails the apply with Atatus's message:

- `env:` exact environment names (several mean any of them; no wildcards, and it cannot be excluded). It is combined with `definition.env` when both are set.
- `service:` node names; `*` is a wildcard.
- `type:` the node type: `app`; `db`, which covers databases and messaging destinations; `messaging`, which matches only messaging sources; `external`, present only when `external = true`.
- `subtype:` the node subtype, such as `mysql` or `redis`.
- A bare word matches node names containing it, ignoring case. `-key:value` hides matching nodes.

Several values of one key mean any of them, and different keys must all match. The map shows the matching nodes with their direct callers and callees.

Names are the dashboard document's keys in snake_case (`yBuckets` is `y_buckets`). A setting whose value is free-form is a `*_json` attribute holding JSON, written with `jsonencode(...)` (for example `yaxis = { min_json = jsonencode(0) }`). Run `terraform providers schema -json` for every attribute, or read the dashboard schema.

## Attribute Reference

- `id` (String) The dashboard id.
- `url` (String) Link that opens the dashboard in Atatus.
- `version` (Number) The board's version, sent on update so a board changed in Atatus since the last refresh is not overwritten.

## How changes and drift work

- **Plan-time validation.** When the dashboard changes, `terraform plan` asks Atatus to validate it without saving; an invalid widget fails the plan with Atatus's message.
- **Updates replace the board.** An update replaces the board's content, as `atatus_dashboard_json` does: settings removed from the configuration are cleared, and every widget is recreated (with a new internal id).
- **Only what you configure is tracked.** Settings Atatus fills in that your configuration does not set never cause a diff. A setting you configure that is changed in Atatus shows in the next plan, and `terraform apply` puts it back. A widget added or removed in Atatus shows as a change to `widgets`.
- **Query buckets.** A query's `bucket` (in `compute`, or on the query for metrics, analytics and process data) is in milliseconds; `0` means automatic, sized to the time range the dashboard is viewed with. Atatus stores it as written. Boards saved before this was fixed may hold a rewritten bucket; the configured value stands and is not reported as drift.
- **Concurrent edits.** An update names the version Terraform last read. If the board changed in Atatus since, the update fails with "Dashboard Changed Outside Terraform" rather than overwrite it. Run `terraform plan` to see the difference, or `terraform apply -refresh-only` to accept the board as it is.
- **Who may edit** is managed with [`atatus_dashboard_restriction`](dashboard_restriction.md); this resource never changes it.

## Import

Import a dashboard by its id, the 24-character hex value in its URL. With Terraform 1.5 or later, let Terraform write the configuration:

```hcl
import {
  to = atatus_dashboard.checkout
  id = "64b7f0c2e4b0a1b2c3d4e5f6"
}
```

```shell
terraform plan -generate-config-out=checkout.tf
```

The generated configuration describes the board as Atatus stores it, defaults included, and lists every unset attribute as `null` (including the widget types each widget does not use); remove those lines as you tidy it. Atatus can also write the configuration for you, without the `null` lines: on the dashboard, choose Configure → Download dashboard Terraform (the "Terraform (HCL)" tab), or call `GET /v2/dashboards/<id>/terraform?format=hcl` with your API key. The text holds the `import` block and the whole resource, so `terraform plan` adopts the board with no changes. Dashboards with the same title get the same resource name; rename one when you export them together.

The classic command works too:

```shell
terraform import atatus_dashboard.checkout 64b7f0c2e4b0a1b2c3d4e5f6
```

If the board has settings this resource has no attribute for (written by a newer Atatus, or by hand), the import warns and names them: they would be removed by the next apply. Manage such a board with `atatus_dashboard_json`, which keeps the whole document.
