/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/service"
	gin_mw "github.com/SENERGY-Platform/gin-middleware"
	"github.com/gin-gonic/gin"
)

func TestOperatorVersionRoutesAreRegistered(t *testing.T) {
	engine := gin.New()
	if _, err := routesAuth.Set(service.Service{}, engine.Group("")); err != nil {
		t.Fatalf("set routes: %v", err)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		found := slices.ContainsFunc(engine.Routes(), func(r gin.RouteInfo) bool {
			return r.Method == method && r.Path == "/operator/:id/versions/:version"
		})
		if !found {
			t.Errorf("%s /operator/:id/versions/:version is not registered: %v", method, engine.Routes())
		}
	}
}

func TestStatusCodeOfAConflict(t *testing.T) {
	for _, err := range []error{lib.ErrConflict, fmt.Errorf("put: %w", lib.ErrConflict)} {
		if got := statusCode(err); got != http.StatusConflict {
			t.Errorf("statusCode(%v) = %d, want 409", err, got)
		}
		if got := safeError(err); got != err {
			t.Errorf("safeError replaced %v with %v", err, got)
		}
	}
}

type putCall struct {
	id      string
	version lib.OperatorVersion
	auth    string
}

func putVersionRequest(t *testing.T, put versionPutter, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	engine := gin.New()
	engine.Use(gin_mw.ErrorHandler(statusCode, ", "))
	engine.PUT("/operator/:id/versions/:version", putOperatorVersionHandler(put))
	req := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer a")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestPutOperatorVersionHandler(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		body       string
		result     func(*putCall) (bool, error)
		wantStatus int
		wantCalled bool
		wantLabel  string
	}{
		{
			name:       "a new label is created",
			target:     "/operator/op-1/versions/v1.0.0",
			body:       `{"image":"img:1"}`,
			result:     func(*putCall) (bool, error) { return true, nil },
			wantStatus: http.StatusCreated,
			wantCalled: true,
			wantLabel:  "v1.0.0",
		},
		{
			name:       "an existing label is replaced",
			target:     "/operator/op-1/versions/v1.0.0",
			body:       `{"image":"img:1","deprecated":true}`,
			result:     func(*putCall) (bool, error) { return false, nil },
			wantStatus: http.StatusOK,
			wantCalled: true,
			wantLabel:  "v1.0.0",
		},
		{
			name:       "the same label in the body is accepted",
			target:     "/operator/op-1/versions/v1.0.0",
			body:       `{"version":"v1.0.0","image":"img:1"}`,
			result:     func(*putCall) (bool, error) { return true, nil },
			wantStatus: http.StatusCreated,
			wantCalled: true,
			wantLabel:  "v1.0.0",
		},
		{
			name:       "another label in the body is refused before the service is asked",
			target:     "/operator/op-1/versions/v1.0.0",
			body:       `{"version":"v1.1.0","image":"img:1"}`,
			result:     func(*putCall) (bool, error) { return true, nil },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "a malformed body",
			target:     "/operator/op-1/versions/v1.0.0",
			body:       `{"image":`,
			result:     func(*putCall) (bool, error) { return true, nil },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:   "invalid input from the service",
			target: "/operator/op-1/versions/1.0.0",
			body:   `{"image":"img:1"}`,
			result: func(*putCall) (bool, error) {
				return false, fmt.Errorf("%w: version must look like v1.2.3", lib.ErrInvalidInput)
			},
			wantStatus: http.StatusBadRequest,
			wantCalled: true,
			wantLabel:  "1.0.0",
		},
		{
			name:       "missing rights",
			target:     "/operator/op-1/versions/v1.0.0",
			body:       `{"image":"img:1"}`,
			result:     func(*putCall) (bool, error) { return false, lib.ErrMissingRights },
			wantStatus: http.StatusForbidden,
			wantCalled: true,
			wantLabel:  "v1.0.0",
		},
		{
			name:       "operator gone",
			target:     "/operator/op-1/versions/v1.0.0",
			body:       `{"image":"img:1"}`,
			result:     func(*putCall) (bool, error) { return false, lib.ErrNotFound },
			wantStatus: http.StatusNotFound,
			wantCalled: true,
			wantLabel:  "v1.0.0",
		},
		{
			name:       "concurrent change",
			target:     "/operator/op-1/versions/v1.0.0",
			body:       `{"image":"img:1"}`,
			result:     func(*putCall) (bool, error) { return false, lib.ErrConflict },
			wantStatus: http.StatusConflict,
			wantCalled: true,
			wantLabel:  "v1.0.0",
		},
		{
			name:       "an internal error is redacted",
			target:     "/operator/op-1/versions/v1.0.0",
			body:       `{"image":"img:1"}`,
			result:     func(*putCall) (bool, error) { return false, errors.New(`mongo: auth error for user "operator-repo"`) },
			wantStatus: http.StatusInternalServerError,
			wantCalled: true,
			wantLabel:  "v1.0.0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var call *putCall
			put := func(_ context.Context, id string, v lib.OperatorVersion, auth string) (bool, error) {
				call = &putCall{id: id, version: v, auth: auth}
				return tc.result(call)
			}

			rec := putVersionRequest(t, put, tc.target, tc.body)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "mongo") {
				t.Errorf("body leaks internals: %q", rec.Body.String())
			}
			if tc.wantStatus < 300 && rec.Body.Len() != 0 {
				t.Errorf("body = %q, want none", rec.Body.String())
			}
			if (call != nil) != tc.wantCalled {
				t.Fatalf("service called = %v, want %v", call != nil, tc.wantCalled)
			}
			if call != nil {
				if call.id != "op-1" || call.auth != "Bearer a" || call.version.Version != tc.wantLabel || call.version.Image != "img:1" {
					t.Errorf("call = %+v, want op-1, the caller's token, label %q and image img:1", *call, tc.wantLabel)
				}
			}
		})
	}
}

