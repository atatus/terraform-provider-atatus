---
page_title: "atatus_dashboard_group Resource - terraform-provider-atatus"
subcategory: ""
description: |-
  The atatus_dashboard_group resource manages a dashboard group: a named folder of dashboards, in a set order.
---

# `atatus_dashboard_group` Resource

The `atatus_dashboard_group` resource manages a dashboard group: a named folder of dashboards, in a set order. Deleting the group does not delete its dashboards.

## Example Usage

```hcl
data "atatus_space" "payments" {
  name = "Payments"
}

resource "atatus_dashboard_group" "payments" {
  name        = "Payments"
  description = "Boards the payments team owns"
  dashboard_ids = [
    atatus_dashboard_json.checkout.id,
    atatus_dashboard_json.refunds.id,
  ]
  space_ids = [data.atatus_space.payments.id]
}
```

## Argument Reference

- `name` (String, Required) Name of the group.
- `description` (String, Optional) Description of the group. Default: empty.
- `order` (Number, Optional) Position of the group among the account's groups, lowest first. Default: `0`.
- `dashboard_ids` (List of String, Optional) Ids of the group's dashboards, in the order the group shows them. A dashboard may appear only once. Default: none.
- `space_ids` (List of String, Optional) Ids of the Spaces the group is labelled with. Default: none, which makes the group visible account-wide. An API key limited to Spaces must set this to some of its own Spaces.

## Attribute Reference

- `id` (String) Id of the group.
- `version` (Number) The group's version. Every change moves it on.

## How changes work

- **The configuration is the whole group.** Every update sends all five settings, so removing `description`, `order` or `space_ids` from the configuration puts them back to their defaults, and `dashboard_ids` replaces the group's list, in order.
- **Changes made in Atatus are drift.** A rename or a reordered dashboard list shows in the next `terraform plan`, and `terraform apply` puts your configuration back.
- **Concurrent edits are refused.** An update names the version Terraform last read. If the group changed in Atatus after that, the update fails with "Dashboard Group Changed Outside Terraform" instead of overwriting the change. Run `terraform plan` to see the difference and apply again, or `terraform apply -refresh-only` to accept the group as it is.
- **Deleted dashboards.** A dashboard that no longer exists drops out of the group when Terraform reads it, so it shows as a difference. Remove its id from `dashboard_ids` (a missing id makes the update fail with "One or more dashboards not found!").
- **Deleted in the UI.** If the group was deleted outside Terraform, the next plan creates it again. If it is deleted between the plan and the apply, the update fails with "Dashboard Group Deleted Outside Terraform"; apply again to create it.

## Import

A dashboard group is imported by its id, the 24-character hex value in the group's URL in Atatus:

```shell
terraform import atatus_dashboard_group.payments 64b7f0c2e4b0a1b2c3d4e5f6
```
