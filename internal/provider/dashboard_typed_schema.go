package provider

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The typed atatus_dashboard is built from the dashboard JSON Schema Atatus
// serves at GET /v2/dashboards/schema (atatus-server
// common/dashboard-widget-spec.js), pinned here. The Terraform schema, the
// HCL <-> JSON conversion and the per-type widget attributes all come from it,
// so a new widget type or setting is supported by replacing this file
// (scripts/update-dashboard-schema.sh) rather than by writing code.
//
// KEEP IN STEP: atatus-server common/dashboard-terraform-hcl.js renders a
// board as HCL for this resource with a port of these rules (snakeName, the
// *_json kinds, the widget union, and the fill-mode read in
// dashboard_typed_convert.go / stateFromServer). Change a naming or reading
// rule in both places; an import of the rendered HCL must still plan no changes.
//
//go:embed dashboardschema/dashboard.schema.json
var dashboardSchemaJSON []byte

type dsKind int

const (
	dsString dsKind = iota
	dsNumber
	dsInt
	dsBool
	dsJSON // a free-form value, written as a JSON string (jsonencode)
	dsObject
	dsList
	dsUnion // a widget: one attribute per widget type, exactly one set
)

// dsNode is one value in the dashboard document, as the typed resource sees it.
type dsNode struct {
	Kind     dsKind
	Desc     string
	Enum     []any
	Min, Max *float64
	MaxItems int
	Pattern  string
	Props    []*dsProp          // dsObject
	Items    *dsNode            // dsList
	Variants map[string]*dsNode // dsUnion: widget type -> that widget's object
	isWidget bool               // a widget object (a union variant): its `type` is implied
}

// dsProp is an object key: its JSON spelling and the attribute name it gets.
type dsProp struct {
	HCL, JSON string
	Node      *dsNode
	Required  bool
	// KeepPrior: Atatus may rewrite the value on save (a query's bucket), so a
	// refresh keeps the configured value instead of reporting the rewrite.
	KeepPrior bool
	// Lowercase: Atatus stores the value lower-cased (a sort order); older boards
	// may still hold another case, which is read lower-cased.
	Lowercase bool
}

func (n *dsNode) prop(hcl string) *dsProp {
	for _, p := range n.Props {
		if p.HCL == hcl {
			return p
		}
	}
	return nil
}

// dashboardModel is the parsed schema: the board's own keys, the widget union,
// the tab shape, and each widget type's response format.
type dashboardModel struct {
	Version         string
	Board           *dsNode // title, description, ..., template_variables (not widgets, tabs)
	Widget          *dsNode // the widget union
	ResponseFormats map[string]string
	Types           []string
}

var (
	dashboardModelOnce sync.Once
	dashboardModelVal  *dashboardModel
	dashboardModelErr  error
)

func loadDashboardModel() (*dashboardModel, error) {
	dashboardModelOnce.Do(func() {
		dashboardModelVal, dashboardModelErr = parseDashboardSchema(dashboardSchemaJSON)
	})
	return dashboardModelVal, dashboardModelErr
}

var (
	camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	nonIdent      = regexp.MustCompile(`[^a-z0-9_]+`)
)

// snakeName is the attribute name for a JSON key: yBuckets -> y_buckets.
func snakeName(key string) string {
	s := strings.ToLower(camelBoundary.ReplaceAllString(key, "${1}_${2}"))
	return strings.Trim(nonIdent.ReplaceAllString(s, "_"), "_")
}

type schemaParser struct {
	defs  map[string]map[string]any
	stack map[string]bool
}