type deleteVersionCall struct {
	id, label, auth string
	opts            service.DeleteOptions
}

func deleteVersionRequest(t *testing.T, del versionDeleter, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	engine := gin.New()
	engine.Use(gin_mw.ErrorHandler(statusCode, ", "))
	engine.DELETE("/operator/:id/versions/:version", func(gc *gin.Context) {
		gc.Set(UserIdKey, "user-a")
		deleteOperatorVersionHandler(del)(gc)
	})
	req := httptest.NewRequest(http.MethodDelete, target, nil)
	req.Header.Set("Authorization", "Bearer a")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestDeleteOperatorVersionHandler(t *testing.T) {
	admin := map[string]string{"X-User-Roles": "user, admin"}
	user := map[string]string{"X-User-Roles": "user"}
	inUse := &lib.InUseError{Version: "v1.0.0", Operators: []lib.UsedOperator{{
		Id:            "op-1",
		OperatorUsage: lib.OperatorUsage{Flows: 1, Readable: []lib.FlowRef{{Id: "f1", Name: "flow"}}},
		PipelineUsage: lib.PipelineUsage{Pipelines: 2, ReadablePipelines: []lib.PipelineRef{}},
	}}}
	tests := []struct {
		name       string
		target     string
		headers    map[string]string
		result     error
		wantStatus int
		wantCalled bool
		wantForce  bool
		wantBody   string
	}{
		{"deleted", "/operator/op-1/versions/v1.0.0", user, nil, http.StatusNoContent, true, false, ""},
		{"forced by an admin", "/operator/op-1/versions/v1.0.0?force=true", admin, nil, http.StatusNoContent, true, true, ""},
		{"force by a user is refused before the service is asked", "/operator/op-1/versions/v1.0.0?force=true", user, nil, http.StatusForbidden, false, false, ""},
		{"force that is no boolean", "/operator/op-1/versions/v1.0.0?force=maybe", admin, nil, http.StatusBadRequest, false, false, ""},
		{"in use", "/operator/op-1/versions/v1.0.0", user, inUse, http.StatusConflict, true, false,
			`{"error":"version is still used by flows or pipelines","flows":1,"readable":[{"id":"f1","name":"flow"}],"pipelines":2,"readable_pipelines":[]}`},
		{"malformed label", "/operator/op-1/versions/1.0.0", user, fmt.Errorf("%w: version must look like v1.2.3", lib.ErrInvalidInput), http.StatusBadRequest, true, false, ""},
		{"only version", "/operator/op-1/versions/v1.0.0", user, fmt.Errorf("%w: an operator keeps at least one version; delete the operator instead", lib.ErrInvalidInput), http.StatusBadRequest, true, false, "delete the operator instead"},
		{"missing rights", "/operator/op-1/versions/v1.0.0", user, lib.ErrMissingRights, http.StatusForbidden, true, false, ""},
		{"no such version", "/operator/op-1/versions/v1.0.0", user, lib.ErrVersionNotFound, http.StatusNotFound, true, false, ""},
		{"operator gone", "/operator/op-1/versions/v1.0.0", user, lib.ErrNotFound, http.StatusNotFound, true, false, ""},
		{"concurrent change", "/operator/op-1/versions/v1.0.0", user, lib.ErrConflict, http.StatusConflict, true, false, lib.ErrConflict.Error()},
		{"pipeline registry down", "/operator/op-1/versions/v1.0.0", user, lib.ErrPipelineRegistryUnavailable, http.StatusBadGateway, true, false, "pipeline registry"},
		{"an internal error is redacted", "/operator/op-1/versions/v1.0.0", user, errors.New(`mongo: auth error for user "operator-repo"`), http.StatusInternalServerError, true, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var call *deleteVersionCall
			del := func(_ context.Context, id string, label string, auth string, opts service.DeleteOptions) error {
				call = &deleteVersionCall{id: id, label: label, auth: auth, opts: opts}
				return tc.result
			}

			rec := deleteVersionRequest(t, del, tc.target, tc.headers)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "mongo") {
				t.Errorf("body leaks internals: %q", rec.Body.String())
			}
			if tc.wantStatus < 300 && rec.Body.Len() != 0 {
				t.Errorf("body = %q, want none", rec.Body.String())
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tc.wantBody)
			}
			if (call != nil) != tc.wantCalled {
				t.Fatalf("service called = %v, want %v", call != nil, tc.wantCalled)
			}
			if call != nil {
				wantLabel := strings.TrimSuffix(strings.SplitN(tc.target, "/versions/", 2)[1], "?force=true")
				if call.id != "op-1" || call.label != wantLabel || call.auth != "Bearer a" || call.opts.Force != tc.wantForce || call.opts.UserId != "user-a" {
					t.Errorf("call = %+v, want op-1, label %q, the caller's token, force %v and user-a", *call, wantLabel, tc.wantForce)
				}
			}
		})
	}
}

