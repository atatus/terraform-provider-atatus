package provider

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

var hclName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestDashboardModel_parses(t *testing.T) {
	m, err := loadDashboardModel()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Types) != 34 || len(m.Widget.Variants) != 34 {
		t.Fatalf("widget types: %d (variants %d), want 34", len(m.Types), len(m.Widget.Variants))
	}
	for _, want := range []string{"title", "description", "dashboard_type", "layout_type", "template_variables", "time_dur", "space_ids"} {
		if m.Board.prop(want) == nil {
			t.Errorf("board attribute %q missing", want)
		}
	}
	ts := m.Widget.Variants["timeseries"]
	def := ts.prop("definition")
	if def == nil || def.Node.prop("legend_mode") == nil || def.Node.prop("y_buckets") != nil {
		t.Fatalf("timeseries definition attributes wrong: %+v", def)
	}
	if ts.prop("requests") == nil || !ts.prop("requests").Required {
		t.Error("timeseries requests should be required")
	}
	if m.Widget.Variants["note"].prop("requests") != nil || m.Widget.Variants["note"].prop("widgets") != nil {
		t.Error("a note should have no requests or widgets attribute")
	}
	group := m.Widget.Variants["group"].prop("widgets")
	if group == nil || group.Node.Items.Kind != dsUnion || group.Node.Items.Variants["group"] != nil {
		t.Error("group children should be a widget union without group")
	}
	if m.ResponseFormats["timeseries"] != "timeseries" || m.ResponseFormats["list_stream"] != "event_list" {
		t.Errorf("response formats: %v", m.ResponseFormats)
	}
	// Every attribute name is a valid HCL identifier.
	var walk func(n *dsNode, at string)
	walk = func(n *dsNode, at string) {
		for _, p := range n.Props {
			if !hclName.MatchString(p.HCL) {
				t.Errorf("%s.%s is not a valid attribute name", at, p.HCL)
			}
			walk(p.Node, at+"."+p.HCL)
		}
		if n.Items != nil {
			walk(n.Items, at+"[]")
		}
		for name, v := range n.Variants {
			walk(v, at+"<"+name+">")
		}
	}
	walk(m.Board, "dashboard")
	walk(m.Widget, "widget")
}

func TestTypedDashboard_schemaIsValid(t *testing.T) {
	r := NewTypedDashboardResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if d := resp.Schema.ValidateImplementation(context.Background()); d.HasError() {
		t.Fatal(d)
	}
}

func TestDashboardModel_roundTrip(t *testing.T) {
	m, _ := loadDashboardModel()
	w := m.Widget.Variants["timeseries"]
	in := map[string]any{
		"title": "CPU", "layout": map[string]any{"x": 0.0, "y": 0.0, "width": 6.0, "height": 3.0},
		"definition": map[string]any{"legend_mode": "auto", "yaxis": map[string]any{"min": 0.0, "scale": "log"},
			"markers": []any{map[string]any{"type": "line", "value": 90.0, "severity": "error"}}},
		"requests": []any{map[string]any{"responseFormat": "timeseries", "displayType": "line",
			"queries": []any{map[string]any{"name": "q", "dataSource": "metrics", "query": "avg:system.cpu.user{*}"}}}},
	}
	v := w.fromJSON(in, nil, true, "w", &readCtx{dropped: map[string]bool{}})
	out, ok, err := w.toJSON(v)
	if err != nil || !ok {
		t.Fatal(err)
	}
	a, _ := json.Marshal(in)
	b, _ := json.Marshal(out)
	if canonicalJSON(json.RawMessage(a)) != canonicalJSON(json.RawMessage(b)) {
		t.Fatalf("round trip changed the widget:\n in: %s\nout: %s", a, b)
	}
	if !strings.Contains(string(b), `"min":0`) {
		t.Errorf("axis min (a JSON attribute) lost: %s", b)
	}
}

// Older boards hold an upper-case sort order; Atatus stores new ones lower-cased and the
// configuration accepts only that spelling, so it is read lower-cased (import must not fail).
func TestDashboardModel_sortOrderReadLowercased(t *testing.T) {
	m, _ := loadDashboardModel()
	w := m.Widget.Variants["toplist"]
	in := map[string]any{"requests": []any{map[string]any{
		"queries": []any{map[string]any{"name": "q", "dataSource": "metrics"}},
		"sort":    map[string]any{"count": 10.0, "orderBy": []any{map[string]any{"type": "formula", "index": 0.0, "order": "DESC"}}},
	}}}
	v := w.fromJSON(in, nil, true, "w", &readCtx{dropped: map[string]bool{}})
	out, _, err := w.toJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"order":"desc"`) {
		t.Fatalf("sort order not read lower-cased: %s", b)
	}
}
