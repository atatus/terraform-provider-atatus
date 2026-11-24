package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// fakeGroups is an in-memory stand-in for /v2/dashboard-groups (atatus-server
// controllers/public-api/dashboard-groups.js and lookups.js over
// dashboard-v2/dashboard-groups.js). Like the server:
//   - create answers 200 with the stored group, __v 0;
//   - a dashboards list naming one id twice, or a malformed id, is a 400;
//   - PUT sets only the fields the body names and bumps __v; with
//     expectedVersion it compares-and-sets, answering 409 on a mismatch;
//   - GET of one group populates dashboards as {_id, title} objects, the
//     list answers them as ids; a missing group is a 404;
//   - DELETE answers 200 {} (a missing group is a 404).
//
// It is seeded with the lookup fixtures (fakeLookupGroups).
type fakeGroups struct {
	mu     sync.Mutex
	groups map[string]map[string]any
	seq    int
	// concurrentEditOnNextPut makes the next PUT see a change made in the UI
	// after Terraform's refresh.
	concurrentEditOnNextPut bool
	// deleteOnNextPut makes the next PUT see the group deleted in the UI after
	// Terraform's refresh.
	deleteOnNextPut bool
}

var fakeObjectID = regexp.MustCompile(`^[0-9a-f]{24}$`)

func newFakeGroups() *fakeGroups {
	g := &fakeGroups{groups: map[string]map[string]any{}}
	for _, fixture := range fakeLookupGroups {
		copyOf := map[string]any{"__v": float64(0), "order": float64(0)}
		for k, v := range fixture {
			copyOf[k] = v
		}
		g.groups[fixture["_id"].(string)] = copyOf
	}
	return g
}

func (g *fakeGroups) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v2/dashboard-groups", g.list)
	mux.HandleFunc("GET /v2/dashboard-groups/{id}", g.get)
	mux.HandleFunc("POST /v2/dashboard-groups", g.create)
	mux.HandleFunc("PUT /v2/dashboard-groups/{id}", g.put)
	mux.HandleFunc("DELETE /v2/dashboard-groups/{id}", g.delete)
}

func fakeGroupDashboardsError(raw any) error {
	if raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("Dashboards must be an array!")
	}
	seen := map[string]bool{}
	for _, v := range list {
		id, _ := v.(string)
		if !fakeObjectID.MatchString(id) {
			return fmt.Errorf("Not a dashboard id: %v", v)
		}
		if seen[id] {
			return fmt.Errorf("Dashboard %s is listed more than once!", id)
		}
		seen[id] = true
	}
	return nil
}

func (g *fakeGroups) list(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	name := r.URL.Query().Get("name")
	var rows []map[string]any
	for _, grp := range g.groups {
		if name == "" || grp["name"] == name {
			rows = append(rows, grp)
		}
	}
	g.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i]["_id"].(string) < rows[j]["_id"].(string) })
	fakePaginate(w, r, rows)
}

func (g *fakeGroups) get(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	grp, ok := g.groups[r.PathValue("id")]
	if !ok {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Dashboard group not found"})
		return
	}
	out := map[string]any{}
	for k, v := range grp {
		out[k] = v
	}
	populated := []any{}
	for _, id := range grp["dashboards"].([]any) {
		populated = append(populated, map[string]any{"_id": id, "title": "Board " + strings.ToUpper(id.(string)[20:])})
	}
	out["dashboards"] = populated
	writeFakeJSON(w, http.StatusOK, out)
}

func (g *fakeGroups) create(w http.ResponseWriter, r *http.Request) {
	body, err := fakeDecodeBody(r)
	if err == nil {
		err = fakeGroupDashboardsError(body["dashboards"])
	}
	if err == nil {
		if name, _ := body["name"].(string); strings.TrimSpace(name) == "" {
			err = fmt.Errorf("Group name is required!")
		}
	}
	if err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	id := fmt.Sprintf("%024x", 0x9709000+g.seq)
	grp := map[string]any{
		"_id": id, "__v": float64(0), "name": strings.TrimSpace(body["name"].(string)),
		"description": "", "order": float64(0), "dashboards": []any{}, "spaceIds": []any{},
	}
	for _, k := range []string{"description", "order", "dashboards", "spaceIds"} {
		if v, ok := body[k]; ok && v != nil {
			grp[k] = v
		}
	}
	g.groups[id] = grp
	writeFakeJSON(w, http.StatusOK, grp)
}

func (g *fakeGroups) put(w http.ResponseWriter, r *http.Request) {
	body, err := fakeDecodeBody(r)
	if err == nil {
		err = fakeGroupDashboardsError(body["dashboards"])
	}
	if err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.deleteOnNextPut {
		g.deleteOnNextPut = false
		delete(g.groups, r.PathValue("id"))
	}
	grp, ok := g.groups[r.PathValue("id")]
	if !ok {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Dashboard group not found"})
		return
	}
	if g.concurrentEditOnNextPut {
		g.concurrentEditOnNextPut = false
		grp["name"] = "Edited concurrently"
		grp["__v"] = grp["__v"].(float64) + 1
	}
	if ev, has := body["expectedVersion"].(float64); has && ev != grp["__v"].(float64) {
		writeFakeJSON(w, http.StatusConflict, map[string]any{"message": "The dashboard group was changed by someone else. Read it again before updating."})
		return
	}
	for _, k := range []string{"name", "description", "order", "dashboards", "spaceIds"} {
		if v, ok := body[k]; ok {
			grp[k] = v
		}
	}
	grp["__v"] = grp["__v"].(float64) + 1
	writeFakeJSON(w, http.StatusOK, grp)
}

func (g *fakeGroups) delete(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.groups[r.PathValue("id")]; !ok {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Dashboard group not found"})
		return
	}
	delete(g.groups, r.PathValue("id"))
	writeFakeJSON(w, http.StatusOK, map[string]any{})
}

// editOutOfBand changes a group as the UI would (bumping __v).
func (g *fakeGroups) editOutOfBand(id string, edit func(map[string]any)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	edit(g.groups[id])
	g.groups[id]["__v"] = g.groups[id]["__v"].(float64) + 1
}

func (g *fakeGroups) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.groups)
}
