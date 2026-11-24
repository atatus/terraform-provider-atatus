package provider

import (
	"net/http"
	"regexp"
)

// The fake's /v2/dashboards/{id}/restriction (atatus-server
// controllers/public-api/dashboard-restriction.js over dashboard-v2/restriction.js).
// Like the server: the restriction lives on the board as restrictedTo; PUT
// replaces it (distinct entries; principalType team|user; 24-hex ids; relation
// editor only), DELETE empties it, and a missing board is a 404. The board's
// __v does not move.

var fakePrincipalID = regexp.MustCompile(`^[0-9a-f]{24}$`)

func (f *fakeDashboardAPI) registerRestrictions(mux *http.ServeMux) {
	mux.HandleFunc("GET /v2/dashboards/{id}/restriction", f.restriction)
	mux.HandleFunc("PUT /v2/dashboards/{id}/restriction", f.restriction)
	mux.HandleFunc("DELETE /v2/dashboards/{id}/restriction", f.restriction)
}

func (f *fakeDashboardAPI) restriction(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if r.Method == http.MethodPut {
		var err error
		if body, err = fakeDecodeBody(r); err != nil {
			writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
			return
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := r.PathValue("id")
	board, ok := f.boards[id]
	if !ok {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Dashboard not found"})
		return
	}
	switch r.Method {
	case http.MethodPut:
		raw, isList := body["restrictedTo"].([]any)
		if !isList {
			writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": "restrictedTo must be a list of {principalType, principalId}!"})
			return
		}
		seen := map[string]bool{}
		entries := []any{}
		for _, e := range raw {
			em, _ := e.(map[string]any)
			kind, _ := em["principalType"].(string)
			pid, _ := em["principalId"].(string)
			rel, hasRel := em["relation"]
			if (kind != "team" && kind != "user") || !fakePrincipalID.MatchString(pid) || (hasRel && rel != "editor") {
				writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": "Invalid restrictedTo entry!"})
				return
			}
			if seen[kind+pid] {
				continue
			}
			seen[kind+pid] = true
			entries = append(entries, map[string]any{"principalType": kind, "principalId": pid, "relation": "editor"})
		}
		board["restrictedTo"] = entries
	case http.MethodDelete:
		board["restrictedTo"] = []any{}
	}
	entries, _ := board["restrictedTo"].([]any)
	if entries == nil {
		entries = []any{}
	}
	writeFakeJSON(w, http.StatusOK, map[string]any{"dashboardId": id, "restrictedTo": entries})
}

// setRestrictionOutOfBand changes a board's restriction as the app would.
func (f *fakeDashboardAPI) setRestrictionOutOfBand(id string, entries []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.boards[id]["restrictedTo"] = entries
}
