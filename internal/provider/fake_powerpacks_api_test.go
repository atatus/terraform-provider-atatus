package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
)

// fakePowerpacks is an in-memory stand-in for /v2/powerpacks (atatus-server
// controllers/public-api/powerpacks.js and lookups.js over
// dashboard-v2/powerpacks.js). Like the server:
//   - create answers the stored pack: version 1, revision 0, every child given
//     a definition.powerpackKey (its index when it has none), layoutType
//     "ordered", a variable's empty prefix read as its name;
//   - PATCH needs expectedRevision and answers 409 when it is not the stored
//     revision; it bumps revision always and version only when the group or
//     the variables change, and answers {powerpack, synced};
//   - a catalog (non-hex) id is a 403 on write; a deleted pack is a 404;
//   - DELETE soft-deletes (204).
//
// It is seeded with the lookup fixtures (fakeLookupPowerpacks).
type fakePowerpacks struct {
	mu    sync.Mutex
	packs map[string]map[string]any // custom packs by id
	order []string                  // custom ids, oldest first
	seq   int
	// concurrentEditOnNextPatch makes the next PATCH see a change made in the
	// UI after Terraform's refresh.
	concurrentEditOnNextPatch bool
	// skippedOnSync is answered as synced.skipped by a PATCH that changes the layout.
	skippedOnSync []map[string]any
}

func newFakePowerpacks() *fakePowerpacks {
	p := &fakePowerpacks{packs: map[string]map[string]any{}}
	for _, fixture := range fakeLookupPowerpacks {
		if fixture["source"] != "custom" {
			continue
		}
		pack := fakeJSONCopy(fixture)
		pack["revision"] = float64(0)
		pack["editors"] = []any{}
		pack["templateVariables"] = []any{}
		pack["groupWidget"] = map[string]any{"title": pack["name"], "showTitle": true, "layoutType": "ordered", "definition": map[string]any{}, "widgets": []any{}}
		p.packs[pack["id"].(string)] = pack
		p.order = append(p.order, pack["id"].(string))
	}
	return p
}

func (p *fakePowerpacks) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v2/powerpacks", p.list)
	mux.HandleFunc("GET /v2/powerpacks/{id}", p.get)
	mux.HandleFunc("POST /v2/powerpacks", p.create)
	mux.HandleFunc("PATCH /v2/powerpacks/{id}", p.patch)
	mux.HandleFunc("DELETE /v2/powerpacks/{id}", p.delete)
}

