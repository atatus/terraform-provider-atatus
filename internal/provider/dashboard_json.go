package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Server-managed keys that are not part of a dashboard's definition.
//
// These follow DASHBOARD_KEYS / WIDGET_KEYS in
// atatus-dashboard/react-app/src/app/features/dashboards/shared/lib/export-dashboard-json.ts
// (toExportableDashboard), except "spaceIds": Export → JSON leaves it out, but
// the server writes it from the document on create and update
// (DASHBOARD_WRITABLE_FIELDS), so a document that sets it is tracked.
var (
	dashboardServerKeys = []string{
		"__v", "_id", "accountId", "authorId", "authorName", "authorEmail", "createdAt", "updatedAt",
		"isFavorited", "userCanEdit", "userIsBoardAdmin", "powerpackStatus", "isTemplate", "url",
		// Who may edit the board: owned by atatus_dashboard_restriction, never by the document.
		"restrictedTo",
	}
	widgetServerKeys = []string{"__v", "_id", "createdAt", "updatedAt", "accountId", "dashboardId", "data"}

	// atatus-server dashboard-v2/helper/index.js DASHBOARD_WRITABLE_FIELDS /
	// WIDGET_WRITABLE_FIELDS: the only keys the server stores.
	dashboardWritableKeys = []string{
		"title", "description", "dashboardType", "layoutType", "reflowType", "isGlobalTimeSelectable", "timeDur",
		"isReadOnly", "isFavorited", "spaceIds", "templateVariables", "templateVariablePresets",
	}
	widgetWritableKeys = []string{
		"type", "title", "titleSize", "titleAlign", "description", "definition", "requests", "time", "layout",
		"widgets", "timeseriesBackground", "showLegend", "legendLayout", "legendColumns", "color", "fontSize",
		"text", "textAlign",
	}
)

// decodeJSONObject parses s as exactly one JSON object. Numbers stay
// json.Number so they re-encode byte-for-byte (no float64 rounding).
func decodeJSONObject(s string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("not valid JSON: unexpected data after the top-level value")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("must be a JSON object ({...})")
	}
	return obj, nil
}

// marshalCanonical encodes v compactly with object keys sorted (encoding/json
// sorts map keys) and without HTML escaping.
func marshalCanonical(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// stripServerKeys removes the server-managed board and widget keys in place.
// A widget's "id" is not removed: tabs[].widgetIds point at it.
func stripServerKeys(doc map[string]any) {
	for _, k := range dashboardServerKeys {
		delete(doc, k)
	}
	widgets, _ := doc["widgets"].([]any)
	for _, w := range widgets {
		if wm, ok := w.(map[string]any); ok {
			for _, k := range widgetServerKeys {
				delete(wm, k)
			}
		}
	}
}

// stripGroupChildServerKeys removes the widget server keys from group
// children (widgets[].widgets[]). Export → JSON keeps a child's "_id", but the
// server mints new child _ids when it creates a board, so they are compared
// without them.
func stripGroupChildServerKeys(doc map[string]any) {
	widgets, _ := doc["widgets"].([]any)
	for _, w := range widgets {
		wm, _ := w.(map[string]any)
		children, _ := wm["widgets"].([]any)
		for _, c := range children {
			if cm, ok := c.(map[string]any); ok {
				for _, k := range widgetServerKeys {
					delete(cm, k)
				}
			}
		}
	}
}

// dropPowerpackOwnedContent removes, from every group linked to a powerpack
// (definition.powerpack.id), what the pack owns: its children and the linked
// definition.powerpack.version. When the pack's layout changes Atatus rewrites
// both on every dashboard using it, which must not read as drift on the
// dashboard (applying would put the old layout back). The link itself, the
// group's title, layout and pinned variables stay compared.
func dropPowerpackOwnedContent(doc map[string]any) {
	widgets, _ := doc["widgets"].([]any)
	for _, w := range widgets {
		wm, _ := w.(map[string]any)
		def, _ := wm["definition"].(map[string]any)
		link, _ := def["powerpack"].(map[string]any)
		if id, _ := link["id"].(string); id == "" {
			continue
		}
		delete(wm, "widgets")
		delete(link, "version")
	}
}

// stripSubdocumentIDs removes "_id" at every depth. The server's create drops
// every "_id" in the body (convertKeysToCamelCase) and Mongo mints new ones for
// subdocument arrays such as templateVariables[] and requests[].query.sort[] /
// columns[], so Export → JSON's nested _ids never come back as written.
func stripSubdocumentIDs(v any) {
	switch x := v.(type) {
	case map[string]any:
		delete(x, "_id")
		for _, val := range x {
			stripSubdocumentIDs(val)
		}
	case []any:
		for _, val := range x {
			stripSubdocumentIDs(val)
		}
	}
}

// dropEmptyObjects removes, at every depth, object-valued keys whose object is
// empty (after the same removal inside it), as Mongoose's minimize does when it
// stores a board: "definition": {} reads back as no definition at all. An empty
// object that is an array element is kept.
func dropEmptyObjects(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			dropEmptyObjects(val)
			if m, ok := val.(map[string]any); ok && len(m) == 0 {
				delete(x, k)
			}
		}
	case []any:
		for _, val := range x {
			dropEmptyObjects(val)
		}
	}
}

