---
page_title: "atatus_dashboard_json Resource - terraform-provider-atatus"
subcategory: ""
description: |-
  The atatus_dashboard_json resource manages an Atatus Custom Dashboard as a single JSON document.
---

# `atatus_dashboard_json` Resource

The `atatus_dashboard_json` resource manages an Atatus **Custom Dashboard** as a single JSON document.

The document is the file the dashboard's **Configure → Export Configuration → JSON** writes. Every widget type the dashboard supports works, and there is no per-widget schema to learn. You keep the JSON in Git, review changes in pull requests, and apply the same board to several accounts or environments.

## Example Usage

### From an exported file

Export a board as JSON, save it beside your configuration, and reference it with `file()`:

```hcl
resource "atatus_dashboard_json" "api_health" {
  dashboard_json = file("${path.module}/dashboards/api-health.json")
}

output "api_health_url" {
  value = atatus_dashboard_json.api_health.url
}
```

### Inline with `jsonencode`

```hcl
resource "atatus_dashboard_json" "checkout" {
  dashboard_json = jsonencode({
    title       = "Checkout"
    description = "Managed by Terraform"
    widgets = [
      {
        type  = "note"
        title = "Runbook"
        text  = "See the on-call runbook."
      }
    ]
  })
}
```

A widget's `type` and `title` go on the widget itself, as Export → JSON writes them, not inside `definition`. `terraform validate` rejects a widget, or a widget inside a group, that keeps `type` or `title` (or, except on a hostmap, `requests` or `description` without a widget-level copy) inside `definition`, because Atatus stores that shape differently from how it is written.

`terraform validate` also rejects other documents that Atatus would store differently from how they are written, so they fail before anything is saved rather than on apply:

- board or widget keys Atatus does not store (a typo, or a field it ignores), and `snake_case` keys outside `definition` (write `reflowType`, not `reflow_type`);
- `showLegend`, `legendLayout` or `legendColumns` inside `definition` (write `show_legend`, `legend_layout`, `legend_columns`);
- query `filters` given as a list (write one string, such as `"env:prod AND status:ok"`), an upper-case `sort.orderBy[].order`, a request `displayType` of `"bars"`, and the retired `mobile_rum` data sources and metrics (write the `rum_v2` ones);
- a chart marker whose `value` or `to` is an expression string, or that uses `display_type` (write numbers, `severity` and `lineStyle`);
- more than 30 tabs, and a tab name that is empty, has leading or trailing spaces, or is longer than 100 characters.

### Tabbed boards

On a board with tabs, each widget keeps its own `id`, and `tabs[].widgetIds` refer to those ids. Export → JSON writes them for you. When you write the JSON by hand, choose any unique strings. A tab's own `id` is optional:

```json
{
  "title": "Service overview",
  "tabs": [
    { "name": "Latency", "widgetIds": ["latency"] },
    { "name": "Notes",   "widgetIds": ["notes"] }
  ],
  "widgets": [
    { "id": "latency", "type": "timeseries", "title": "Latency" },
    { "id": "notes",   "type": "note", "text": "On call: #ops" }
  ]
}
```

On a tabbed board every widget needs an `id` and must be listed in exactly one tab, and every `widgetIds` entry must name a widget. `terraform validate` checks this, because Atatus would otherwise move or drop the entries and the board would never match the document.

On each apply, Atatus gives every widget, every tab, and (when a board is created) every widget inside a group a new internal id. The provider maps widget and tab ids back to yours by position, and ignores the `_id` of widgets inside a group, so none of this shows as a diff.

### Document schema

The dashboard document is described by a JSON Schema (draft 2020-12) at `GET /v2/dashboards/schema`, using the same application key. It covers every widget type, each type's `definition` keys with their values and defaults, and the request, query and template-variable shapes. Point your editor at a saved copy to get completion and checks while writing a document in its own `.json` file. The schema's `x-schema-version` changes whenever a key or value changes.

## How changes and drift work

