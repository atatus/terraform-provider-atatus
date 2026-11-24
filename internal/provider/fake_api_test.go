package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeAPIKey is an obvious placeholder; the fake API rejects anything else.
const fakeAPIKey = "test-fake-api-key"

// fakeDashboardAPI is an in-memory stand-in for the public /v2/dashboards API.
// It follows the real server (atatus-server api/lib/controllers/public-api/index.js
// → dashboard-v2/dashboards.js createSharedDashboard / update, helper/index.js,
// helper/dashboard-tabs.js), including the behaviours that make diffs unstable:
//   - create converts the body like convertKeysToCamelCase: every nested "id" /
//     "_id" is dropped and a widget's definition.type / title (and, for a
//     non-hostmap widget, definition.requests / description) move to the widget;
//     PUT does no such conversion and refuses a widget with no top-level type;
//   - widgets are validated like validateWidgetData: a chart type needs
//     requests[].queries[] with a data source (and a query for metrics), and
//     only a group holds children;
//   - only the writable board / widget fields are stored (unknown keys
//     dropped), requests[] through the WidgetV2 subschema's keys with its
//     defaults (responseFormat, onRightYaxis, formulas, groupBy,
//     compute.bucket), list-valued filters joined into a string, and empty
//     objects removed (Mongoose minimize);
//   - defaults the user never wrote ("layoutType", template variable lists, the
//     board's templateVariablePresets [] and a preset's or entry's lists),
//     audit fields, a "url";
//   - presets are validated like validateTemplateVariablePresets: unique
//     trimmed names, every entry naming one of the document's variables;
//   - every widget gets a new _id on create and PUT; group children and
//     templateVariables[] entries get new _ids on create (every "_id" in the
//     body is dropped), PUT keeps the ones it is given;
//   - tabs are rebuilt with NEW tab ids and reconciled (a widget in no tab joins
//     the first one; unknown and duplicate ids are dropped);
//   - PUT replaces the board: the optional board fields the body leaves out
//     (description, reflowType, isGlobalTimeSelectable, timeDur,
//     templateVariables, templateVariablePresets, isReadOnly) are cleared — spaceIds is kept — as the public
//     API's whole-document PUT does (dashboard-v2/dashboards.js
//     replaceDashboardDocument). legacySetPut models older servers, whose PUT
//     was a $set that kept them;
//   - numbers are re-encoded the way Node does (4.0 → 4), __v is bumped, a stale
//     expectedVersion answers 409 and a deleted board 404.
type fakeDashboardAPI struct {
	srv *httptest.Server

	mu                sync.Mutex
	boards            map[string]map[string]any
	seq               int
	requests          []string
	putsWithVersion   int
	putsNoVersion     int
	concurrentEditPut bool // next PUT sees an edit made "in the UI" just before it
	legacySetPut      bool // PUT keeps board fields the body leaves out (servers before field clearing)
	// reorderOnGet lists a board's widgets in reverse on GET: the real server may list
	// them in another order than they were created in.
	reorderOnGet bool

	// checks serves /v2/channels and /v2/synthetic/checks on the same server
	// (fake_synthetic_api_test.go), so one fake answers the whole provider.
	checks *fakeChecks
	// groups serves /v2/dashboard-groups (fake_groups_api_test.go).
	groups *fakeGroups
	// packs serves /v2/powerpacks (fake_powerpacks_api_test.go).
	packs *fakePowerpacks
}

// Read-only fields the real server ignores on POST/PUT (the contract's list).
var fakeServerIgnoredKeys = []string{
	"_id", "__v", "accountId", "authorId", "authorName", "authorEmail", "createdAt", "updatedAt",
	"isFavorited", "userCanEdit", "userIsBoardAdmin", "powerpackStatus", "isTemplate", "url",
	"restoredFromVersionId", "importDashboard", "restrictedTo",
}