// dropImportedEmptyArrays removes empty arrays from a board read on import.
// The server stores every array path as [] by default (spaceIds,
// templateVariables, and a widget's requests, widgets, legendColumns, ...), so
// an imported document would carry keys a hand-written one never has and plan
// an update right after import. The board's own "widgets" and "tabs" stay:
// an empty board and an untabbed board mean something different from a
// missing key. Ordinary reads do not need this; they keep only the keys the
// prior document had (pruneToPrior).
func dropImportedEmptyArrays(board map[string]any) {
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				walk(val)
				if a, ok := val.([]any); ok && len(a) == 0 {
					delete(x, k)
				}
			}
		case []any:
			for _, val := range x {
				walk(val)
			}
		}
	}
	for k, val := range board {
		walk(val)
		if a, ok := val.([]any); ok && len(a) == 0 && k != "widgets" && k != "tabs" {
			delete(board, k)
		}
	}
}

// normalizeNumber rewrites a JSON number as the IEEE double the server (Node's
// JSON.parse) stores, so numbers compare by value the way they round-trip:
// 4.0, 4 and 4e0 are all "4", 0.50 is "0.5", 1e3 is "1000", and
// 9007199254740993 is 9007199254740992. A number too large for a double
// becomes null, as JSON.stringify writes Infinity. Anything it cannot parse is
// kept as written.
func normalizeNumber(n json.Number) any {
	f, err := strconv.ParseFloat(string(n), 64)
	if math.IsInf(f, 0) {
		return nil
	}
	if err != nil {
		return n
	}
	if f == 0 {
		return json.Number("0") // -0 too: JSON.stringify(-0) is "0"
	}
	if abs := math.Abs(f); abs >= 1e-6 && abs < 1e21 {
		return json.Number(strconv.FormatFloat(f, 'f', -1, 64))
	}
	return json.Number(strconv.FormatFloat(f, 'e', -1, 64))
}

// normalizeNumbers applies normalizeNumber to every number in v, in place.
func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = normalizeNumbers(val)
		}
	case []any:
		for i := range x {
			x[i] = normalizeNumbers(x[i])
		}
	case json.Number:
		return normalizeNumber(x)
	}
	return v
}

// canonicalDashboardJSON is the comparison form of a dashboard document:
// server-managed keys removed (group children's too), nested subdocument _ids
// removed, empty objects removed, numbers compared by value, keys sorted, no
// insignificant whitespace.
func canonicalDashboardJSON(s string) (string, error) {
	doc, err := decodeJSONObject(s)
	if err != nil {
		return "", err
	}
	stripServerKeys(doc)
	stripGroupChildServerKeys(doc)
	dropPowerpackOwnedContent(doc)
	stripSubdocumentIDs(doc)
	dropEmptyObjects(doc)
	normalizeNumbers(doc)
	return marshalCanonical(doc)
}

