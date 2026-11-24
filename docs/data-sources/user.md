---
page_title: "atatus_user Data Source - terraform-provider-atatus"
subcategory: ""
description: |-
  Looks up a member of the Atatus account by email.
---

# `atatus_user` Data Source

Looks up a member of the Atatus account by email.

## Example Usage

```hcl
data "atatus_user" "ada" {
  email = "ada@example.com"
}
```

## Argument Reference

- `email` (Required) Email of the member (case-insensitive).

## Attribute Reference

- `id` Id of the user.
- `first_name` First name.
- `last_name` Last name.
- `role` `owner`, `admin` or `user`.

The lookup fails when no member has that email.

The lookup sees what the provider's API key can see in its account.
