---
page_title: "atatus_dashboard_restriction Resource - terraform-provider-atatus"
subcategory: ""
description: |-
  The atatus_dashboard_restriction resource restricts who may edit an Atatus dashboard to the listed teams and users, plus the account's owners and admins.
---

# `atatus_dashboard_restriction` Resource

The `atatus_dashboard_restriction` resource restricts who may edit an Atatus dashboard. Only the listed teams and users can change it, plus the account's owners and admins. Everyone who can see the dashboard can still view it. Destroying the resource lifts the restriction.

The restriction is kept apart from the dashboard's content: `atatus_dashboard_json` never sends or compares it. So the dashboard and who may change it can live in different configurations, for example a platform team's configuration owning access to every team's dashboards.

## Example Usage

```hcl
data "atatus_team" "sre" {
  name = "SRE"
}

data "atatus_user" "ada" {
  email = "ada@example.com"
}

resource "atatus_dashboard_restriction" "checkout" {
  dashboard_id = atatus_dashboard_json.checkout.id
  editors = [
    "team:${data.atatus_team.sre.id}",
    "user:${data.atatus_user.ada.id}",
  ]
}
```

## Argument Reference

- `dashboard_id` (String, Required) Id of the dashboard to restrict. Changing it moves the restriction, and the previous dashboard's restriction is lifted.
- `editors` (Set of String, Required) Who may edit the dashboard: 1 to 50 entries, each `team:<team id>` or `user:<user id>`. Look the ids up with [`atatus_team`](../data-sources/team.md) and [`atatus_user`](../data-sources/user.md). A team must be active and a user must be a member of the account.

## Attribute Reference

- `id` (String) The dashboard id.

## How it works

- **What is restricted.** Every change to the dashboard is restricted: its settings, widgets, tabs, adding a powerpack, saving one of its groups as a powerpack, changes made through Ask AI, and deleting it. When someone outside `editors` changes a powerpack, its copy on this dashboard is not updated (the powerpack update reports the dashboard as skipped) until someone who may edit the dashboard updates the pack. Viewing, sharing and scheduled reports are not restricted.
- **Owners and admins** can always edit the dashboard and change its restriction.
- **Changing the restriction** needs the same rights as editing the dashboard, plus the dashboard share permission. So someone the dashboard is restricted against cannot lift the restriction.
- **API keys** act as the person who created them. A key made by someone outside `editors` cannot change the dashboard; a key made by an owner or admin always can, including one limited to Spaces (it still only reaches dashboards in its Spaces).
- **When it applies.** The restriction is enforced in accounts that use Spaces and roles. It can be set in any account and takes effect once roles are turned on.
- **Drift.** A restriction changed or lifted in Atatus shows in the next `terraform plan`, and `terraform apply` puts yours back. Changing the restriction does not change the dashboard's version, so it never causes a "Dashboard Changed Outside Terraform" conflict on `atatus_dashboard_json`.
- **Deleted dashboards.** If the dashboard is deleted, the restriction goes with it, and the resource leaves the state.

## Import

A restriction is imported by its dashboard's id:

```shell
terraform import atatus_dashboard_restriction.checkout 64b7f0c2e4b0a1b2c3d4e5f6
```