// pruneToPrior drops keys the server added that the prior document never had,
// so server defaults do not show as drift. Objects recurse key by key; arrays
// recurse element-wise only when both sides have the same length (otherwise
// the server array is kept as it is, so an added or removed element shows).
func pruneToPrior(server, prior any) any {
	switch s := server.(type) {
	case map[string]any:
		p, ok := prior.(map[string]any)
		if !ok {
			return s
		}
		out := make(map[string]any, len(s))
		for k, v := range s {
			if pv, has := p[k]; has {
				out[k] = pruneToPrior(v, pv)
			}
		}
		return out
	case []any:
		p, ok := prior.([]any)
		if !ok || len(p) != len(s) {
			return s
		}
		out := make([]any, len(s))
		for i := range s {
			out[i] = pruneToPrior(s[i], p[i])
		}
		return out
	default:
		return s
	}
}

// canonicalizeServerDashboard turns the API's full dashboard object into the
// canonical exported document, aligned with prior (the document in state; nil
// on import, when every server key is kept):
//
//  1. server-managed keys are stripped;
//  2. keys the server added that prior never had are pruned (prior != nil);
//  3. widget ids: the server re-mints widget _ids on every import. When the
//     widget count is unchanged, the n-th server widget takes the n-th prior
//     widget's "id" and tabs[].widgetIds are rewritten through the same map.
//     On import, or when widgets were added or removed (so positions no
//     longer line up), a tabbed board's widgets get "id" = server _id, as
//     React's export does, so the tabs still resolve;
//  4. tab ids: the server mints a new id for every tab on each import, so when
//     the tab count is unchanged the n-th tab takes the n-th prior tab's "id".
func canonicalizeServerDashboard(server, prior map[string]any) map[string]any {
	widgets, _ := server["widgets"].([]any)
	serverIDs := make([]string, len(widgets))
	for i, w := range widgets {
		if wm, ok := w.(map[string]any); ok {
			serverIDs[i], _ = wm["_id"].(string)
		}
	}

	stripServerKeys(server)
	doc := server
	if prior != nil {
		doc, _ = pruneToPrior(server, prior).(map[string]any)
	} else {
		dropImportedEmptyArrays(doc)
	}

	var priorWidgets, priorTabs []any
	if prior != nil {
		priorWidgets, _ = prior["widgets"].([]any)
		priorTabs, _ = prior["tabs"].([]any)
	}
	tabs, _ := doc["tabs"].([]any)
	hasTabs := len(tabs) > 0

	// server _id → the prior widget's own id value (a string or a number).
	idMap := map[string]any{}
	widgets, _ = doc["widgets"].([]any)
	alignedWidgets := prior != nil && len(priorWidgets) == len(widgets)
	for i, w := range widgets {
		wm, ok := w.(map[string]any)
		if !ok || i >= len(serverIDs) {
			continue
		}
		serverID := serverIDs[i]
		if alignedWidgets {
			pw, _ := priorWidgets[i].(map[string]any)
			if priorID, has := pw["id"]; has && priorID != nil {
				wm["id"] = priorID
				if serverID != "" {
					idMap[serverID] = priorID
				}
			}
			continue
		}
		if hasTabs && serverID != "" {
			wm["id"] = serverID
		}
	}

	if len(priorTabs) == len(tabs) {
		for i, t := range tabs {
			tm, ok := t.(map[string]any)
			pt, pok := priorTabs[i].(map[string]any)
			if !ok || !pok {
				continue
			}
			if pid, has := pt["id"]; has {
				if _, serverHas := tm["id"]; serverHas {
					tm["id"] = pid
				}
			}
		}
	}

	if len(idMap) > 0 {
		for _, t := range tabs {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			ids, _ := tm["widgetIds"].([]any)
			for j, id := range ids {
				if sid, ok := id.(string); ok {
					if mapped, found := idMap[sid]; found {
						ids[j] = mapped
					}
				}
			}
		}
	}
	return doc
}

