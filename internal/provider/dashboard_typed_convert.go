package provider

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// HCL -> JSON: a value of the typed resource as the dashboard document key it
// stands for. A null attribute is an absent key.

func absent(v attr.Value) bool { return v == nil || v.IsNull() || v.IsUnknown() }

func (n *dsNode) toJSON(v attr.Value) (any, bool, error) {
	if absent(v) {
		return nil, false, nil
	}
	switch n.Kind {
	case dsString:
		return v.(basetypes.StringValue).ValueString(), true, nil
	case dsJSON:
		var out any
		if err := json.Unmarshal([]byte(v.(basetypes.StringValue).ValueString()), &out); err != nil {
			return nil, false, fmt.Errorf("not valid JSON: %w", err)
		}
		return out, true, nil
	case dsNumber:
		return v.(basetypes.Float64Value).ValueFloat64(), true, nil
	case dsInt:
		return v.(basetypes.Int64Value).ValueInt64(), true, nil
	case dsBool:
		return v.(basetypes.BoolValue).ValueBool(), true, nil
	case dsList:
		elems := v.(basetypes.ListValue).Elements()
		out := make([]any, len(elems))
		for i, e := range elems {
			val, _, err := n.Items.toJSON(e)
			if err != nil {
				return nil, false, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = val
		}
		return out, true, nil
	case dsObject:
		out, err := n.objectToJSON(v.(basetypes.ObjectValue).Attributes())
		return out, true, err
	case dsUnion:
		attrs := v.(basetypes.ObjectValue).Attributes()
		for _, t := range n.variantNames() {
			if absent(attrs[t]) {
				continue
			}
			out, err := n.Variants[t].objectToJSON(attrs[t].(basetypes.ObjectValue).Attributes())
			if err != nil {
				return nil, false, fmt.Errorf("%s: %w", t, err)
			}
			out["type"] = t
			return out, true, nil
		}
		return nil, false, fmt.Errorf("a widget sets none of the widget types")
	}
	return nil, false, nil
}

func (n *dsNode) objectToJSON(attrs map[string]attr.Value) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range n.Props {
		val, ok, err := p.Node.toJSON(attrs[p.HCL])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.HCL, err)
		}
		if ok {
			out[p.JSON] = val
		}
	}
	return out, nil
}

// JSON -> HCL. A refresh reads only what the configuration (the prior value)
// sets, so the keys Atatus fills in never show as a diff, while a changed or
// removed value does: fill=false. On import there is no configuration, and
// every key Atatus returns is read: fill=true. A list whose length changed is
// read in full, so an added or removed element shows.

type readCtx struct {
	dropped map[string]bool // keys Atatus returned that this resource cannot represent (import only)
}

func (rc *readCtx) drop(path string) {
	if rc != nil && rc.dropped != nil {
		rc.dropped[path] = true
	}
}

func nullOf(t attr.Type) attr.Value {
	switch tt := t.(type) {
	case basetypes.ListType:
		return types.ListNull(tt.ElemType)
	case basetypes.ObjectType:
		return types.ObjectNull(tt.AttrTypes)
	case basetypes.Float64Type:
		return types.Float64Null()
	case basetypes.Int64Type:
		return types.Int64Null()
	case basetypes.BoolType:
		return types.BoolNull()
	}
	return types.StringNull()
}

func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var round any
	if json.Unmarshal(b, &round) != nil {
		return string(b)
	}
	s, _ := marshalCanonical(round)
	return s
}