// helper/index.js WIDGET_WRITABLE_FIELDS / DASHBOARD_WRITABLE_FIELDS.
var (
	fakeWidgetWritable = []string{
		"type", "title", "titleSize", "titleAlign", "description", "definition", "requests", "time", "layout",
		"widgets", "timeseriesBackground", "showLegend", "legendLayout", "legendColumns", "color", "fontSize",
		"text", "textAlign",
	}
	fakeBoardWritable = []string{
		"title", "description", "dashboardType", "layoutType", "reflowType", "isGlobalTimeSelectable", "timeDur",
		"isReadOnly", "isFavorited", "spaceIds", "templateVariables", "templateVariablePresets",
	}
)

// Optional board fields a whole-document PUT clears when the body leaves them out.
var fakeClearedOnPut = []string{"description", "reflowType", "isGlobalTimeSelectable", "timeDur", "templateVariables", "templateVariablePresets", "isReadOnly"}

var fakeWidgetTypes = map[string]bool{"timeseries": true, "query_value": true, "toplist": true, "note": true, "group": true, "hostmap": true, "topology_map": true}

// Types validateWidgetData sends through validateRequests.
var fakeChartTypes = map[string]bool{"timeseries": true, "query_value": true, "toplist": true, "hostmap": true}

// WidgetV2 requests[] / queries[] subschema keys (strict: anything else is dropped).
var (
	fakeRequestKeys = []string{"queries", "query", "formulas", "responseFormat", "sort", "style", "displayType", "onRightYaxis"}
	fakeQueryKeys   = []string{"name", "dataSource", "query", "aggregator", "limit", "metricName", "service", "resourceHash", "filters", "groupBy", "compute"}
)

func newFakeDashboardAPI(t *testing.T) *fakeDashboardAPI {
	t.Helper()
	f := &fakeDashboardAPI{boards: map[string]map[string]any{}, checks: newFakeChecks(), groups: newFakeGroups(), packs: newFakePowerpacks()}
	mux := http.NewServeMux()
	f.checks.register(mux)
	f.registerLookups(mux)
	mux.HandleFunc("POST /v2/dashboards", f.create)
	mux.HandleFunc("GET /v2/dashboards/{id}", f.get)
	mux.HandleFunc("PUT /v2/dashboards/{id}", f.put)
	mux.HandleFunc("DELETE /v2/dashboards/{id}", f.delete)
	f.registerRestrictions(mux)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
		f.mu.Unlock()
		if r.Header.Get("X-API-KEY") != fakeAPIKey {
			writeFakeJSON(w, http.StatusForbidden, map[string]any{"message": "Invalid API key"})
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDashboardAPI) URL() string { return f.srv.URL }

func writeFakeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fakeDecodeBody decodes numbers as float64, as Node does, so 4.0 comes back as 4.
func fakeDecodeBody(r *http.Request) (map[string]any, error) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("body must be a JSON object: %v", err)
	}
	return body, nil
}

func fakePick(m map[string]any, keys []string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

// fakeCamelConvert mirrors what convertKeysToCamelCase does to an exported
// (already camelCase) document on create: drop every "id" / "_id" and lift a
// definition's type / title (and requests / description) onto its widget.
func fakeCamelConvert(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = fakeCamelConvert(x[i])
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			if k == "id" || k == "_id" {
				continue
			}
			def, isDef := val.(map[string]any)
			if k != "definition" || !isDef {
				out[k] = fakeCamelConvert(val)
				continue
			}
			rest := map[string]any{}
			for dk, dv := range def {
				rest[dk] = dv
			}
			typ, _ := rest["type"].(string)
			if typ == "" {
				typ, _ = x["type"].(string)
			}
			for _, lift := range []string{"title", "type"} {
				if lv, ok := rest[lift]; ok {
					out[lift] = lv
					delete(rest, lift)
				}
			}
			if rq, ok := rest["requests"]; ok && typ != "hostmap" {
				if _, own := x["requests"]; !own {
					out["requests"] = fakeCamelConvert(rq)
				}
				delete(rest, "requests")
			}
			if d, ok := rest["description"].(string); ok && typ != "hostmap" && x["description"] == nil {
				out["description"] = d
				delete(rest, "description")
			}
			if len(rest) > 0 {
				out["definition"] = rest
			}
		}
		return out
	}
	return v
}

