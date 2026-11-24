#!/usr/bin/env bash
# Refreshes the dashboard schema atatus_dashboard is built from
# (internal/provider/dashboardschema/dashboard.schema.json) from an Atatus API.
#
#   ATATUS_API_KEY=ak_... [ATATUS_ENDPOINT=https://api.atatus.com] scripts/update-dashboard-schema.sh
#
# Then run the tests (TF_ACC=1 go test ./...) and review the diff: a removed or
# renamed attribute is a breaking change for configurations that use it.
set -euo pipefail
: "${ATATUS_API_KEY:?set ATATUS_API_KEY to an application key}"
endpoint="${ATATUS_ENDPOINT:-https://api.atatus.com}"
out="$(cd "$(dirname "$0")/.." && pwd)/internal/provider/dashboardschema/dashboard.schema.json"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
curl -fsS -H "X-API-KEY: ${ATATUS_API_KEY}" "${endpoint%/}/v2/dashboards/schema" -o "$tmp"
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); json.dump(d, open(sys.argv[2], "w"), indent=1, sort_keys=True); open(sys.argv[2], "a").write("\n"); print("dashboard schema", d.get("x-schema-version"))' "$tmp" "$out"