func send(t *testing.T, engine *gin.Engine, method, target, body, userId string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-UserId", userId)
	req.Header.Set(HeaderAuthorization, userToken(t, userId))
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// TestIntegrationOperatorVersionsOverHTTP walks the client-visible sequence: put versions, read the
// operator back, and send the whole read result to the update endpoint the way the web-ui does.
func TestIntegrationOperatorVersionsOverHTTP(t *testing.T) {
	engine := testEngine(t)
	createOperators(t, engine, "alpha")
	id := getOperators(t, engine, "/operator", "user-a").Operators[0].Id.Hex()
	target := func(label string) string { return "/operator/" + id + "/versions/" + label }

	if rec := send(t, engine, http.MethodPut, target("v1.0.0"), `{"image":"img:1","inputs":[{"name":"in","type":"string"}]}`, "user-a"); rec.Code != http.StatusCreated {
		t.Fatalf("first put = %d, want 201 (body %q)", rec.Code, rec.Body.String())
	}
	if rec := send(t, engine, http.MethodPut, target("v1.0.0"), `{"image":"img:1","inputs":[{"name":"in","type":"string"}],"deprecated":false}`, "user-a"); rec.Code != http.StatusOK {
		t.Fatalf("second put = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if rec := send(t, engine, http.MethodPut, target("v1.1.0"), `{"version":"v1.0.0","image":"img:2"}`, "user-a"); rec.Code != http.StatusBadRequest {
		t.Errorf("label mismatch = %d, want 400", rec.Code)
	}
	if rec := send(t, engine, http.MethodPut, target("1.1.0"), `{"image":"img:2"}`, "user-a"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad label = %d, want 400", rec.Code)
	}
	if rec := send(t, engine, http.MethodPut, target("v1.1.0"), `{"image":"img:2"}`, "user-b"); rec.Code != http.StatusForbidden {
		t.Errorf("other user = %d, want 403", rec.Code)
	}

	rec := get(t, engine, "/operator/"+id, "user-a")
	var op lib.Operator
	if err := json.Unmarshal(rec.Body.Bytes(), &op); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if op.Image != "img:1" || len(op.Versions) != 1 || op.Version == nil || *op.Version != 3 {
		t.Fatalf("operator = image %q, %d versions, revision %v; want img:1, 1, 3", op.Image, len(op.Versions), op.Version)
	}

	// The web-ui sends the whole read result back, versions included.
	op.Name = "alpha renamed"
	body, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if rec = send(t, engine, http.MethodPost, "/operator/"+id, string(body), "user-a"); rec.Code != http.StatusOK {
		t.Fatalf("update with the read result = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	op.Image = "img:other"
	body, _ = json.Marshal(op)
	if rec = send(t, engine, http.MethodPost, "/operator/"+id, string(body), "user-a"); rec.Code != http.StatusBadRequest {
		t.Errorf("update with a differing image = %d, want 400", rec.Code)
	}
}

// usageServer answers usage requests from answer, keyed by the request's query, and records each
// query. The handler runs on the server's goroutines, so access goes through the mutex.
type usageServer struct {
	mu      sync.Mutex
	answers map[string]string
	queries []string
}

func newUsageServer(t *testing.T, answers map[string]string) (*usageServer, string) {
	t.Helper()
	u := &usageServer{answers: answers}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.queries = append(u.queries, r.URL.RawQuery)
		_, _ = w.Write([]byte(u.answers[r.URL.RawQuery]))
	}))
	t.Cleanup(srv.Close)
	return u, srv.URL
}

func (u *usageServer) set(query, answer string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.answers[query] = answer
}

func (u *usageServer) asked() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.queries)
}

