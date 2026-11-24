# Changelog

All notable changes to the Atatus Terraform provider. Versions follow [Semantic Versioning](https://semver.org/).

## 1.1.0 (Unreleased)

NOTES FOR EXISTING CHECK USERS:

* resource/atatus_http_check, atatus_ssl_check, atatus_tcp_check, atatus_dns_check, atatus_icmp_check, atatus_multistep_check: `retry_count` is now validated to 0-3, the server's own limit. A configuration outside that range fails at plan time instead of at apply.
* resource/atatus_http_check, atatus_ssl_check, atatus_tcp_check, atatus_dns_check, atatus_icmp_check, atatus_multistep_check: `locations` defaults to `["US_NEW_YORK"]` when omitted. Before, a check without `locations` planned an update on every run.
* resource/atatus_http_check, atatus_ssl_check, atatus_tcp_check, atatus_dns_check, atatus_icmp_check, atatus_multistep_check: checks can be imported with `terraform import`.
* provider: `api_key` is optional and falls back to the `ATATUS_API_KEY` environment variable. `ATATUS_ENDPOINT` sets the API endpoint.
* provider: `auth_type` and `account_id` are deprecated and ignored. The API key identifies the account, and the public API accepts only API keys sent as a header. Remove both from the provider block.

FEATURES:

* **New Resource:** `atatus_dashboard`: manage a Custom Dashboard as typed HCL. Every widget type and its settings are attributes, checked against the Atatus widget catalogue at plan time.
* **New Resource:** `atatus_dashboard_json`: manage a Custom Dashboard from the JSON document that the dashboard's Export → JSON writes.
* **New Resource:** `atatus_dashboard_group`: a named folder of dashboards, in a set order.
* **New Resource:** `atatus_powerpack`: a reusable group of widgets that dashboards add as linked groups.
* **New Resource:** `atatus_dashboard_restriction`: limit who may edit a dashboard to chosen teams and users. Account owners and admins can always edit.
* **New Data Source:** `atatus_dashboard`, `atatus_dashboards`, `atatus_dashboard_group`, `atatus_powerpack`, `atatus_notification_channel`, `atatus_project`, `atatus_space`, `atatus_team`, `atatus_user`.

ENHANCEMENTS:

* provider: `validate` checks the API key when the provider starts.
* provider: `http_retry_enabled`, `http_retry_max_retries` and `http_retry_timeout`. Rate-limited requests (429) are retried after the server's `Retry-After`. Server errors and network failures are retried only for requests that are safe to repeat, so a create is never sent twice.
* provider: an API key acts with its creator's access, narrowed to the key's Spaces. A key limited to Spaces cannot create account-wide dashboards or groups.
* resource/atatus_dashboard: `terraform plan -generate-config-out` writes the configuration for an existing dashboard after `import`. Keys the typed schema does not model are listed in a warning instead of being dropped silently.
* resource/atatus_dashboard: `template_variable_presets` manages saved variable selections (presets). Applying one sets the variables it lists and puts every other variable back to its saved default. `atatus_dashboard_json` accepts the same `templateVariablePresets`.
* resource/atatus_dashboard: a `topology_map` widget takes `definition.filters` (`env:`, `service:`, `type:`, `subtype:`, and bare words that match service names).
* resource/atatus_dashboard: Atatus writes ready-to-paste configuration for an existing dashboard (Configure → Download dashboard Terraform, or `GET /v2/dashboards/<id>/terraform`): the `import` block plus the whole resource, with no `null` lines.
* resource/atatus_dashboard: tabs are managed by widget position (`tabs[].widgets`), so widget ids never need to appear in configuration.
* resource/atatus_dashboard: the widget catalogue is embedded at build time, so planning never calls the API to read the schema.
* resource/atatus_dashboard_json: concurrent edits are detected by version. A dashboard changed outside Terraform is reported instead of being overwritten.
* resource/atatus_dashboard_json: removing an attribute from the document clears it on the server, and an imported dashboard plans no changes.
* resource/atatus_dashboard_json: the widgets of a linked powerpack instance are ignored, because a powerpack sync rewrites them. `restrictedTo` is ignored too, so it is managed only by `atatus_dashboard_restriction`.
* resource/atatus_dashboard_group: changes made outside Terraform are detected by version and reported as "Changed Outside Terraform" instead of being overwritten.
* resource/atatus_powerpack: drift is tracked by the powerpack's version, because the server rewrites the stored group on save.

BUG FIXES:

* resource/atatus_http_check, atatus_ssl_check, atatus_tcp_check, atatus_dns_check, atatus_icmp_check, atatus_multistep_check: deleting a check that no longer exists succeeds instead of failing.

NOTES:

* `atatus_dashboard` and `atatus_dashboard_json` manage the same kind of object. Use one or the other for a given dashboard, never both.

## 1.0.0

FEATURES:

* **New Resource:** `atatus_http_check`, `atatus_ssl_check`, `atatus_tcp_check`, `atatus_dns_check`, `atatus_icmp_check`, `atatus_multistep_check`: synthetic monitoring checks.