// ---------------------------------------------------------------------------
// dashboard_json attribute type: a string whose semantic equality is canonical
// JSON equality, so key order, whitespace and server-managed keys never show
// as a diff. (Hand-written instead of adding terraform-plugin-framework-jsontypes:
// it also has to ignore the server-managed keys.)
// ---------------------------------------------------------------------------

var (
	_ basetypes.StringTypable                    = dashboardJSONType{}
	_ basetypes.StringValuableWithSemanticEquals = dashboardJSONValue{}
)

type dashboardJSONType struct {
	basetypes.StringType
}

func (t dashboardJSONType) Equal(o attr.Type) bool {
	_, ok := o.(dashboardJSONType)
	return ok
}

func (t dashboardJSONType) String() string { return "provider.dashboardJSONType" }

func (t dashboardJSONType) ValueFromString(ctx context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return dashboardJSONValue{StringValue: in}, nil
}

func (t dashboardJSONType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	v, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	sv, ok := v.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", v)
	}
	return dashboardJSONValue{StringValue: sv}, nil
}

func (t dashboardJSONType) ValueType(ctx context.Context) attr.Value { return dashboardJSONValue{} }

type dashboardJSONValue struct {
	basetypes.StringValue
}

func newDashboardJSONValue(s string) dashboardJSONValue {
	return dashboardJSONValue{StringValue: basetypes.NewStringValue(s)}
}

func (v dashboardJSONValue) Equal(o attr.Value) bool {
	other, ok := o.(dashboardJSONValue)
	return ok && v.StringValue.Equal(other.StringValue)
}

func (v dashboardJSONValue) Type(ctx context.Context) attr.Type { return dashboardJSONType{} }

// StringSemanticEquals: equal when both documents have the same canonical form.
func (v dashboardJSONValue) StringSemanticEquals(ctx context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	newValue, ok := newValuable.(dashboardJSONValue)
	if !ok {
		diags.AddError("Semantic Equality Check Error", fmt.Sprintf("expected dashboardJSONValue, got %T", newValuable))
		return false, diags
	}
	if v.IsNull() || v.IsUnknown() || newValue.IsNull() || newValue.IsUnknown() {
		return false, diags
	}
	return dashboardJSONSemanticallyEqual(v.ValueString(), newValue.ValueString()), diags
}

func dashboardJSONSemanticallyEqual(a, b string) bool {
	if a == b {
		return true
	}
	ca, err := canonicalDashboardJSON(a)
	if err != nil {
		return false
	}
	cb, err := canonicalDashboardJSON(b)
	if err != nil {
		return false
	}
	return ca == cb
}

// dashboardJSONObjectValidator rejects a dashboard_json that is not a JSON object.
type dashboardJSONObjectValidator struct{}

func (dashboardJSONObjectValidator) Description(ctx context.Context) string {
	return "must be a JSON object (the document Export → JSON writes)"
}

func (v dashboardJSONObjectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (dashboardJSONObjectValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	doc, err := decodeJSONObject(req.ConfigValue.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid dashboard_json", "dashboard_json "+err.Error())
		return
	}
	for _, problem := range dashboardDocumentProblems(doc) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid dashboard_json", problem)
	}
}

// Server limits on tabs (helper/dashboard-tabs.js MAX_TABS / MAX_TAB_NAME).
const (
	maxDashboardTabs   = 30
	maxDashboardTabLen = 100
)

var (
	// helper/index.js snakecaseToCamelcase, which create applies to every key
	// outside a widget's "definition".
	snakeKeyPart = regexp.MustCompile(`_\w`)
	// normalizeMarker's severities and line styles, read out of display_type.
	markerDisplayWords = map[string]bool{
		"error": true, "warning": true, "ok": true, "info": true,
		"solid": true, "dashed": true, "dotted": true, "bold": true,
	}
	// retired-sources.js: queries on these are rewritten to rum_v2.
	retiredDataSources = map[string]string{"mobile_rum": "rum_v2", "mobile_rum_stream": "rum_v2_stream"}
)

