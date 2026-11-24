package provider

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// fakeChecks is an in-memory stand-in for the public synthetic checks and
// notification channel API (atatus-server api/lib/controllers/public-api/index.js
// → synthetics/checks.js, synthetics/helper.js). It follows the server where
// the provider depends on it:
//   - the body may only hold CHECK_BODY_FIELDS (anything else is a 400
//     "Unknown field(s)"), and name, type, subtype and a non-empty locations
//     list are required; retryCount may not exceed 3;
//   - create answers 200 (not 201) with the stored check, including _id;
//   - there is no PUT: PATCH is a partial $set that may not change type or
//     subtype and refuses an empty body;
//   - GET of a missing check is a 404; GET populates createdBy as an object;
//   - DELETE answers 204 whether or not the check exists;
//   - GET /v2/channels lists only _id, name and type (settings are stripped);
//   - every apiSteps[] entry gets an _id when it has none (the Check model's
//     apiSteps subschema, unlike assertions, does not set _id: false).
type fakeChecks struct {
	mu       sync.Mutex
	checks   map[string]map[string]any
	seq      int
	channels []map[string]any
	// channelsDown makes GET /v2/channels fail, to exercise Read's warning.
	channelsDown bool
}

// The fake account's channels: one email channel per address the tests use,
// and one non-email channel.
func newFakeChecks() *fakeChecks {
	return &fakeChecks{
		checks: map[string]map[string]any{},
		channels: []map[string]any{
			{"_id": "ch-email-devops", "name": "devops@example.com", "type": "EMAIL"},
			{"_id": "ch-email-oncall", "name": "oncall@example.com", "type": "EMAIL"},
			{"_id": "ch-slack-alerts", "name": "#alerts", "type": "SLACK"},
		},
	}
}

var fakeCheckBodyFields = map[string]bool{
	"name": true, "type": true, "subtype": true, "status": true, "locations": true, "frequency": true,
	"retryCount": true, "retryInterval": true, "tags": true, "runType": true, "host": true, "server": true,
	"port": true, "icmpSendPackets": true, "request": true, "apiSteps": true, "browserSteps": true,
	"browserVariables": true, "browserScript": true, "deviceTypes": true, "assertions": true,
	"minFailureDuration": true, "minLocationFailed": true, "channelIds": true, "teamIds": true,
	"api_key": true, "silent": true,
}

func (c *fakeChecks) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v2/channels", c.listChannels)
	mux.HandleFunc("POST /v2/synthetic/checks", c.create)
	mux.HandleFunc("GET /v2/synthetic/checks/{id}", c.get)
	mux.HandleFunc("PATCH /v2/synthetic/checks/{id}", c.patch)
	mux.HandleFunc("DELETE /v2/synthetic/checks/{id}", c.delete)
}

func (c *fakeChecks) listChannels(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channelsDown {
		writeFakeJSON(w, http.StatusInternalServerError, map[string]any{"message": "Oops, something breaks in our end. Try again!"})
		return
	}
	writeFakeJSON(w, http.StatusOK, c.channels)
}

func fakeUnknownCheckFields(body map[string]any) error {
	var unknown []string
	for k := range body {
		if !fakeCheckBodyFields[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("Unknown field(s): %s.", strings.Join(unknown, ", "))
	}
	return nil
}

func fakeValidateCheck(body map[string]any, creating bool) error {
	if err := fakeUnknownCheckFields(body); err != nil {
		return err
	}
	if creating {
		for _, k := range []string{"name", "type", "subtype"} {
			if s, _ := body[k].(string); s == "" {
				return fmt.Errorf("%s is missing!", k)
			}
		}
	}
	if loc, ok := body["locations"]; ok || creating {
		if list, _ := loc.([]any); len(list) == 0 {
			return fmt.Errorf("Locations are missing!")
		}
	}
	if n, ok := body["retryCount"].(float64); ok && n > 3 {
		return fmt.Errorf("Retry count should be less than or equal to 3!")
	}
	return nil
}

func (c *fakeChecks) create(w http.ResponseWriter, r *http.Request) {
	body, err := fakeDecodeBody(r)
	if err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	if err := fakeValidateCheck(body, true); err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	id := fmt.Sprintf("%024x", 0xc0ffee000+c.seq)
	check := map[string]any{}
	for k, v := range body {
		if k != "api_key" && k != "silent" {
			check[k] = v
		}
	}
	c.mintStepIDs(check)
	check["_id"] = id
	check["__v"] = float64(0)
	check["accountId"] = "fake-account"
	check["createdBy"] = "fake-user"
	c.checks[id] = check
	writeFakeJSON(w, http.StatusOK, check)
}

func (c *fakeChecks) get(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	check, ok := c.checks[r.PathValue("id")]
	if !ok {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Check is missing!"})
		return
	}
	out := map[string]any{}
	for k, v := range check {
		out[k] = v
	}
	out["createdBy"] = map[string]any{"_id": "fake-user", "firstName": "Fake", "lastName": "User", "email": "fake@example.com"}
	out["resultStatus"] = "pass"
	writeFakeJSON(w, http.StatusOK, out)
}

func (c *fakeChecks) patch(w http.ResponseWriter, r *http.Request) {
	body, err := fakeDecodeBody(r)
	if err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	check, ok := c.checks[r.PathValue("id")]
	if !ok {
		writeFakeJSON(w, http.StatusNotFound, map[string]any{"message": "Check is missing!"})
		return
	}
	for _, k := range []string{"type", "subtype"} {
		if v, ok := body[k]; ok && v != check[k] {
			writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": "Check type can't be changed!"})
			return
		}
	}
	delete(body, "api_key")
	delete(body, "silent")
	if len(body) == 0 {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": "No fields to update!"})
		return
	}
	if err := fakeValidateCheck(body, false); err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	for k, v := range body {
		check[k] = v
	}
	c.mintStepIDs(check)
	check["__v"] = check["__v"].(float64) + 1
	writeFakeJSON(w, http.StatusOK, check)
}

// mintStepIDs gives apiSteps entries without an _id a new one, as Mongoose
// does for subdocuments. Callers hold c.mu.
func (c *fakeChecks) mintStepIDs(check map[string]any) {
	steps, _ := check["apiSteps"].([]any)
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := step["_id"].(string); id == "" {
			c.seq++
			step["_id"] = fmt.Sprintf("%024x", 0x57e9000+c.seq)
		}
	}
}

func (c *fakeChecks) delete(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.checks, r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (c *fakeChecks) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.checks)
}

func (c *fakeChecks) setChannelsDown(down bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channelsDown = down
}