func fakeValidate(body map[string]any) error {
	if title, _ := body["title"].(string); strings.TrimSpace(title) == "" {
		return fmt.Errorf("title is required")
	}
	if err := fakeValidatePresets(body); err != nil {
		return err
	}
	raw, present := body["widgets"]
	if !present {
		return nil
	}
	widgets, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("widgets must be an array")
	}
	if len(widgets) > 500 {
		return fmt.Errorf("a dashboard can have at most 500 widgets")
	}
	for i, w := range widgets {
		if err := fakeValidateWidget(w, true); err != nil {
			return fmt.Errorf("widgets[%d]: %v", i, err)
		}
	}
	return nil
}

// fakeValidatePresets is validateTemplateVariablePresets for a whole document:
// unique trimmed names, and every entry names one of the document's variables.
func fakeValidatePresets(body map[string]any) error {
	presets, ok := body["templateVariablePresets"].([]any)
	if !ok {
		return nil
	}
	known := map[string]bool{}
	vars, _ := body["templateVariables"].([]any)
	for _, v := range vars {
		if vm, ok := v.(map[string]any); ok {
			if n, ok := vm["name"].(string); ok {
				known[n] = true
			}
		}
	}
	seen := map[string]bool{}
	for i, p := range presets {
		pm, _ := p.(map[string]any)
		name, _ := pm["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("Template variable preset at index %d needs a name!", i)
		}
		if seen[name] {
			return fmt.Errorf("Template variable preset '%s' appears more than once!", name)
		}
		seen[name] = true
		entries, _ := pm["templateVariables"].([]any)
		for _, e := range entries {
			em, _ := e.(map[string]any)
			if n, _ := em["name"].(string); !known[n] {
				return fmt.Errorf("Template variable preset '%s' uses '%s', which is not a variable of this dashboard!", name, n)
			}
		}
	}
	return nil
}

// fakeStorePresets adds what Mongoose stores for presets: the board's list
// always ([] when the body has none), and [] for a preset's or an entry's
// missing list.
func fakeStorePresets(board map[string]any) {
	presets, ok := board["templateVariablePresets"].([]any)
	if !ok {
		board["templateVariablePresets"] = []any{}
		return
	}
	for _, p := range presets {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		entries, ok := pm["templateVariables"].([]any)
		if !ok {
			pm["templateVariables"] = []any{}
			continue
		}
		for _, e := range entries {
			if em, ok := e.(map[string]any); ok {
				if _, has := em["values"]; !has {
					em["values"] = []any{}
				}
			}
		}
	}
}

// fakeValidateWidget is validateWidgetData for the fake's widget types.
func fakeValidateWidget(w any, topLevel bool) error {
	wm, _ := w.(map[string]any)
	typ, _ := wm["type"].(string)
	if !fakeWidgetTypes[typ] {
		return fmt.Errorf("unsupported widget type %q", typ)
	}
	children, _ := wm["widgets"].([]any)
	if typ != "group" && len(children) > 0 {
		return fmt.Errorf("Only a group can contain widgets!")
	}
	for _, c := range children {
		if cm, _ := c.(map[string]any); cm["type"] == "group" || !topLevel {
			return fmt.Errorf("A group cannot contain another group!")
		}
		if err := fakeValidateWidget(c, false); err != nil {
			return err
		}
	}
	if !fakeChartTypes[typ] {
		return nil
	}
	requests, ok := wm["requests"].([]any)
	if !ok {
		return fmt.Errorf("Invalid request format: requests must be a list of queries!")
	}
	for _, r := range requests {
		rm, _ := r.(map[string]any)
		queries, _ := rm["queries"].([]any)
		if len(queries) == 0 {
			return fmt.Errorf("Invalid queries format!")
		}
		for _, q := range queries {
			qm, _ := q.(map[string]any)
			ds, _ := qm["dataSource"].(string)
			if ds == "" {
				return fmt.Errorf("Invalid data source: %v!", qm["dataSource"])
			}
			if query, _ := qm["query"].(string); ds == "metrics" && query == "" {
				return fmt.Errorf("Query is required for metrics data source.")
			}
		}
	}
	return nil
}