func serverCamelCase(key string) string {
	return snakeKeyPart.ReplaceAllStringFunc(key, func(m string) string { return strings.ToUpper(m[1:]) })
}

func keySet(lists ...[]string) map[string]bool {
	out := map[string]bool{}
	for _, l := range lists {
		for _, k := range l {
			out[k] = true
		}
	}
	return out
}

var (
	boardDocumentKeys  = keySet(dashboardWritableKeys, dashboardServerKeys, []string{"widgets", "tabs"})
	widgetDocumentKeys = keySet(widgetWritableKeys, widgetServerKeys, []string{"id"})
)

// unknownKeyProblem explains why a key outside allowed would not be stored as
// written, or returns "" when the key is allowed.
func unknownKeyProblem(at, key string, allowed map[string]bool) string {
	if allowed[key] {
		return ""
	}
	if camel := serverCamelCase(key); camel != key && allowed[camel] {
		return fmt.Sprintf("%s%s: write it as %q (camelCase, as Export → JSON does); Atatus renames it on create and ignores it on update", at, key, camel)
	}
	return fmt.Sprintf("%s%s: Atatus does not store this key, so the board would never match the document; remove it", at, key)
}

// snakeKeyProblems reports keys under v that create would camel-case.
func snakeKeyProblems(at string, v any) []string {
	var problems []string
	switch x := v.(type) {
	case map[string]any:
		for _, k := range sortedKeys(x) {
			if k != "_id" && k != "__v" {
				if camel := serverCamelCase(k); camel != k {
					problems = append(problems, fmt.Sprintf("%s.%s: write it as %q (camelCase); Atatus renames it on create", at, k, camel))
					continue
				}
			}
			problems = append(problems, snakeKeyProblems(at+"."+k, x[k])...)
		}
	case []any:
		for i, e := range x {
			problems = append(problems, snakeKeyProblems(fmt.Sprintf("%s[%d]", at, i), e)...)
		}
	}
	return problems
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func retiredSourceProblems(at string, q map[string]any) []string {
	var problems []string
	if ds, _ := q["dataSource"].(string); retiredDataSources[ds] != "" {
		problems = append(problems, fmt.Sprintf("%s.dataSource: %q is retired and Atatus stores it as %q; write %q", at, ds, retiredDataSources[ds], retiredDataSources[ds]))
	}
	compute, _ := q["compute"].(map[string]any)
	for _, m := range []struct {
		path string
		v    any
	}{{".metricName", q["metricName"]}, {".compute.metricName", compute["metricName"]}} {
		if name, _ := m.v.(string); strings.HasPrefix(name, "mobile_rum.") {
			problems = append(problems, fmt.Sprintf("%s%s: %q is retired and Atatus stores it under rum_v2; write the rum_v2 metric", at, m.path, name))
		}
	}
	return problems
}

// widgetProblems checks one widget (top-level, or a group child) at path at.
func widgetProblems(at string, wm map[string]any) []string {
	var problems []string
	for _, k := range sortedKeys(wm) {
		if p := unknownKeyProblem(at+".", k, widgetDocumentKeys); p != "" {
			problems = append(problems, p)
		}
	}

	def, _ := wm["definition"].(map[string]any)
	typ, _ := wm["type"].(string)
	if typ == "" {
		typ, _ = def["type"].(string)
	}
	for _, key := range []string{"type", "title", "requests", "description"} {
		if _, inDef := def[key]; !inDef {
			continue
		}
		_, own := wm[key]
		if (key == "requests" || key == "description") && (own || typ == "hostmap") {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s.definition.%s: put %q on the widget itself, not inside \"definition\" (as Export → JSON writes it)", at, key, key))
	}
	for _, pair := range [][2]string{{"showLegend", "show_legend"}, {"legendLayout", "legend_layout"}, {"legendColumns", "legend_columns"}} {
		camel, snake := pair[0], pair[1]
		_, hasCamel := def[camel]
		_, hasSnake := def[snake]
		if hasCamel && !hasSnake {
			problems = append(problems, fmt.Sprintf("%s.definition.%s: write it as %q; Atatus renames it inside \"definition\"", at, camel, snake))
		}
	}
	if markers, _ := def["markers"].([]any); typ != "distribution" {
		for i, mk := range markers {
			mm, _ := mk.(map[string]any)
			for _, k := range []string{"value", "to"} {
				if _, isString := mm[k].(string); isString {
					problems = append(problems, fmt.Sprintf("%s.definition.markers[%d].%s: must be a number (use \"type\": \"line\" or \"range\" with \"value\" / \"to\"); Atatus rewrites expression strings", at, i, k))
				}
			}
			for _, k := range []string{"display_type", "displayType"} {
				dt, _ := mm[k].(string)
				for _, word := range strings.Fields(strings.ToLower(dt)) {
					if markerDisplayWords[word] {
						problems = append(problems, fmt.Sprintf("%s.definition.markers[%d].%s: use \"severity\" and \"lineStyle\" instead; Atatus rewrites it", at, i, k))
						break
					}
				}
			}
		}
	}

	requests, _ := wm["requests"].([]any)
	problems = append(problems, snakeKeyProblems(at+".requests", requests)...)
	for r, req := range requests {
		rm, _ := req.(map[string]any)
		rat := fmt.Sprintf("%s.requests[%d]", at, r)
		if rm["displayType"] == "bars" {
			problems = append(problems, rat+`.displayType: write "bar"; Atatus rewrites "bars"`)
		}
		queries, _ := rm["queries"].([]any)
		for q, query := range queries {
			qm, _ := query.(map[string]any)
			qat := fmt.Sprintf("%s.queries[%d]", rat, q)
			if _, isArray := qm["filters"].([]any); isArray {
				problems = append(problems, qat+`.filters: write one string (e.g. "env:prod AND status:ok"); Atatus joins a list into a string`)
			}
			problems = append(problems, retiredSourceProblems(qat, qm)...)
		}
		if lq, ok := rm["query"].(map[string]any); ok {
			problems = append(problems, retiredSourceProblems(rat+".query", lq)...)
		}
		sortSpec, _ := rm["sort"].(map[string]any)
		orderBy, _ := sortSpec["orderBy"].([]any)
		for o, entry := range orderBy {
			em, _ := entry.(map[string]any)
			if order, ok := em["order"].(string); ok && order != strings.ToLower(order) {
				problems = append(problems, fmt.Sprintf("%s.sort.orderBy[%d].order: write %q; Atatus lower-cases it", rat, o, strings.ToLower(order)))
			}
		}
	}
	return problems
}