func parseDashboardSchema(raw []byte) (*dashboardModel, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("dashboard schema: %w", err)
	}
	p := &schemaParser{defs: map[string]map[string]any{}, stack: map[string]bool{}}
	defs, _ := root["$defs"].(map[string]any)
	for k, v := range defs {
		if m, ok := v.(map[string]any); ok {
			p.defs[k] = m
		}
	}
	m := &dashboardModel{ResponseFormats: map[string]string{}}
	m.Version, _ = root["x-schema-version"].(string)
	for name, def := range p.defs {
		if t, ok := strings.CutPrefix(name, "widget_"); ok {
			m.Types = append(m.Types, t)
			if rf, ok := def["x-response-format"].(string); ok {
				m.ResponseFormats[t] = rf
			}
		}
	}
	sort.Strings(m.Types)
	if len(m.Types) == 0 {
		return nil, fmt.Errorf("dashboard schema: no widget types")
	}

	board, err := p.node(root, "dashboard")
	if err != nil {
		return nil, err
	}
	kept := board.Props[:0]
	for _, prop := range board.Props {
		switch prop.JSON {
		case "widgets":
			m.Widget = prop.Node.Items
		case "tabs":
		default:
			kept = append(kept, prop)
		}
	}
	board.Props = kept
	m.Board = board
	if m.Widget == nil || m.Widget.Kind != dsUnion {
		return nil, fmt.Errorf("dashboard schema: widgets is not a widget list")
	}
	return m, nil
}

func (p *schemaParser) node(s map[string]any, at string) (*dsNode, error) {
	desc, _ := s["description"].(string)
	// {$ref}, {anyOf: [X, {type: null}]}, {allOf: [X], description}
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		def, ok := p.defs[name]
		if !ok {
			return nil, fmt.Errorf("dashboard schema: %s: unknown $ref %s", at, ref)
		}
		if p.stack[name] {
			return nil, fmt.Errorf("dashboard schema: %s: recursive $ref %s", at, ref)
		}
		p.stack[name] = true
		n, err := p.node(def, name)
		delete(p.stack, name)
		if err == nil && desc != "" {
			cp := *n
			cp.Desc = desc
			n = &cp
		}
		return n, err
	}
	for _, key := range []string{"anyOf", "allOf"} {
		if list, ok := s[key].([]any); ok {
			for _, alt := range list {
				am, _ := alt.(map[string]any)
				if am["type"] == "null" {
					continue
				}
				n, err := p.node(am, at)
				if err == nil && desc != "" {
					cp := *n
					cp.Desc = desc
					n = &cp
				}
				return n, err
			}
		}
	}
	if list, ok := s["oneOf"].([]any); ok {
		return p.union(list, at)
	}

	n := &dsNode{Desc: desc, MaxItems: -1}
	if e, ok := s["enum"].([]any); ok {
		for _, v := range e {
			if v != nil {
				n.Enum = append(n.Enum, v)
			}
		}
	}
	if v, ok := s["minimum"].(float64); ok {
		n.Min = &v
	}
	if v, ok := s["maximum"].(float64); ok {
		n.Max = &v
	}
	if v, ok := s["maxItems"].(float64); ok {
		n.MaxItems = int(v)
	}
	n.Pattern, _ = s["pattern"].(string)

	typ := s["type"]
	if _, hasProps := s["properties"]; typ == nil && hasProps {
		typ = "object" // the board and widget_<type> definitions omit it
	}
	if list, ok := typ.([]any); ok { // ["string", "null"]
		typ = nil
		for _, t := range list {
			if t != "null" {
				typ = t
			}
		}
	}
	switch typ {
	case "string":
		n.Kind = dsString
	case "number":
		n.Kind = dsNumber
	case "integer":
		n.Kind = dsInt
	case "boolean":
		n.Kind = dsBool
	case "array":
		if n.MaxItems == 0 {
			n.Kind = dsList // an always-empty list; the object parse leaves it out
			n.Items = &dsNode{Kind: dsJSON, MaxItems: -1}
			return n, nil
		}
		items, _ := s["items"].(map[string]any)
		if items == nil {
			n.Kind = dsJSON
			return n, nil
		}
		it, err := p.node(items, at+"[]")
		if err != nil {
			return nil, err
		}
		n.Kind, n.Items = dsList, it
	case "object":
		props, _ := s["properties"].(map[string]any)
		if len(props) == 0 {
			n.Kind = dsJSON // an open object with no named keys
			return n, nil
		}
		n.Kind = dsObject
		required := map[string]bool{}
		if rl, ok := s["required"].([]any); ok {
			for _, r := range rl {
				if rs, ok := r.(string); ok {
					required[rs] = true
				}
			}
		}
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		names := map[string]string{}
		for _, k := range keys {
			pm, _ := props[k].(map[string]any)
			child, err := p.node(pm, at+"."+k)
			if err != nil {
				return nil, err
			}
			// An always-empty list (`requests` of a note) is not configurable.
			if child.Kind == dsList && child.MaxItems == 0 {
				continue
			}
			// Atatus lower-cases a sort order on save; only the stored spelling is accepted.
			lowercase := k == "order" && strings.HasSuffix(at, "orderBy[]")
			if lowercase {
				child.Enum = []any{"asc", "desc"}
			}
			hcl := snakeName(k)
			if child.Kind == dsJSON || (child.Kind == dsList && child.Items.Kind == dsJSON) {
				hcl += "_json"
			}
			if other, taken := names[hcl]; taken {
				return nil, fmt.Errorf("dashboard schema: %s: keys %q and %q both map to %q", at, other, k, hcl)
			}
			names[hcl] = k
			n.Props = append(n.Props, &dsProp{HCL: hcl, JSON: k, Node: child, Required: required[k],
				KeepPrior: k == "bucket", Lowercase: lowercase})
		}
	default:
		n.Kind = dsJSON
	}
	return n, nil
}