// fakeMinimize drops empty objects at every depth, as Mongoose minimize does.
func fakeMinimize(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			fakeMinimize(val)
			if m, ok := val.(map[string]any); ok && len(m) == 0 {
				delete(x, k)
			}
		}
	case []any:
		for _, val := range x {
			fakeMinimize(val)
		}
	}
}

// fakeStoreRequests casts requests[] through the WidgetV2 subschema: unknown
// keys dropped, defaults added, list filters joined (normalizeWidgetFilters).
func fakeStoreRequests(raw any) any {
	requests, ok := raw.([]any)
	if !ok {
		return raw
	}
	out := make([]any, 0, len(requests))
	for _, r := range requests {
		rm, _ := r.(map[string]any)
		stored := fakePick(rm, fakeRequestKeys)
		if _, ok := stored["responseFormat"]; !ok {
			stored["responseFormat"] = "scalar"
		}
		if _, ok := stored["onRightYaxis"]; !ok {
			stored["onRightYaxis"] = false
		}
		if _, ok := stored["formulas"]; !ok {
			stored["formulas"] = []any{}
		}
		queries, _ := stored["queries"].([]any)
		storedQueries := make([]any, 0, len(queries))
		for _, q := range queries {
			qm, _ := q.(map[string]any)
			sq := fakePick(qm, fakeQueryKeys)
			if list, ok := sq["filters"].([]any); ok {
				var parts []string
				for _, f := range list {
					if fs, ok := f.(string); ok && strings.TrimSpace(fs) != "" {
						parts = append(parts, fs)
					}
				}
				sq["filters"] = strings.Join(parts, " AND ")
			}
			if _, ok := sq["groupBy"]; !ok {
				sq["groupBy"] = []any{}
			}
			compute, _ := sq["compute"].(map[string]any)
			if compute == nil {
				compute = map[string]any{}
				sq["compute"] = compute
			}
			if _, ok := compute["bucket"]; !ok {
				compute["bucket"] = float64(0)
			}
			storedQueries = append(storedQueries, sq)
		}
		if _, has := stored["queries"]; has {
			stored["queries"] = storedQueries
		}
		out = append(out, stored)
	}
	return out
}

// fakeStoreTemplateVariables keeps each entry's _id when it has one (PUT) and
// mints one otherwise (create, whose body lost every _id), and adds the
// schema's list defaults.
func (f *fakeDashboardAPI) fakeStoreTemplateVariables(board map[string]any) {
	vars, ok := board["templateVariables"].([]any)
	if !ok {
		return
	}
	for _, v := range vars {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if _, has := vm["_id"]; !has {
			f.seq++
			vm["_id"] = fmt.Sprintf("tv%06d", f.seq)
		}
		for _, k := range []string{"availableValues", "default"} {
			if _, has := vm[k]; !has {
				vm[k] = []any{}
			}
		}
	}
}

// fakeRebuildTabs is dashboard-tabs.js rebuildTabs + reconcile: new tab ids,
// widgetIds re-linked through idMap, unknown / duplicate ids dropped, a widget
// in no tab appended to the first tab. nil = untabbed.
func (f *fakeDashboardAPI) fakeRebuildTabs(rawTabs any, idMap map[string]string, widgetIDs []string) []any {
	tabs, _ := rawTabs.([]any)
	if len(tabs) == 0 {
		return nil
	}
	placed := map[string]bool{}
	var out []any
	resolved := 0
	for i, t := range tabs {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		name, _ := tm["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("Tab %d", i+1)
		}
		ids := []any{}
		src, _ := tm["widgetIds"].([]any)
		for _, k := range src {
			nid := idMap[fmt.Sprint(k)]
			if nid == "" || placed[nid] {
				continue
			}
			placed[nid] = true
			resolved++
			ids = append(ids, nid)
		}
		f.seq++
		out = append(out, map[string]any{"id": fmt.Sprintf("tab-%06d", f.seq), "name": name, "widgetIds": ids})
	}
	if len(out) == 0 || (len(widgetIDs) > 0 && resolved == 0) {
		return nil
	}
	first := out[0].(map[string]any)
	for _, id := range widgetIDs {
		if !placed[id] {
			first["widgetIds"] = append(first["widgetIds"].([]any), id)
		}
	}
	return out
}