// TestIntegrationDeleteOperatorVersionOverHTTP runs the version delete through the real handler,
// service, permissions and database, with the flow repository and the pipeline registry stubbed.
func TestIntegrationDeleteOperatorVersionOverHTTP(t *testing.T) {
	flows, flowsURL := newUsageServer(t, map[string]string{
		"":               `{"flows":0,"readable":[]}`,
		"version=v1.0.0": `{"flows":0,"readable":[]}`,
		"version=v1.1.0": `{"flows":1,"readable":[{"id":"f1","name":"flow one"}]}`,
	})
	pipelines, pipelinesURL := newUsageServer(t, map[string]string{
		"":              `{"pipelines":1,"readable":[{"id":"p1","name":"pipe one"}]}`,
		"image=img%3A1": `{"pipelines":1,"readable":[]}`,
		"image=img%3A2": `{"pipelines":0,"readable":[]}`,
	})
	engine := testEngineWith(t, flowsURL, pipelinesURL)
	createOperators(t, engine, "alpha")
	id := getOperators(t, engine, "/operator", "user-a").Operators[0].Id.Hex()
	target := func(label string) string { return "/operator/" + id + "/versions/" + label }
	for _, put := range []struct{ label, body string }{{"v1.0.0", `{"image":"img:1"}`}, {"v1.1.0", `{"image":"img:2"}`}} {
		if rec := send(t, engine, http.MethodPut, target(put.label), put.body, "user-a"); rec.Code != http.StatusCreated {
			t.Fatalf("put %s = %d (body %q)", put.label, rec.Code, rec.Body.String())
		}
	}

	refusals := []struct {
		name, label, userId string
		want                int
	}{
		{"another user", "v1.0.0", "user-b", http.StatusForbidden},
		{"a malformed label", "1.0.0", "user-a", http.StatusBadRequest},
		{"an unknown label", "v9.0.0", "user-a", http.StatusNotFound},
	}
	for _, tc := range refusals {
		if rec := send(t, engine, http.MethodDelete, target(tc.label), "", tc.userId); rec.Code != tc.want {
			t.Errorf("%s = %d, want %d (body %q)", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}
	// The usage stubs would let the delete through, so only the order of the checks keeps them unasked.
	if got := append(flows.asked(), pipelines.asked()...); len(got) != 0 {
		t.Errorf("usage was asked before the refusals: %v", got)
	}

	rec := send(t, engine, http.MethodDelete, target("v1.1.0"), "", "user-a")
	if rec.Code != http.StatusConflict {
		t.Fatalf("version pinned by a flow = %d, want 409 (body %q)", rec.Code, rec.Body.String())
	}
	want := `{"error":"version is still used by flows or pipelines","flows":1,"readable":[{"id":"f1","name":"flow one"}],"pipelines":0,"readable_pipelines":[]}`
	if rec.Body.String() != want {
		t.Errorf("body = %s, want %s", rec.Body.String(), want)
	}
	if rec = send(t, engine, http.MethodDelete, target("v1.0.0"), "", "user-a"); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), `"pipelines":1`) {
		t.Errorf("version run by a pipeline = %d, body %q, want 409 with the pipeline count", rec.Code, rec.Body.String())
	}
	if !slices.Equal(flows.asked(), []string{"version=v1.1.0", "version=v1.0.0"}) ||
		!slices.Equal(pipelines.asked(), []string{"image=img%3A2", "image=img%3A1"}) {
		t.Errorf("asked flows %v and pipelines %v, want the version and its image each", flows.asked(), pipelines.asked())
	}

	// The whole operator: no flow, but a pipeline.
	if rec = send(t, engine, http.MethodDelete, "/operator/"+id, "", "user-a"); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), `"error":"operator is still used by flows or pipelines","flows":0,"readable":[],"pipelines":1,"readable_pipelines":[{"id":"p1","name":"pipe one"}]`) {
		t.Errorf("operator run by a pipeline = %d, body %q, want 409 with the usage", rec.Code, rec.Body.String())
	}

	pipelines.set("image=img%3A1", `{"pipelines":0,"readable":[]}`)
	if rec = send(t, engine, http.MethodDelete, target("v1.0.0"), "", "user-a"); rec.Code != http.StatusNoContent {
		t.Fatalf("unused version = %d, want 204 (body %q)", rec.Code, rec.Body.String())
	}
	var op lib.Operator
	if err := json.Unmarshal(get(t, engine, "/operator/"+id, "user-a").Body.Bytes(), &op); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(op.Versions) != 1 || op.Versions[0].Version != "v1.1.0" || op.Image != "img:2" {
		t.Errorf("operator = image %q, versions %+v; want only v1.1.0 with img:2", op.Image, op.Versions)
	}

	// The last version stays, whoever uses it.
	rec = send(t, engine, http.MethodDelete, target("v1.1.0"), "", "user-a")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "delete the operator instead") {
		t.Errorf("only version = %d, body %q, want 400 naming the way out", rec.Code, rec.Body.String())
	}
}