func (n *dsNode) fromJSON(j any, prior attr.Value, fill bool, path string, rc *readCtx) attr.Value {
	t := n.attrType()
	if !fill && absent(prior) {
		return nullOf(t)
	}
	if j == nil {
		return nullOf(t)
	}
	switch n.Kind {
	case dsString:
		switch x := j.(type) {
		case string:
			if fill && x == "" {
				return types.StringNull() // an empty setting is an unset one (and Terraform writes it so in generated configuration)
			}
			return types.StringValue(x)
		case float64:
			return types.StringValue(strconv.FormatFloat(x, 'f', -1, 64))
		case bool:
			return types.StringValue(strconv.FormatBool(x))
		}
	case dsJSON:
		s := canonicalJSON(j)
		if p, ok := prior.(basetypes.StringValue); ok && !absent(prior) {
			var pv any
			if json.Unmarshal([]byte(p.ValueString()), &pv) == nil && canonicalJSON(pv) == s {
				return prior // the same value as configured, in the configuration's spelling
			}
		}
		return types.StringValue(s)
	case dsNumber:
		if x, ok := j.(float64); ok {
			return types.Float64Value(x)
		}
	case dsInt:
		if x, ok := j.(float64); ok {
			return types.Int64Value(int64(math.Round(x)))
		}
	case dsBool:
		if x, ok := j.(bool); ok {
			return types.BoolValue(x)
		}
	case dsList:
		arr, ok := j.([]any)
		if !ok {
			break
		}
		if fill && len(arr) == 0 {
			return nullOf(t) // an empty list Atatus stores by default
		}
		var priorElems []attr.Value
		if pl, ok := prior.(basetypes.ListValue); ok && !absent(prior) {
			priorElems = pl.Elements()
		}
		aligned := !fill && len(priorElems) == len(arr)
		elems := make([]attr.Value, len(arr))
		for i, e := range arr {
			var pe attr.Value
			if aligned {
				pe = priorElems[i]
			}
			elems[i] = n.Items.fromJSON(e, pe, !aligned, fmt.Sprintf("%s[%d]", path, i), rc)
			// A null element of a list of objects (formulaOptions keeps them for formulas with no
			// options) is read as an empty object: Terraform cannot write a null list element as HCL.
			if elems[i].IsNull() && n.Items.Kind == dsObject {
				elems[i] = types.ObjectValueMust(n.Items.objectAttrTypes(), nullAttrs(n.Items))
			}
		}
		return types.ListValueMust(n.Items.attrType(), elems)
	case dsObject:
		m, ok := j.(map[string]any)
		if !ok {
			break
		}
		var priorAttrs map[string]attr.Value
		if po, ok := prior.(basetypes.ObjectValue); ok && !absent(prior) {
			priorAttrs = po.Attributes()
		}
		return n.objectFromJSON(m, priorAttrs, fill, path, rc)
	case dsUnion:
		m, ok := j.(map[string]any)
		if !ok {
			break
		}
		typ, _ := m["type"].(string)
		variant, known := n.Variants[typ]
		if !known {
			rc.drop(path + " (widget type " + typ + ")")
			return nullOf(t)
		}
		var priorVariant attr.Value
		if po, ok := prior.(basetypes.ObjectValue); ok && !absent(prior) {
			priorVariant = po.Attributes()[typ]
		}
		attrs := map[string]attr.Value{}
		for name, v := range n.Variants {
			attrs[name] = nullOf(v.attrType())
		}
		var priorAttrs map[string]attr.Value
		if po, ok := priorVariant.(basetypes.ObjectValue); ok && !absent(priorVariant) {
			priorAttrs = po.Attributes()
		}
		// A widget whose type changed in Atatus is read in full.
		attrs[typ] = variant.objectFromJSON(m, priorAttrs, fill || priorAttrs == nil, path+"."+typ, rc)
		if absent(attrs[typ]) {
			attrs[typ] = types.ObjectValueMust(variant.objectAttrTypes(), nullAttrs(variant))
		}
		return types.ObjectValueMust(t.(basetypes.ObjectType).AttrTypes, attrs)
	}
	return nullOf(t)
}

// defaultish: a value Atatus stores by default and that carries nothing ("",
// false, [], {}, a request's default `query: {dataSource: "metrics"}`), so
// leaving it out of the resource loses nothing.
func defaultish(key string, v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == "" || (key == "dataSource" && x == "metrics")
	case bool:
		return !x
	case []any:
		for _, e := range x {
			if !defaultish("", e) {
				return false
			}
		}
		return true
	case map[string]any:
		for k, e := range x {
			if !defaultish(k, e) {
				return false
			}
		}
		return true
	}
	return false
}

func nullAttrs(n *dsNode) map[string]attr.Value {
	out := make(map[string]attr.Value, len(n.Props))
	for _, p := range n.Props {
		out[p.HCL] = nullOf(p.Node.attrType())
	}
	return out
}

func (n *dsNode) objectFromJSON(m map[string]any, priorAttrs map[string]attr.Value, fill bool, path string, rc *readCtx) attr.Value {
	attrs := make(map[string]attr.Value, len(n.Props))
	known := map[string]bool{}
	if n.isWidget {
		known["type"] = true // the widget's type is the union attribute that holds it
	}
	set := 0
	for _, p := range n.Props {
		known[p.JSON] = true
		var pv attr.Value
		if priorAttrs != nil {
			pv = priorAttrs[p.HCL]
		}
		var v attr.Value
		switch {
		case !fill && absent(pv):
			v = nullOf(p.Node.attrType())
		case !fill && p.KeepPrior:
			v = pv // Atatus rewrites it on save (a query bucket); what was configured stands
		default:
			raw := m[p.JSON]
			if s, ok := raw.(string); ok && p.Lowercase {
				raw = strings.ToLower(s)
			}
			v = p.Node.fromJSON(raw, pv, fill, path+"."+p.HCL, rc)
		}
		if !v.IsNull() {
			set++
		}
		attrs[p.HCL] = v
	}
	if fill {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			// "_"-prefixed keys are Atatus's own markers (the AI's _aiSection), not settings; a
			// definition's title / type repeat the widget's own (a create moves them onto it).
			if !known[k] && k != "id" && !strings.HasPrefix(k, "_") && !defaultish(k, m[k]) &&
				!(strings.HasSuffix(path, ".definition") && (k == "title" || k == "type")) {
				rc.drop(path + "." + k)
			}
		}
		if set == 0 {
			return types.ObjectNull(n.objectAttrTypes()) // nothing this resource represents
		}
	}
	return types.ObjectValueMust(n.objectAttrTypes(), attrs)
}