// storeWidgets gives every top-level widget a new _id (keyed by its "id", else
// "_id", in raw) and returns the stored widgets, the id map and the new ids.
func (f *fakeDashboardAPI) storeWidgets(widgets, raw []any, boardID string, mintChildren bool) ([]any, map[string]string, []string) {
	idMap := map[string]string{}
	var ids []string
	out := make([]any, 0, len(widgets))
	for i, w := range widgets {
		wm, _ := w.(map[string]any)
		stored := fakePick(wm, fakeWidgetWritable)
		f.seq++
		newID := fmt.Sprintf("w%06d", f.seq)
		ids = append(ids, newID)
		if i < len(raw) {
			if src, ok := raw[i].(map[string]any); ok {
				key := src["id"]
				if key == nil {
					key = src["_id"]
				}
				if key != nil {
					idMap[fmt.Sprint(key)] = newID
				}
			}
		}
		if _, has := stored["requests"]; has {
			stored["requests"] = fakeStoreRequests(stored["requests"])
		}
		if children, ok := stored["widgets"].([]any); ok {
			for _, c := range children {
				if cm, ok := c.(map[string]any); ok {
					if _, has := cm["requests"]; has {
						cm["requests"] = fakeStoreRequests(cm["requests"])
					}
					if _, has := cm["_id"]; !has || mintChildren {
						f.seq++
						cm["_id"] = fmt.Sprintf("c%06d", f.seq)
					}
				}
			}
		}
		fakeMinimize(stored)
		stored["_id"] = newID
		stored["dashboardId"] = boardID
		stored["accountId"] = "acct-fake"
		stored["createdAt"] = "2026-01-01T00:00:00.000Z"
		out = append(out, stored)
	}
	return out, idMap, ids
}

func (f *fakeDashboardAPI) stamp(board map[string]any, id string, version float64) {
	if _, ok := board["layoutType"]; !ok {
		board["layoutType"] = "ordered"
	}
	board["_id"] = id
	board["__v"] = version
	board["accountId"] = "acct-fake"
	board["authorName"] = "Fake User"
	board["createdAt"] = "2026-01-01T00:00:00.000Z"
	board["updatedAt"] = fmt.Sprintf("2026-01-01T00:00:%02d.000Z", int(version)%60)
	board["isFavorited"] = false
	board["url"] = f.srv.URL + "/dashboards/" + id
}

func stripFakeReadonly(body map[string]any) {
	for _, k := range fakeServerIgnoredKeys {
		delete(body, k)
	}
	widgets, _ := body["widgets"].([]any)
	for _, w := range widgets {
		if wm, ok := w.(map[string]any); ok {
			for _, k := range []string{"__v", "createdAt", "updatedAt", "accountId", "dashboardId", "data"} {
				delete(wm, k)
			}
		}
	}
}