// union reads {oneOf: [{$ref: widget_<type>}, ...]}: the widget object of each type,
// without its `type` (implied by the attribute set) and `id` (managed for tabs).
func (p *schemaParser) union(list []any, at string) (*dsNode, error) {
	u := &dsNode{Kind: dsUnion, Variants: map[string]*dsNode{}, MaxItems: -1}
	for _, alt := range list {
		am, _ := alt.(map[string]any)
		ref, _ := am["$ref"].(string)
		name := strings.TrimPrefix(ref, "#/$defs/")
		t, ok := strings.CutPrefix(name, "widget_")
		if !ok {
			return nil, fmt.Errorf("dashboard schema: %s: oneOf of %q, not of widgets", at, ref)
		}
		v, err := p.node(am, at+"<"+t+">")
		if err != nil {
			return nil, err
		}
		kept := v.Props[:0]
		for _, prop := range v.Props {
			if prop.JSON != "type" && prop.JSON != "id" {
				kept = append(kept, prop)
			}
		}
		v.Props = kept
		v.isWidget = true
		u.Variants[t] = v
	}
	return u, nil
}

func (u *dsNode) variantNames() []string {
	names := make([]string, 0, len(u.Variants))
	for t := range u.Variants {
		names = append(names, t)
	}
	sort.Strings(names)
	return names
}

// --- attribute types ---------------------------------------------------------

func (n *dsNode) attrType() attr.Type {
	switch n.Kind {
	case dsString, dsJSON:
		return types.StringType
	case dsNumber:
		return types.Float64Type
	case dsInt:
		return types.Int64Type
	case dsBool:
		return types.BoolType
	case dsList:
		return types.ListType{ElemType: n.Items.attrType()}
	case dsObject:
		return types.ObjectType{AttrTypes: n.objectAttrTypes()}
	case dsUnion:
		at := map[string]attr.Type{}
		for t, v := range n.Variants {
			at[t] = v.attrType()
		}
		return types.ObjectType{AttrTypes: at}
	}
	return types.StringType
}

func (n *dsNode) objectAttrTypes() map[string]attr.Type {
	at := make(map[string]attr.Type, len(n.Props))
	for _, p := range n.Props {
		at[p.HCL] = p.Node.attrType()
	}
	return at
}

// --- Terraform schema ---------------------------------------------------------

func (n *dsNode) describe(extra string) string {
	d := n.Desc
	if len(n.Enum) > 0 {
		vals := make([]string, len(n.Enum))
		for i, v := range n.Enum {
			vals[i] = fmt.Sprintf("`%v`", v)
		}
		d = strings.TrimSpace(d + " One of " + strings.Join(vals, ", ") + ".")
	}
	if n.Kind == dsJSON {
		d = strings.TrimSpace(d + " A JSON value (jsonencode).")
	}
	return strings.TrimSpace(d + " " + extra)
}

