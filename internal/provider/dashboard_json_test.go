package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func mustDecode(t *testing.T, s string) map[string]any {
	t.Helper()
	m, err := decodeJSONObject(s)
	if err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return m
}

func mustCanonical(t *testing.T, v any) string {
	t.Helper()
	s, err := marshalCanonical(v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDecodeJSONObject(t *testing.T) {
	for _, bad := range []string{``, `[]`, `"x"`, `12`, `{"a":1} {"b":2}`, `{"a":`, `null`} {
		if _, err := decodeJSONObject(bad); err == nil {
			t.Errorf("decodeJSONObject(%q) accepted a non-object", bad)
		}
	}
	if _, err := decodeJSONObject(" {\"a\": 1}\n\t "); err != nil {
		t.Errorf("trailing whitespace rejected: %v", err)
	}
}

func TestCanonicalDashboardJSON_orderWhitespaceNumbersAndServerKeys(t *testing.T) {
	a := `{"title":"T","widgets":[{"definition":{"type":"note","v":1.50,"big":12345678901234567890}}]}`
	b := `{
	  "_id": "abc", "__v": 7, "url": "https://example.test/d/abc", "accountId": "x", "createdAt": "2026",
	  "widgets": [ { "_id": "w1", "dashboardId": "abc", "data": {"rows": 1}, "updatedAt": "2026",
	                 "definition": { "big": 12345678901234567890, "v": 1.50, "type": "note" } } ],
	  "title": "T"
	}`
	ca, err := canonicalDashboardJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := canonicalDashboardJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if ca != cb {
		t.Fatalf("canonical forms differ:\n%s\n%s", ca, cb)
	}
	// Numbers compare as the double the server stores: 1.50 is 1.5, and an
	// integer above 2^53 is rounded as Node's JSON.parse rounds it.
	if !strings.Contains(ca, `"big":12345678901234567000`) || !strings.Contains(ca, `"v":1.5`) {
		t.Fatalf("numbers not normalised: %s", ca)
	}
	// Sorted keys.
	if ca != `{"title":"T","widgets":[{"definition":{"big":12345678901234567000,"type":"note","v":1.5}}]}` {
		t.Fatalf("unexpected canonical form: %s", ca)
	}
	// No HTML escaping of <, >, &.
	c, _ := canonicalDashboardJSON(`{"title":"a<b>&c"}`)
	if c != `{"title":"a<b>&c"}` {
		t.Fatalf("HTML-escaped: %s", c)
	}
}

func TestStripServerKeys_matchesReactExportList(t *testing.T) {
	// The lists follow export-dashboard-json.ts, except "spaceIds": the server
	// writes it from the document, so it is tracked, not ignored; and
	// "restrictedTo", which atatus_dashboard_restriction owns.
	wantBoard := "__v _id accountId authorId authorName authorEmail createdAt updatedAt isFavorited userCanEdit userIsBoardAdmin powerpackStatus isTemplate url restrictedTo"
	wantWidget := "__v _id createdAt updatedAt accountId dashboardId data"
	if got := strings.Join(dashboardServerKeys, " "); got != wantBoard {
		t.Errorf("board keys = %s", got)
	}
	if got := strings.Join(widgetServerKeys, " "); got != wantWidget {
		t.Errorf("widget keys = %s", got)
	}
	doc := mustDecode(t, `{"id":"keep","title":"T","widgets":[{"id":"w-keep","_id":"x"}]}`)
	stripServerKeys(doc)
	if got := mustCanonical(t, doc); got != `{"id":"keep","title":"T","widgets":[{"id":"w-keep"}]}` {
		t.Fatalf("strip removed too much or too little: %s", got)
	}
}

func TestPruneToPrior(t *testing.T) {
	server := mustDecode(t, `{
	  "title": "T", "layoutType": "ordered",
	  "settings": {"a": 1, "added": true, "nested": {"x": 1, "y": 2}},
	  "list": [{"k": 1, "def": 0}, {"k": 2, "def": 0}],
	  "grown": [{"k": 1, "def": 0}, {"k": 2, "def": 0}],
	  "scalarBecameObject": {"z": 1}
	}`)
	prior := mustDecode(t, `{
	  "title": "Old", "settings": {"a": 1, "nested": {"x": 1}},
	  "list": [{"k": 1}, {"k": 9}],
	  "grown": [{"k": 1}],
	  "scalarBecameObject": "s",
	  "removedByServer": 1
	}`)
	got := mustCanonical(t, pruneToPrior(server, prior))
	want := `{"grown":[{"def":0,"k":1},{"def":0,"k":2}],"list":[{"k":1},{"k":2}],"scalarBecameObject":{"z":1},"settings":{"a":1,"nested":{"x":1}},"title":"T"}`
	if got != want {
		t.Fatalf("prune:\n got %s\nwant %s", got, want)
	}
}

func TestCanonicalizeServerDashboard_importKeepsEverything(t *testing.T) {
	server := mustDecode(t, `{"_id":"d1","__v":3,"url":"u","title":"T","layoutType":"ordered",
	  "widgets":[{"_id":"s1","dashboardId":"d1","definition":{"type":"note","precision":2}}]}`)
	got := mustCanonical(t, canonicalizeServerDashboard(server, nil))
	want := `{"layoutType":"ordered","title":"T","widgets":[{"definition":{"precision":2,"type":"note"}}]}`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestCanonicalizeServerDashboard_importTabbedEmitsIDs(t *testing.T) {
	server := mustDecode(t, `{"_id":"d1","title":"T","tabs":[{"name":"A","widgetIds":["s2","s1"]}],
	  "widgets":[{"_id":"s1","definition":{"type":"note"}},{"_id":"s2","definition":{"type":"note"}}]}`)
	got := mustCanonical(t, canonicalizeServerDashboard(server, nil))
	want := `{"tabs":[{"name":"A","widgetIds":["s2","s1"]}],"title":"T","widgets":[{"definition":{"type":"note"},"id":"s1"},{"definition":{"type":"note"},"id":"s2"}]}`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestCanonicalizeServerDashboard_mapsIDsByPositionAndRewritesTabs(t *testing.T) {
	prior := mustDecode(t, `{"title":"T","tabs":[{"name":"A","widgetIds":["mine-b","mine-a"]}],
	  "widgets":[{"id":"mine-a","definition":{"type":"note"}},{"id":"mine-b","definition":{"type":"note"}}]}`)
	server := mustDecode(t, `{"_id":"d1","__v":4,"title":"T","layoutType":"ordered",
	  "tabs":[{"name":"A","widgetIds":["new2","new1"],"serverDefault":true}],
	  "widgets":[{"_id":"new1","definition":{"type":"note","precision":2}},{"_id":"new2","definition":{"type":"note"}}]}`)
	got := mustCanonical(t, canonicalizeServerDashboard(server, prior))
	want := mustCanonical(t, prior)
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestCanonicalizeServerDashboard_extraServerWidgetShowsAsDrift(t *testing.T) {
	prior := mustDecode(t, `{"title":"T","tabs":[{"name":"A","widgetIds":["a"]}],
	  "widgets":[{"id":"a","definition":{"type":"note"}}]}`)
	server := mustDecode(t, `{"_id":"d1","title":"T","tabs":[{"name":"A","widgetIds":["s1","s2"]}],
	  "widgets":[{"_id":"s1","definition":{"type":"note"}},{"_id":"s2","definition":{"type":"toplist"}}]}`)
	got := mustCanonical(t, canonicalizeServerDashboard(server, prior))
	// The widget count changed, so positions no longer line up: every widget
	// shows its server id rather than a prior id that may belong to another widget.
	want := `{"tabs":[{"name":"A","widgetIds":["s1","s2"]}],"title":"T","widgets":[{"definition":{"type":"note"},"id":"s1"},{"definition":{"type":"toplist"},"id":"s2"}]}`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if dashboardJSONSemanticallyEqual(mustCanonical(t, prior), got) {
		t.Fatal("an added widget must not be semantically equal")
	}
}

func TestCanonicalizeServerDashboard_untabbedIDsRestored(t *testing.T) {
	// The importer drops "id" from untabbed widgets; the user's ids are restored by position.
	prior := mustDecode(t, `{"title":"T","widgets":[{"id":"x","definition":{"type":"note"}}]}`)
	server := mustDecode(t, `{"_id":"d","title":"T","widgets":[{"_id":"s9","definition":{"type":"note"}}]}`)
	if got, want := mustCanonical(t, canonicalizeServerDashboard(server, prior)), mustCanonical(t, prior); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestDashboardJSONValue_semanticEquals(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		a, b string
		want bool
	}{
		{`{"a":1,"b":[1,2]}`, `{ "b": [1, 2], "a": 1 }`, true},
		{`{"title":"T"}`, `{"title":"T","_id":"x","__v":2,"url":"u","updatedAt":"now"}`, true},
		{`{"title":"T"}`, `{"title":"U"}`, false},
		{`{"a":[1,2]}`, `{"a":[2,1]}`, false},
		{`{"n":1}`, `{"n":1.0}`, true},
		{`{"n":1000}`, `{"n":1e3}`, true},
		{`{"n":0.5}`, `{"n":0.50}`, true},
		{`{"n":1}`, `{"n":1.000001}`, false},
		{`{"widgets":[{"type":"group","widgets":[{"_id":"c-old","type":"note"}]}]}`, `{"widgets":[{"type":"group","widgets":[{"_id":"c-new","type":"note"}]}]}`, true},
		{`{"widgets":[{"type":"group","widgets":[{"type":"note","text":"a"}]}]}`, `{"widgets":[{"type":"group","widgets":[{"type":"note","text":"b"}]}]}`, false},
		{`{"widgets":[{"id":"a"}]}`, `{"widgets":[{"id":"b"}]}`, false},
		{`not json`, `{"a":1}`, false},
	}
	for _, c := range cases {
		got, diags := newDashboardJSONValue(c.a).StringSemanticEquals(ctx, newDashboardJSONValue(c.b))
		if diags.HasError() {
			t.Fatalf("diags: %v", diags)
		}
		if got != c.want {
			t.Errorf("semanticEquals(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	// Null / unknown are never semantically equal to a document.
	null := dashboardJSONValue{StringValue: types.StringNull()}
	if eq, _ := null.StringSemanticEquals(ctx, newDashboardJSONValue(`{}`)); eq {
		t.Error("null equal to {}")
	}
}

func TestDashboardJSONType_roundTrip(t *testing.T) {
	ctx := context.Background()
	typ := dashboardJSONType{}
	v, diags := typ.ValueFromString(ctx, types.StringValue(`{"a":1}`))
	if diags.HasError() {
		t.Fatal(diags)
	}
	tv, err := v.ToTerraformValue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	back, err := typ.ValueFromTerraform(ctx, tv)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Equal(v.(dashboardJSONValue)) || !back.Type(ctx).Equal(typ) {
		t.Fatalf("round trip lost the custom type: %#v", back)
	}
}

func TestDashboardJSONObjectValidator(t *testing.T) {
	ctx := context.Background()
	for s, wantErr := range map[string]bool{`{"title":"x"}`: false, `[1]`: true, `{bad`: true} {
		resp := &validator.StringResponse{}
		dashboardJSONObjectValidator{}.ValidateString(ctx, validator.StringRequest{Path: path.Root("dashboard_json"), ConfigValue: types.StringValue(s)}, resp)
		if resp.Diagnostics.HasError() != wantErr {
			t.Errorf("%s: error=%v, want %v (%v)", s, resp.Diagnostics.HasError(), wantErr, resp.Diagnostics)
		}
	}
	resp := &validator.StringResponse{}
	dashboardJSONObjectValidator{}.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringUnknown()}, resp)
	if resp.Diagnostics.HasError() {
		t.Error("unknown value must not be validated")
	}
}

func TestApplyServerDashboard_keepsPriorTextWhenEqual(t *testing.T) {
	prior := "{\n  \"widgets\": [{\"definition\": {\"type\": \"note\"}}],\n  \"title\": \"T\"\n}"
	raw := []byte(`{"_id":"d7","__v":2,"url":"https://example.test/d7","title":"T","layoutType":"ordered",
	  "widgets":[{"_id":"w1","definition":{"type":"note","precision":2}}]}`)
	var m dashboardResourceModel
	if diags := applyServerDashboard(raw, prior, &m); diags.HasError() {
		t.Fatal(diags)
	}
	if m.DashboardJSON.ValueString() != prior {
		t.Fatalf("prior text not kept: %s", m.DashboardJSON.ValueString())
	}
	if m.ID.ValueString() != "d7" || m.Version.ValueInt64() != 2 || m.URL.ValueString() != "https://example.test/d7" {
		t.Fatalf("computed fields wrong: %+v", m)
	}

	// A real change on the server replaces the text with the canonical document.
	raw = []byte(`{"_id":"d7","__v":3,"title":"Changed","widgets":[{"_id":"w1","definition":{"type":"note"}}]}`)
	if diags := applyServerDashboard(raw, prior, &m); diags.HasError() {
		t.Fatal(diags)
	}
	if got := m.DashboardJSON.ValueString(); got != `{"title":"Changed","widgets":[{"definition":{"type":"note"}}]}` {
		t.Fatalf("drift not stored canonically: %s", got)
	}
}

func TestNormalizeNumber(t *testing.T) {
	for in, want := range map[string]string{
		"4": "4", "4.0": "4", "4e0": "4", "1e3": "1000", "1E+3": "1000", "0.50": "0.5", "-0.0": "0",
		"1.25e-2": "0.0125", "12345678901234567890.0": "12345678901234567000", "0.1": "0.1",
		// Rounded to the nearest double, as Node's JSON.parse stores them.
		"9007199254740993": "9007199254740992", "0.12345678901234567890": "0.12345678901234568",
		"1e21": "1e+21", "1e-7": "1e-07",
	} {
		got, ok := normalizeNumber(json.Number(in)).(json.Number)
		if !ok || string(got) != want {
			t.Errorf("normalizeNumber(%s) = %v, want %s", in, got, want)
		}
	}
	// Too large for a double: JSON.stringify writes Infinity as null.
	for _, in := range []string{"1e400", "-1e999999999"} {
		if got := normalizeNumber(json.Number(in)); got != nil {
			t.Errorf("normalizeNumber(%s) = %v, want nil", in, got)
		}
	}
	if !dashboardJSONSemanticallyEqual(`{"v":9007199254740993}`, `{"v":9007199254740992}`) {
		t.Error("an integer above 2^53 must equal its double")
	}
	if !dashboardJSONSemanticallyEqual(`{"v":1e400}`, `{"v":null}`) {
		t.Error("an overflowing number must equal null")
	}
}

// The server mints a new id for every tab on each create and update.
func TestCanonicalizeServerDashboard_tabIDsMappedByPosition(t *testing.T) {
	prior := mustDecode(t, `{"title":"T","tabs":[{"id":"tab-a","name":"A","widgetIds":["w1"]},{"id":"tab-b","name":"B","widgetIds":["w2"]}],
	  "widgets":[{"id":"w1","type":"note"},{"id":"w2","type":"note"}]}`)
	server := mustDecode(t, `{"_id":"d","title":"T","tabs":[{"id":"NEW-UUID-1","name":"A","widgetIds":["s1"]},{"id":"NEW-UUID-2","name":"B","widgetIds":["s2"]}],
	  "widgets":[{"_id":"s1","type":"note"},{"_id":"s2","type":"note"}]}`)
	if got, want := mustCanonical(t, canonicalizeServerDashboard(server, prior)), mustCanonical(t, prior); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	// A tab added in the UI: counts differ, the server tab ids are shown.
	server = mustDecode(t, `{"_id":"d","title":"T","tabs":[{"id":"N1","name":"A","widgetIds":["s1"]},{"id":"N2","name":"B","widgetIds":["s2"]},{"id":"N3","name":"C","widgetIds":[]}],
	  "widgets":[{"_id":"s1","type":"note"},{"_id":"s2","type":"note"}]}`)
	got := mustCanonical(t, canonicalizeServerDashboard(server, prior))
	if !strings.Contains(got, `"id":"N3"`) || dashboardJSONSemanticallyEqual(mustCanonical(t, prior), got) {
		t.Fatalf("added tab not shown as drift: %s", got)
	}
}

// Export → JSON keeps a group child's _id; the server re-mints it on create.
func TestApplyServerDashboard_groupChildIDsIgnored(t *testing.T) {
	prior := `{"title":"T","widgets":[{"type":"group","widgets":[{"_id":"c-old","type":"note","text":"x"}]}]}`
	raw := []byte(`{"_id":"d","__v":0,"title":"T","widgets":[{"_id":"s1","type":"group","widgets":[{"_id":"c-new","type":"note","text":"x"}]}]}`)
	var m dashboardResourceModel
	if diags := applyServerDashboard(raw, prior, &m); diags.HasError() {
		t.Fatal(diags)
	}
	if m.DashboardJSON.ValueString() != prior {
		t.Fatalf("re-minted child _id caused a difference: %s", m.DashboardJSON.ValueString())
	}
}

// Node writes 4.0 as 4; the planned text must still be kept.
func TestApplyServerDashboard_numbersByValue(t *testing.T) {
	prior := `{"title":"T","widgets":[{"type":"note","layout":{"width":4.0,"x":1e1}}]}`
	raw := []byte(`{"_id":"d","__v":0,"title":"T","widgets":[{"_id":"s1","type":"note","layout":{"width":4,"x":10}}]}`)
	var m dashboardResourceModel
	if diags := applyServerDashboard(raw, prior, &m); diags.HasError() {
		t.Fatal(diags)
	}
	if m.DashboardJSON.ValueString() != prior {
		t.Fatalf("number formatting caused a difference: %s", m.DashboardJSON.ValueString())
	}
}

// A widget deleted in the UI must not shift the remaining widgets onto the
// wrong ids: with the count changed, server ids are shown.
func TestCanonicalizeServerDashboard_deletedWidgetKeepsIdentity(t *testing.T) {
	prior := mustDecode(t, `{"title":"T","tabs":[{"id":"t","name":"A","widgetIds":["a","b","c"]}],
	  "widgets":[{"id":"a","type":"note","title":"A"},{"id":"b","type":"note","title":"B"},{"id":"c","type":"note","title":"C"}]}`)
	server := mustDecode(t, `{"_id":"d","title":"T","tabs":[{"id":"t2","name":"A","widgetIds":["s2","s3"]}],
	  "widgets":[{"_id":"s2","type":"note","title":"B"},{"_id":"s3","type":"note","title":"C"}]}`)
	got := mustCanonical(t, canonicalizeServerDashboard(server, prior))
	want := `{"tabs":[{"id":"t","name":"A","widgetIds":["s2","s3"]}],"title":"T","widgets":[{"id":"s2","title":"B","type":"note"},{"id":"s3","title":"C","type":"note"}]}`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

// With a prior document, a widget the user wrote without an id never gains one.
func TestCanonicalizeServerDashboard_noIDAddedWhenPriorHadNone(t *testing.T) {
	prior := mustDecode(t, `{"title":"T","widgets":[{"type":"note"},{"type":"note"}]}`)
	server := mustDecode(t, `{"_id":"d","title":"T","widgets":[{"_id":"s1","type":"note"},{"_id":"s2","type":"note"}]}`)
	if got, want := mustCanonical(t, canonicalizeServerDashboard(server, prior)), mustCanonical(t, prior); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestDashboardDocumentProblems(t *testing.T) {
	cases := map[string][]string{
		// Stored shape, as Export → JSON writes it.
		`{"title":"T","widgets":[{"type":"note","title":"x","requests":[1],"definition":{"precision":2}}]}`: nil,
		// A hostmap keeps definition.requests / description; a widget's own requests win.
		`{"title":"T","widgets":[{"type":"hostmap","definition":{"requests":{},"description":"d"}},{"type":"toplist","requests":[],"definition":{"requests":{}}}]}`: nil,
		`{"title":"T","widgets":[{"definition":{"type":"note","title":"Runbook"}}]}`: {
			`widgets[0].definition.type: put "type"`, `widgets[0].definition.title: put "title"`,
		},
		`{"title":"T","widgets":[{"type":"timeseries","definition":{"requests":[],"description":"d"}}]}`: {
			`widgets[0].definition.requests`, `widgets[0].definition.description`,
		},
		// Tabbed boards: every widget has an id and sits in exactly one tab.
		`{"title":"T","tabs":[{"name":"A","widgetIds":["a"]},{"name":"B","widgetIds":["b"]}],"widgets":[{"id":"a","type":"note"},{"id":"b","type":"note"}]}`: nil,
		`{"title":"T","tabs":[{"name":"A","widgetIds":["w1"]}],"widgets":[{"id":"w1","type":"note"},{"type":"note"}]}`: {
			`widgets[1] needs an "id"`,
		},
		`{"title":"T","tabs":[{"name":"A","widgetIds":["a","zz"]},{"name":"B","widgetIds":["a"]}],"widgets":[{"id":"a","type":"note"},{"id":"b","type":"note"},{"id":"b","type":"note"}]}`: {
			`widgets[2]: id "b" is used by more than one widget`, `tabs[0].widgetIds: "zz" is not the id`,
			`tabs[1].widgetIds: widget "a" is already in a tab`, `widgets[1] (id "b") is in no tab`,
		},
		// Group children are checked like top-level widgets: create lifts their definition keys too.
		`{"title":"T","widgets":[{"type":"group","widgets":[{"type":"note","text":"x","definition":{"description":"why","title":"child"}}]}]}`: {
			`widgets[0].widgets[0].definition.title: put "title"`, `widgets[0].widgets[0].definition.description: put "description"`,
		},
		// Keys the server does not store, or renames.
		`{"title":"T","reflow_type":"auto","titel":"x","importDashboard":true,"widgets":[{"type":"note","requets":[],"title_size":2}]}`: {
			`importDashboard: Atatus does not store this key`, `reflow_type: write it as "reflowType"`, `titel: Atatus does not store this key`,
			`widgets[0].requets: Atatus does not store`, `widgets[0].title_size: write it as "titleSize"`,
		},
		`{"title":"T","templateVariables":[{"name":"env","available_values":["a"]}],"widgets":[]}`: {
			`templateVariables[0].available_values: write it as "availableValues"`,
		},
		// Forms the server rewrites.
		`{"title":"T","widgets":[{"type":"timeseries","definition":{"showLegend":true,"markers":[{"value":"y = 50","display_type":"error dashed"}]},
		  "requests":[{"displayType":"bars","sort":{"orderBy":[{"order":"DESC"}]},"queries":[{"dataSource":"mobile_rum","filters":["env:prod"],"compute":{"metricName":"mobile_rum.view.count"}}]}]}]}`: {
			`widgets[0].definition.showLegend: write it as "show_legend"`,
			`widgets[0].definition.markers[0].value: must be a number`,
			`widgets[0].definition.markers[0].display_type: use "severity"`,
			`widgets[0].requests[0].displayType: write "bar"`,
			`widgets[0].requests[0].queries[0].filters: write one string`,
			`widgets[0].requests[0].queries[0].dataSource: "mobile_rum" is retired`,
			`widgets[0].requests[0].queries[0].compute.metricName: "mobile_rum.view.count" is retired`,
			`widgets[0].requests[0].sort.orderBy[0].order: write "desc"`,
		},
		// A distribution's markers are percentiles, which the server leaves alone.
		`{"title":"T","widgets":[{"type":"distribution","definition":{"markers":[{"value":"p90"}]}}]}`: nil,
		// Tab names the server trims, replaces or shortens.
		`{"title":"T","tabs":[{"name":" A ","widgetIds":["a"]},{"name":"","widgetIds":["b"]},{"name":"` + strings.Repeat("n", 101) + `","widgetIds":["c"]}],
		  "widgets":[{"id":"a","type":"note"},{"id":"b","type":"note"},{"id":"c","type":"note"}]}`: {
			`tabs[0].name: remove the leading or trailing spaces`, `tabs[1].name: give the tab a non-empty name`, `tabs[2].name: at most 100 characters`,
		},
		// Numeric widget ids are valid: the server links them by String(id).
		`{"title":"T","tabs":[{"name":"A","widgetIds":[1]},{"name":"B","widgetIds":[2]}],"widgets":[{"id":1,"type":"note"},{"id":2,"type":"note"}]}`: nil,
		// spaceIds is a writable board key.
		`{"title":"T","spaceIds":["s1"],"widgets":[]}`: nil,
	}
	for doc, want := range cases {
		got := dashboardDocumentProblems(mustDecode(t, doc))
		if len(got) != len(want) {
			t.Errorf("%s:\n got %q\nwant %q", doc, got, want)
			continue
		}
		for i := range want {
			if !strings.Contains(got[i], want[i]) {
				t.Errorf("%s: problem %d = %q, want it to contain %q", doc, i, got[i], want[i])
			}
		}
	}
}

func TestBoardKeysKeptByServer(t *testing.T) {
	prior := `{"title":"T","description":"old","timeDur":"1d","tabs":[],"widgets":[]}`
	planned := `{"title":"T2","widgets":[]}`
	raw := json.RawMessage(`{"_id":"d","__v":3,"url":"u","title":"T2","description":"old","layoutType":"ordered","widgets":[]}`)
	got := boardKeysKeptByServer(prior, planned, raw)
	if strings.Join(got, ",") != "description" {
		t.Fatalf("got %v, want [description] (timeDur was cleared, tabs/widgets always replaced, layoutType was never in prior)", got)
	}
}

func TestDashboardDocumentProblems_tooManyTabs(t *testing.T) {
	var tabs, widgets []string
	for i := 0; i <= maxDashboardTabs; i++ {
		tabs = append(tabs, fmt.Sprintf(`{"name":"T%d","widgetIds":["w%d"]}`, i, i))
		widgets = append(widgets, fmt.Sprintf(`{"id":"w%d","type":"note"}`, i))
	}
	doc := mustDecode(t, `{"title":"T","tabs":[`+strings.Join(tabs, ",")+`],"widgets":[`+strings.Join(widgets, ",")+`]}`)
	got := dashboardDocumentProblems(doc)
	if len(got) != 1 || !strings.Contains(got[0], "at most 30 tabs") {
		t.Fatalf("got %q", got)
	}
}

// Create drops every "_id" in the body and Mongo mints new ones for
// templateVariables[] and requests[].query.sort[] / columns[] entries, so
// Export → JSON's nested _ids must not make the result differ from the plan.
func TestApplyServerDashboard_nestedSubdocumentIDsIgnored(t *testing.T) {
	prior := `{"title":"T","templateVariables":[{"_id":"65a000000000000000000001","name":"env","prefix":"env"}],
	  "widgets":[{"type":"list_stream","requests":[{"query":{"dataSource":"logs","sort":[{"_id":"65a000000000000000000002","column":"ts","order":"desc"}]}}]}]}`
	raw := []byte(`{"_id":"d","__v":0,"title":"T","templateVariables":[{"_id":"NEW1","name":"env","prefix":"env","availableValues":[],"default":[]}],
	  "widgets":[{"_id":"s1","type":"list_stream","requests":[{"responseFormat":"scalar","query":{"dataSource":"logs","sort":[{"_id":"NEW2","column":"ts","order":"desc"}]}}]}]}`)
	var m dashboardResourceModel
	if diags := applyServerDashboard(raw, prior, &m); diags.HasError() {
		t.Fatal(diags)
	}
	if m.DashboardJSON.ValueString() != prior {
		t.Fatalf("re-minted nested _id caused a difference: %s", m.DashboardJSON.ValueString())
	}
}

// Mongoose minimize stores no empty objects: "definition": {} reads back absent.
func TestApplyServerDashboard_emptyObjectsMatchAbsent(t *testing.T) {
	prior := `{"title":"T","widgets":[{"type":"note","text":"x","definition":{}},{"type":"timeseries","definition":{"yaxis":{},"palette":"dog"}}]}`
	raw := []byte(`{"_id":"d","__v":0,"title":"T","widgets":[{"_id":"s1","type":"note","text":"x"},{"_id":"s2","type":"timeseries","definition":{"palette":"dog"}}]}`)
	var m dashboardResourceModel
	if diags := applyServerDashboard(raw, prior, &m); diags.HasError() {
		t.Fatal(diags)
	}
	if m.DashboardJSON.ValueString() != prior {
		t.Fatalf("minimized empty objects caused a difference: %s", m.DashboardJSON.ValueString())
	}
	// An empty object inside an array is a value of its own and still compares.
	if dashboardJSONSemanticallyEqual(`{"a":[{}]}`, `{"a":[]}`) {
		t.Error("[{}] must not equal []")
	}
}

// Numeric widget ids: the server links tabs by String(id); the prior numbers
// are written back into tabs[].widgetIds.
func TestCanonicalizeServerDashboard_numericWidgetIDs(t *testing.T) {
	prior := mustDecode(t, `{"title":"T","tabs":[{"name":"A","widgetIds":[1]},{"name":"B","widgetIds":[2]}],
	  "widgets":[{"id":1,"type":"note"},{"id":2,"type":"note"}]}`)
	server := mustDecode(t, `{"_id":"d","title":"T","tabs":[{"id":"N1","name":"A","widgetIds":["s1"]},{"id":"N2","name":"B","widgetIds":["s2"]}],
	  "widgets":[{"_id":"s1","type":"note"},{"_id":"s2","type":"note"}]}`)
	if got, want := mustCanonical(t, canonicalizeServerDashboard(server, prior)), mustCanonical(t, prior); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

// spaceIds is written by the server, so a change to it is a real change.
func TestDashboardJSON_spaceIDsTracked(t *testing.T) {
	if dashboardJSONSemanticallyEqual(`{"title":"T","spaceIds":["a"]}`, `{"title":"T","spaceIds":["b"]}`) {
		t.Fatal("a spaceIds change must not be ignored")
	}
}

// On import the server's default empty arrays are dropped (they would plan an
// update right after import), except the board's own widgets and tabs.
func TestCanonicalizeServerDashboard_importDropsDefaultEmptyArrays(t *testing.T) {
	server := map[string]any{
		"_id": "b1", "title": "T", "spaceIds": []any{}, "templateVariables": []any{}, "tabs": []any{},
		"widgets": []any{
			map[string]any{"_id": "w1", "type": "note", "requests": []any{}, "widgets": []any{}, "legendColumns": []any{},
				"definition": map[string]any{"content": "x", "markers": []any{}}},
		},
	}
	doc := canonicalizeServerDashboard(server, nil)
	for _, k := range []string{"spaceIds", "templateVariables"} {
		if _, has := doc[k]; has {
			t.Errorf("board key %q kept", k)
		}
	}
	if _, has := doc["tabs"]; !has {
		t.Error("board tabs dropped")
	}
	w := doc["widgets"].([]any)[0].(map[string]any)
	for _, k := range []string{"requests", "widgets", "legendColumns"} {
		if _, has := w[k]; has {
			t.Errorf("widget key %q kept", k)
		}
	}
	if _, has := w["definition"].(map[string]any)["markers"]; has {
		t.Error("nested empty array kept")
	}

	empty := canonicalizeServerDashboard(map[string]any{"title": "E", "widgets": []any{}}, nil)
	if _, has := empty["widgets"]; !has {
		t.Error("an empty board's widgets list must stay")
	}
}
