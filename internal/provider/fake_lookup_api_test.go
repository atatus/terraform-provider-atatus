package provider

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// The read-only lookups behind the data sources (atatus-server
// controllers/public-api/lookups.js), served from the fake's own boards plus
// fixed groups, powerpacks, Spaces, teams, users and projects. Lists answer
// the server's pagination shape and honour page/limit, so paging is exercised.

var fakeLookupGroups = []map[string]any{
	{"_id": "a00000000000000000000001", "name": "Platform", "description": "Platform boards", "dashboards": []any{"b00000000000000000000001", "b00000000000000000000002"}, "spaceIds": []any{"5p0000000000000000000001"}},
	{"_id": "a00000000000000000000002", "name": "Twin", "description": "", "dashboards": []any{}, "spaceIds": []any{}},
	{"_id": "a00000000000000000000003", "name": "Twin", "description": "", "dashboards": []any{}, "spaceIds": []any{}},
}

var fakeLookupPowerpacks = []map[string]any{
	{"id": "c00000000000000000000001", "source": "custom", "name": "Service health", "displayName": "Service health", "description": "RED metrics", "tags": []any{"apm"}, "version": float64(3)},
	{"id": "hosts-overview", "source": "preset", "name": "Hosts overview", "displayName": "Hosts overview", "description": "CPU, memory", "tags": []any{}, "version": float64(1)},
}

var fakeLookupSpaces = []map[string]any{
	{"_id": "5p0000000000000000000001", "name": "Payments", "description": "Payments team space"},
}

var fakeLookupTeams = []map[string]any{
	{"_id": "7e0000000000000000000001", "name": "SRE", "description": "On call"},
}

var fakeLookupUsers = []map[string]any{
	{"_id": "u00000000000000000000001", "firstName": "Ada", "lastName": "Lovelace", "email": "ada@example.com", "role": "owner"},
}

var fakeLookupProjects = []map[string]any{
	{"_id": "p00000000000000000000001", "name": "checkout", "type": "APM", "subType": "Node.js"},
	{"_id": "p00000000000000000000002", "name": "checkout", "type": "Browser", "subType": "React"},
}

func (f *fakeDashboardAPI) registerLookups(mux *http.ServeMux) {
	mux.HandleFunc("GET /v2/dashboards", f.listDashboards)
	f.groups.register(mux)
	f.packs.register(mux)
	mux.HandleFunc("GET /v2/spaces", fakeListHandler(fakeLookupSpaces, "name"))
	mux.HandleFunc("GET /v2/teams", fakeListHandler(fakeLookupTeams, "name"))
	mux.HandleFunc("GET /v2/users", fakeUserList)
	mux.HandleFunc("GET /v2/projects", func(w http.ResponseWriter, r *http.Request) {
		writeFakeJSON(w, http.StatusOK, map[string]any{"data": fakeLookupProjects})
	})
}

func fakePaginate(w http.ResponseWriter, r *http.Request, rows []map[string]any) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	start := (page - 1) * limit
	end := start + limit
	if start > len(rows) {
		start = len(rows)
	}
	if end > len(rows) {
		end = len(rows)
	}
	data := rows[start:end]
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"data": data, "count": len(data), "page": page, "limit": limit,
		"totalCount": len(rows), "totalPages": (len(rows) + limit - 1) / limit,
	})
}

// fakeListHandler serves rows, exactly filtered by ?<field>= when given.
func fakeListHandler(rows []map[string]any, field string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := r.URL.Query().Get(field)
		var out []map[string]any
		for _, row := range rows {
			if want == "" || row[field] == want {
				out = append(out, row)
			}
		}
		fakePaginate(w, r, out)
	}
}

func fakeUserList(w http.ResponseWriter, r *http.Request) {
	want := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
	var out []map[string]any
	for _, u := range fakeLookupUsers {
		if want == "" || strings.ToLower(u["email"].(string)) == want {
			out = append(out, u)
		}
	}
	fakePaginate(w, r, out)
}

// GET /v2/dashboards: the fake's boards, exactly by ?title= or by a
// case-insensitive ?search= on title or description.
func (f *fakeDashboardAPI) listDashboards(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	title, search := r.URL.Query().Get("title"), strings.ToLower(r.URL.Query().Get("search"))
	var rows []map[string]any
	for _, b := range f.boards {
		t, _ := b["title"].(string)
		d, _ := b["description"].(string)
		if title != "" && t != title {
			continue
		}
		if title == "" && search != "" && !strings.Contains(strings.ToLower(t), search) && !strings.Contains(strings.ToLower(d), search) {
			continue
		}
		rows = append(rows, map[string]any{
			"_id": b["_id"], "title": t, "description": d, "dashboardType": b["dashboardType"],
			"layoutType": b["layoutType"], "spaceIds": b["spaceIds"], "isReadOnly": b["isReadOnly"], "url": b["url"],
		})
	}
	f.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i]["_id"].(string) < rows[j]["_id"].(string) })
	fakePaginate(w, r, rows)
}