func (f *fakeDashboardAPI) create(w http.ResponseWriter, r *http.Request) {
	raw, err := fakeDecodeBody(r)
	var body map[string]any
	if err == nil {
		stripFakeReadonly(raw)
		body = fakeCamelConvert(raw).(map[string]any)
		err = fakeValidate(body)
		if err == nil {
			if _, ok := body["widgets"].([]any); !ok {
				err = fmt.Errorf("The file has no widgets list!")
			}
		}
	}
	if err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	if r.URL.Query().Get("dryRun") == "true" {
		writeFakeJSON(w, http.StatusOK, map[string]any{"valid": true})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("%024x", 0xda5b0000+f.seq) // a 24-hex ObjectId, as the server mints
	board := fakePick(body, fakeBoardWritable)
	f.fakeStoreTemplateVariables(board)
	fakeStorePresets(board)
	fakeMinimize(board)
	rawWidgets, _ := raw["widgets"].([]any)
	widgets, idMap, ids := f.storeWidgets(body["widgets"].([]any), rawWidgets, id, true)
	board["widgets"] = widgets
	if tabs := f.fakeRebuildTabs(raw["tabs"], idMap, ids); tabs != nil {
		board["tabs"] = tabs
	}
	f.stamp(board, id, 0)
	f.boards[id] = board
	writeFakeJSON(w, http.StatusOK, board)
}

func (f *fakeDashboardAPI) get(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.boards[r.PathValue("id")]
	if !ok {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Dashboard not found"})
		return
	}
	if ws, _ := b["widgets"].([]any); f.reorderOnGet && len(ws) > 1 {
		out := map[string]any{}
		for k, v := range b {
			out[k] = v
		}
		reversed := make([]any, len(ws))
		for i, wv := range ws {
			reversed[len(ws)-1-i] = wv
		}
		out["widgets"] = reversed
		b = out
	}
	writeFakeJSON(w, http.StatusOK, b)
}

func (f *fakeDashboardAPI) put(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := r.PathValue("id")
	cur, ok := f.boards[id]
	if !ok {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Dashboard not found"})
		return
	}
	body, err := fakeDecodeBody(r)
	if err == nil {
		if _, isArr := body["widgets"].([]any); !isArr {
			err = fmt.Errorf("The dashboard JSON must have a \"widgets\" list!")
		}
	}
	if err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	curV := fakeVersion(cur)
	if f.concurrentEditPut {
		f.concurrentEditPut = false
		curV++
		cur["__v"] = curV
		cur["title"] = "Edited concurrently"
	}
	if ev, ok := body["expectedVersion"].(float64); ok {
		f.putsWithVersion++
		if ev != curV {
			writeFakeJSON(w, http.StatusConflict, map[string]any{"message": fmt.Sprintf("Dashboard was modified (version %v, expected %v)", curV, ev)})
			return
		}
	} else {
		f.putsNoVersion++
	}
	stripFakeReadonly(body)
	if err := fakeValidate(body); err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	// The writable fields in the body; the optional ones it leaves out are
	// cleared, unless the fake models an older server's $set.
	board := map[string]any{}
	for k, v := range cur {
		board[k] = v
	}
	if !f.legacySetPut {
		for _, k := range fakeClearedOnPut {
			if _, has := body[k]; !has {
				delete(board, k)
			}
		}
	}
	for k, v := range fakePick(body, fakeBoardWritable) {
		board[k] = v
	}
	f.fakeStoreTemplateVariables(board)
	fakeStorePresets(board)
	fakeMinimize(board)
	widgets, idMap, ids := f.storeWidgets(body["widgets"].([]any), body["widgets"].([]any), id, false)
	board["widgets"] = widgets
	delete(board, "tabs")
	if tabs := f.fakeRebuildTabs(body["tabs"], idMap, ids); tabs != nil {
		board["tabs"] = tabs
	}
	f.stamp(board, id, curV+1)
	f.boards[id] = board
	writeFakeJSON(w, http.StatusOK, board)
}

func (f *fakeDashboardAPI) delete(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := r.PathValue("id")
	if _, ok := f.boards[id]; !ok {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Dashboard not found"})
		return
	}
	delete(f.boards, id)
	writeFakeJSON(w, http.StatusOK, map[string]any{})
}

func fakeVersion(b map[string]any) float64 {
	v, _ := b["__v"].(float64)
	return v
}

// editOutOfBand changes a board as a user in the UI would (bumps __v).
func (f *fakeDashboardAPI) editOutOfBand(id string, edit func(board map[string]any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.boards[id]
	edit(b)
	b["__v"] = fakeVersion(b) + 1
}

func (f *fakeDashboardAPI) deleteOutOfBand(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.boards, id)
}

func (f *fakeDashboardAPI) board(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.boards[id]
}

func (f *fakeDashboardAPI) boardCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.boards)
}

// onlyBoardID returns the id of the single stored board ("" otherwise).
func (f *fakeDashboardAPI) onlyBoardID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.boards) != 1 {
		return ""
	}
	for id := range f.boards {
		return id
	}
	return ""
}

func (f *fakeDashboardAPI) setConcurrentEditOnNextPut() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.concurrentEditPut = true
}