// dashboardDocumentProblems finds documents the server would store differently
// from how they are written. Terraform would report such a write as an
// inconsistent result after apply, or as a diff that never settles, so they are
// refused at validate time instead:
//   - board and widget keys the server does not store (unknown, or snake_case
//     forms that create camel-cases and update drops), and snake_case keys
//     inside requests[], templateVariables[] and tabs[];
//   - a widget setting kept inside "definition" that the server's create moves
//     onto the widget (type and title; requests and description unless the
//     widget has its own or is a hostmap), on top-level widgets and group
//     children alike. Update stores them where they are and refuses a widget
//     with no top-level "type". Export → JSON writes them on the widget;
//   - forms the server rewrites: definition.showLegend / legendLayout /
//     legendColumns, marker expression strings and display_type, list-valued
//     query filters, upper-case sort orders, displayType "bars", retired
//     mobile_rum sources;
//   - tabs: more than the server keeps, or a name it trims, shortens or
//     replaces; on a tabbed board, a widget with no "id", or one that is not in
//     exactly one tab, or a tab entry that names no widget: the server drops
//     unknown entries and moves an unplaced widget into the first tab.
func dashboardDocumentProblems(doc map[string]any) []string {
	var problems []string
	for _, k := range sortedKeys(doc) {
		if p := unknownKeyProblem("", k, boardDocumentKeys); p != "" {
			problems = append(problems, p)
		}
	}
	variables, _ := doc["templateVariables"].([]any)
	problems = append(problems, snakeKeyProblems("templateVariables", variables)...)
	for i, v := range variables {
		vm, _ := v.(map[string]any)
		if vq, ok := vm["valuesQuery"].(map[string]any); ok {
			problems = append(problems, retiredSourceProblems(fmt.Sprintf("templateVariables[%d].valuesQuery", i), vq)...)
		}
	}

	widgets, _ := doc["widgets"].([]any)
	for i, w := range widgets {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		at := fmt.Sprintf("widgets[%d]", i)
		problems = append(problems, widgetProblems(at, wm)...)
		children, _ := wm["widgets"].([]any)
		for j, c := range children {
			if cm, ok := c.(map[string]any); ok {
				problems = append(problems, widgetProblems(fmt.Sprintf("%s.widgets[%d]", at, j), cm)...)
			}
		}
	}

	tabs, _ := doc["tabs"].([]any)
	if len(tabs) == 0 {
		return problems
	}
	problems = append(problems, snakeKeyProblems("tabs", tabs)...)
	if len(tabs) > maxDashboardTabs {
		problems = append(problems, fmt.Sprintf("tabs: a dashboard keeps at most %d tabs, this one has %d", maxDashboardTabs, len(tabs)))
	}
	for t, tab := range tabs {
		tm, _ := tab.(map[string]any)
		name, _ := tm["name"].(string)
		switch {
		case strings.TrimSpace(name) == "":
			problems = append(problems, fmt.Sprintf("tabs[%d].name: give the tab a non-empty name; Atatus names an unnamed tab itself", t))
		case strings.TrimSpace(name) != name:
			problems = append(problems, fmt.Sprintf("tabs[%d].name: remove the leading or trailing spaces; Atatus trims them", t))
		case utf8.RuneCountInString(name) > maxDashboardTabLen:
			problems = append(problems, fmt.Sprintf("tabs[%d].name: at most %d characters; Atatus shortens longer names", t, maxDashboardTabLen))
		}
	}
	ids := map[string]bool{}
	for i, w := range widgets {
		wm, _ := w.(map[string]any)
		id, ok := wm["id"]
		key := fmt.Sprint(id)
		switch {
		case !ok || id == nil || key == "":
			problems = append(problems, fmt.Sprintf("widgets[%d] needs an \"id\": the board has tabs, and tabs[].widgetIds refer to widgets by id", i))
		case ids[key]:
			problems = append(problems, fmt.Sprintf("widgets[%d]: id %q is used by more than one widget", i, key))
		default:
			ids[key] = true
		}
	}
	placed := map[string]bool{}
	for t, tab := range tabs {
		tm, _ := tab.(map[string]any)
		entries, _ := tm["widgetIds"].([]any)
		for _, e := range entries {
			key := fmt.Sprint(e)
			switch {
			case !ids[key]:
				problems = append(problems, fmt.Sprintf("tabs[%d].widgetIds: %q is not the id of any widget", t, key))
			case placed[key]:
				problems = append(problems, fmt.Sprintf("tabs[%d].widgetIds: widget %q is already in a tab; each widget belongs to exactly one tab", t, key))
			default:
				placed[key] = true
			}
		}
	}
	for i, w := range widgets {
		wm, _ := w.(map[string]any)
		if id, ok := wm["id"]; ok && id != nil && ids[fmt.Sprint(id)] && !placed[fmt.Sprint(id)] {
			placed[fmt.Sprint(id)] = true // report a duplicated id once
			problems = append(problems, fmt.Sprintf("widgets[%d] (id %q) is in no tab; list it in one tabs[].widgetIds", i, fmt.Sprint(id)))
		}
	}
	return problems
}