func stringEnum(n *dsNode) []string {
	var out []string
	for _, v := range n.Enum {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (n *dsNode) stringValidators() []validator.String {
	var vs []validator.String
	if n.Kind == dsJSON {
		return []validator.String{jsonStringValidator{}}
	}
	if e := stringEnum(n); len(e) > 0 {
		vs = append(vs, stringvalidator.OneOf(e...))
	}
	if n.Pattern != "" {
		if re, err := regexp.Compile(n.Pattern); err == nil {
			vs = append(vs, stringvalidator.RegexMatches(re, "must match "+n.Pattern))
		}
	}
	return vs
}

func (n *dsNode) float64Validators() []validator.Float64 {
	var vs []validator.Float64
	if len(n.Enum) > 0 {
		var e []float64
		for _, v := range n.Enum {
			if f, ok := v.(float64); ok {
				e = append(e, f)
			}
		}
		vs = append(vs, float64validator.OneOf(e...))
	}
	if n.Min != nil || n.Max != nil {
		lo, hi := -math.MaxFloat64, math.MaxFloat64
		if n.Min != nil {
			lo = *n.Min
		}
		if n.Max != nil {
			hi = *n.Max
		}
		vs = append(vs, float64validator.Between(lo, hi))
	}
	return vs
}

func (n *dsNode) int64Validators() []validator.Int64 {
	var vs []validator.Int64
	if len(n.Enum) > 0 {
		var e []int64
		for _, v := range n.Enum {
			if f, ok := v.(float64); ok {
				e = append(e, int64(f))
			}
		}
		vs = append(vs, int64validator.OneOf(e...))
	}
	if n.Min != nil || n.Max != nil {
		lo, hi := int64(math.MinInt64), int64(math.MaxInt64)
		if n.Min != nil {
			lo = int64(*n.Min)
		}
		if n.Max != nil {
			hi = int64(*n.Max)
		}
		vs = append(vs, int64validator.Between(lo, hi))
	}
	return vs
}

func (n *dsNode) listValidators() []validator.List {
	if n.MaxItems > 0 {
		return []validator.List{listvalidator.SizeAtMost(n.MaxItems)}
	}
	return nil
}

// attribute is the Terraform attribute for a value; required applies inside
// an object only (the JSON Schema `required`).
func (n *dsNode) attribute(required bool) schema.Attribute {
	opt := !required
	switch n.Kind {
	case dsString, dsJSON:
		return schema.StringAttribute{Required: required, Optional: opt, Description: n.describe(""), Validators: n.stringValidators()}
	case dsNumber:
		return schema.Float64Attribute{Required: required, Optional: opt, Description: n.describe(""), Validators: n.float64Validators()}
	case dsInt:
		return schema.Int64Attribute{Required: required, Optional: opt, Description: n.describe(""), Validators: n.int64Validators()}
	case dsBool:
		return schema.BoolAttribute{Required: required, Optional: opt, Description: n.describe("")}
	case dsList:
		switch n.Items.Kind {
		case dsObject:
			return schema.ListNestedAttribute{Required: required, Optional: opt, Description: n.describe(""),
				NestedObject: schema.NestedAttributeObject{Attributes: n.Items.objectAttributes()}, Validators: n.listValidators()}
		case dsUnion:
			return schema.ListNestedAttribute{Required: required, Optional: opt, Description: n.describe(""),
				NestedObject: schema.NestedAttributeObject{Attributes: n.Items.unionAttributes(),
					Validators: []validator.Object{exactlyOneWidgetType{}}}, Validators: n.listValidators()}
		default:
			return schema.ListAttribute{Required: required, Optional: opt, Description: n.describe(""),
				ElementType: n.Items.attrType(), Validators: n.listValidators()}
		}
	case dsObject:
		return schema.SingleNestedAttribute{Required: required, Optional: opt, Description: n.describe(""), Attributes: n.objectAttributes()}
	}
	return schema.StringAttribute{Optional: true}
}

func (n *dsNode) objectAttributes() map[string]schema.Attribute {
	out := make(map[string]schema.Attribute, len(n.Props))
	for _, p := range n.Props {
		out[p.HCL] = p.Node.attribute(p.Required)
	}
	return out
}

func (n *dsNode) unionAttributes() map[string]schema.Attribute {
	out := make(map[string]schema.Attribute, len(n.Variants))
	for t, v := range n.Variants {
		a := v.attribute(false).(schema.SingleNestedAttribute)
		if a.Description == "" {
			a.Description = "A " + t + " widget."
		}
		out[t] = a
	}
	return out
}