// fakeJSONCopy deep-copies a JSON-shaped value.
func fakeJSONCopy(m map[string]any) map[string]any {
	raw, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func fakePowerpackPresets() []map[string]any {
	var out []map[string]any
	for _, fixture := range fakeLookupPowerpacks {
		if fixture["source"] != "custom" {
			out = append(out, fixture)
		}
	}
	return out
}

func (p *fakePowerpacks) list(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	p.mu.Lock()
	var rows []map[string]any
	for i := len(p.order) - 1; i >= 0; i-- { // newest first, like the server
		pack := p.packs[p.order[i]]
		if pack["deleted"] != true {
			rows = append(rows, pack)
		}
	}
	p.mu.Unlock()
	rows = append(rows, fakePowerpackPresets()...)
	var out []map[string]any
	for _, row := range rows {
		if q == "" || strings.Contains(strings.ToLower(row["name"].(string)), q) || strings.Contains(strings.ToLower(row["description"].(string)), q) {
			out = append(out, row)
		}
	}
	fakePaginate(w, r, out)
}

func (p *fakePowerpacks) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !fakeObjectID.MatchString(id) {
		for _, preset := range fakePowerpackPresets() {
			if preset["id"] == id {
				writeFakeJSON(w, http.StatusOK, preset)
				return
			}
		}
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Powerpack not found"})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pack, ok := p.packs[id]
	if !ok || pack["deleted"] == true {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Powerpack not found"})
		return
	}
	writeFakeJSON(w, http.StatusOK, pack)
}

// fakeNormalizeGroup is the group as the server stores it.
func fakeNormalizeGroup(raw any, name string) (map[string]any, error) {
	gw, _ := raw.(map[string]any)
	widgets, _ := gw["widgets"].([]any)
	if len(widgets) == 0 {
		return nil, fmt.Errorf("A powerpack needs at least one widget!")
	}
	out := map[string]any{"title": name, "showTitle": true, "layoutType": "ordered", "definition": map[string]any{}}
	if t, ok := gw["title"].(string); ok && t != "" {
		out["title"] = t
	}
	if s, ok := gw["showTitle"].(bool); ok {
		out["showTitle"] = s
	}
	if d, ok := gw["definition"].(map[string]any); ok {
		out["definition"] = d
	}
	children := []any{}
	for i, w := range widgets {
		child := fakeJSONCopy(w.(map[string]any))
		def, _ := child["definition"].(map[string]any)
		if def == nil {
			def = map[string]any{}
		}
		if key, _ := def["powerpackKey"].(string); key == "" {
			def["powerpackKey"] = fmt.Sprint(i)
		}
		child["definition"] = def
		children = append(children, child)
	}
	out["widgets"] = children
	return out, nil
}

func fakeNormalizeVariables(raw any) []any {
	list, _ := raw.([]any)
	out := []any{}
	for _, v := range list {
		vm, _ := v.(map[string]any)
		name, _ := vm["name"].(string)
		prefix, _ := vm["prefix"].(string)
		if prefix == "" {
			prefix = name
		}
		def, _ := vm["default"].([]any)
		avail, _ := vm["availableValues"].([]any)
		if def == nil {
			def = []any{}
		}
		if avail == nil {
			avail = []any{}
		}
		out = append(out, map[string]any{"name": name, "prefix": prefix, "default": def, "availableValues": avail})
	}
	return out
}

func fakeStringsOrEmpty(raw any) []any {
	if list, ok := raw.([]any); ok {
		return list
	}
	return []any{}
}

func (p *fakePowerpacks) create(w http.ResponseWriter, r *http.Request) {
	body, err := fakeDecodeBody(r)
	name, _ := body["name"].(string)
	if err == nil && strings.TrimSpace(name) == "" {
		err = fmt.Errorf("Powerpack name is required!")
	}
	var group map[string]any
	if err == nil {
		group, err = fakeNormalizeGroup(body["groupWidget"], strings.TrimSpace(name))
	}
	if err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	id := fmt.Sprintf("%024x", 0xc0ffee000+p.seq)
	description, _ := body["description"].(string)
	pack := map[string]any{
		"id": id, "source": "custom", "name": strings.TrimSpace(name), "displayName": strings.TrimSpace(name),
		"description": description, "tags": fakeStringsOrEmpty(body["tags"]), "editors": fakeStringsOrEmpty(body["editors"]),
		"templateVariables": fakeNormalizeVariables(body["templateVariables"]), "groupWidget": group,
		"version": float64(1), "revision": float64(0),
	}
	p.packs[id] = pack
	p.order = append(p.order, id)
	writeFakeJSON(w, http.StatusOK, pack)
}

func (p *fakePowerpacks) patch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !fakeObjectID.MatchString(id) {
		writeFakeJSON(w, http.StatusForbidden, map[string]any{"message": "Powerpacks created by Atatus cannot be changed!"})
		return
	}
	body, err := fakeDecodeBody(r)
	if err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pack, ok := p.packs[id]
	if !ok || pack["deleted"] == true {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Powerpack not found!"})
		return
	}
	if p.concurrentEditOnNextPatch {
		p.concurrentEditOnNextPatch = false
		pack["name"] = "Edited concurrently"
		pack["revision"] = pack["revision"].(float64) + 1
	}
	expected, has := body["expectedRevision"].(float64)
	if !has {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": "expectedRevision or expectedVersion is required!"})
		return
	}
	if expected != pack["revision"].(float64) {
		writeFakeJSON(w, http.StatusConflict, map[string]any{"message": "This powerpack was changed by someone else. Please reload it and try again."})
		return
	}
	name := pack["name"].(string)
	if n, ok := body["name"].(string); ok {
		name = strings.TrimSpace(n)
	}
	group := pack["groupWidget"].(map[string]any)
	if raw, ok := body["groupWidget"]; ok {
		if group, err = fakeNormalizeGroup(raw, name); err != nil {
			writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
			return
		}
	}
	variables := pack["templateVariables"]
	if raw, ok := body["templateVariables"]; ok {
		variables = fakeNormalizeVariables(raw)
	}
	changed := !reflect.DeepEqual(fakeJSONCopy(map[string]any{"g": group, "v": variables}),
		fakeJSONCopy(map[string]any{"g": pack["groupWidget"], "v": pack["templateVariables"]}))
	pack["name"], pack["displayName"] = name, name
	for _, k := range []string{"description", "tags", "editors"} {
		if v, ok := body[k]; ok {
			pack[k] = v
		}
	}
	pack["groupWidget"], pack["templateVariables"] = group, variables
	pack["revision"] = pack["revision"].(float64) + 1
	synced := map[string]any{"dashboards": []any{}, "skipped": []any{}}
	if changed {
		pack["version"] = pack["version"].(float64) + 1
		if len(p.skippedOnSync) > 0 {
			skipped := []any{}
			for _, s := range p.skippedOnSync {
				skipped = append(skipped, s)
			}
			synced["skipped"] = skipped
		}
	}
	writeFakeJSON(w, http.StatusOK, map[string]any{"powerpack": pack, "synced": synced})
}

func (p *fakePowerpacks) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !fakeObjectID.MatchString(id) {
		writeFakeJSON(w, http.StatusForbidden, map[string]any{"message": "Powerpacks created by Atatus cannot be changed!"})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pack, ok := p.packs[id]
	if !ok || pack["deleted"] == true {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Powerpack not found!"})
		return
	}
	pack["deleted"] = true
	w.WriteHeader(http.StatusNoContent)
}

// editOutOfBand changes a pack as the app would: revision always moves, the
// version too when layout is true.
func (p *fakePowerpacks) editOutOfBand(id string, layout bool, edit func(map[string]any)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	edit(p.packs[id])
	p.packs[id]["revision"] = p.packs[id]["revision"].(float64) + 1
	if layout {
		p.packs[id]["version"] = p.packs[id]["version"].(float64) + 1
	}
}

// live is the number of packs not deleted.
func (p *fakePowerpacks) live() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, pack := range p.packs {
		if pack["deleted"] != true {
			n++
		}
	}
	return n
}