- **Plan.** When `dashboard_json` changes, `terraform plan` asks Atatus to validate the document without saving anything. An invalid widget or field fails the plan with the server's message. If that check cannot run (for example, the API is unreachable), the plan prints a warning and the document is validated on apply instead.
- **Update in place.** A change to the JSON replaces the board's content. The board keeps its id and URL, and Atatus adds an entry to its version history. All widgets are recreated.
- **Formatting does not matter.** Key order and whitespace are ignored. So are the fields the server manages: `_id`, `__v`, `accountId`, the author fields, `createdAt`, `updatedAt`, `isFavorited`, `userCanEdit`, `userIsBoardAdmin`, `powerpackStatus`, `isTemplate` and `url` on the board, `_id`, `__v`, `createdAt`, `updatedAt`, `accountId`, `dashboardId` and `data` on widgets, and the `_id` of any nested entry (template variables, group children, list columns and sort entries), which Atatus re-mints when it creates a board. Export → JSON leaves the top-level fields out. Changing only the text layout of the JSON still plans an update, which applies the same board again.
- **Who may edit is separate.** The board's edit restriction (`restrictedTo`) belongs to [`atatus_dashboard_restriction`](dashboard_restriction.md). It is never sent from the document or compared with it.
- **Spaces are tracked.** `spaceIds` is not written by Export → JSON, but when your document sets it, Atatus stores it, and a change to it plans an update.
- **Numbers compare by value.** `4.0` and `4`, or `1e3` and `1000`, are the same number. Numbers are compared as the 64-bit floating-point value Atatus stores, so an integer above 2^53 or a decimal with more than about 17 significant digits compares as its rounded value, and a number too large to store compares as `null`.
- **Empty objects.** Atatus does not store an empty object, so `"definition": {}` (or an empty object nested anywhere) is the same as leaving the key out.
- **Server defaults are not drift.** Atatus fills in default settings you did not write (for example `layoutType`, or defaults inside a widget `definition`). The provider only compares keys that appear in your document, so these defaults never cause a diff.
- **Edits in the UI are drift.** If someone changes a value in your document in the UI, adds or removes a widget, or changes a tab, the next `terraform plan` shows the difference and `terraform apply` puts your version back. A brand-new setting that your document never mentions is not tracked.
- **Removing a board setting clears it.** An update replaces the whole board, so deleting a key such as `description`, `timeDur`, `templateVariables`, `templateVariablePresets` or `isReadOnly` from the JSON clears it on the board. Spaces are the exception: a document without `spaceIds` keeps the board's Spaces (the apply says so), so leaving them out never makes a board account-wide; write `"spaceIds": []` to do that. Widgets and tabs are always replaced in full. (Older Atatus servers kept a removed setting instead; against one of those the apply prints a warning naming the keys it kept.)
- **Variable presets name variables.** Each entry of a `templateVariablePresets` preset (`{ name, templateVariables: [{ name, values }] }`) must name a variable in the same document's `templateVariables`, or the plan fails with Atatus's message. Applying a preset sets the variables it lists and puts every other variable back to its saved default. Removing a variable in Atatus also removes it from the presets, which the next plan shows as drift.
- **Powerpack groups follow their pack.** On a `group` widget linked to a powerpack (`definition.powerpack.id`), the group's `widgets` and `definition.powerpack.version` belong to the pack. Atatus rewrites them on every dashboard when the pack changes, so they are not compared. The link itself, the group's title and layout, and any pinned variables still are. See [Using a powerpack on a dashboard](powerpack.md#using-a-powerpack-on-a-dashboard).
- **Widgets matched by position.** The provider lines widgets up with your document by position. If a widget is added or deleted in the UI, the widget count no longer matches, so the plan shows each widget with its Atatus id instead of yours. Applying restores your document.
- **Concurrent edits.** Every update sends the board version (`version`) that Terraform last read. If the board was changed in Atatus after that, the update is refused rather than overwriting the change. Run `terraform apply -refresh-only` to accept the board as it is now, or run `terraform plan` again to see the difference and re-apply.
- **Deleted in the UI.** If the board was deleted outside Terraform, the next plan creates it again.

## Import

Adopt a board that already exists by its dashboard id (the id in the board's URL):

```shell
terraform import atatus_dashboard_json.api_health 64b7f0c2e4b0a1b2c3d4e5f6
```

The imported `dashboard_json` contains every setting the server returns, including defaults. Empty lists the server stores by default (such as `spaceIds`, `templateVariables`, or a widget's `requests`) are left out of the imported document, so a configuration written from Export → JSON plans no changes after the import. If your document differs from the board in other ways, the first plan shows an in-place update, and applying it writes your document to the board.

## Argument Reference

### Required

- `dashboard_json` (String) The dashboard as a JSON object, in the format Export → JSON writes. Use `file(...)` or `jsonencode(...)`. Must be a JSON object.

## Attribute Reference

- `id` (String) The dashboard id.
- `url` (String) The link that opens the board in Atatus.
- `version` (Number) The board's revision number on the server. Terraform sends it with each update to detect concurrent edits.

## Limits and permissions

- A dashboard can have at most **500 widgets**.
- The public API allows **500 requests per hour per IP address**. Each dashboard costs about one request per refresh, one more when its JSON changes (the plan-time check), and one per create, update or delete. For many dashboards, run Terraform with a lower `-parallelism` or less often.
- The API key must be an **application key with Read and Write** permission.
- A board with `"isReadOnly": true` can be applied only with a key created by an account **owner or admin**; any other key is refused with 403, and `terraform plan` reports it. Remove `isReadOnly` to manage the board as editable.
- An API key works across the whole account: it can read and change dashboards in **every Space**, regardless of Space membership.
