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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/service"
	gin_mw "github.com/SENERGY-Platform/gin-middleware"
	"github.com/gin-gonic/gin"
)

func TestStatusCodeOfTheDeleteGuard(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"in use", &lib.InUseError{}, http.StatusConflict},
		{"in use, wrapped", fmt.Errorf("delete: %w", &lib.InUseError{}), http.StatusConflict},
		{"version in use", &lib.InUseError{Version: "v1.0.0"}, http.StatusConflict},
		{"flow repository unavailable", lib.ErrFlowRepoUnavailable, http.StatusBadGateway},
		{"pipeline registry unavailable", lib.ErrPipelineRegistryUnavailable, http.StatusBadGateway},
		{"pipeline registry unavailable, wrapped", fmt.Errorf("delete: %w", lib.ErrPipelineRegistryUnavailable), http.StatusBadGateway},
		{"version not found", lib.ErrVersionNotFound, http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusCode(tc.err); got != tc.want {
				t.Errorf("statusCode = %d, want %d", got, tc.want)
			}
			// Their texts are static, so they may reach the caller.
			if got := safeError(tc.err); got != tc.err {
				t.Errorf("safeError replaced %v with %v", tc.err, got)
			}
		})
	}
}

func TestIsAdmin(t *testing.T) {
	adminToken := unsignedToken(t, map[string]any{"sub": "u", "realm_access": map[string]any{"roles": []string{"user", "admin"}}})
	userToken := unsignedToken(t, map[string]any{"sub": "u", "realm_access": map[string]any{"roles": []string{"user"}}})
	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"nothing", nil, false},
		{"admin in the roles header", map[string]string{"X-User-Roles": "user, admin"}, true},
		{"user in the roles header", map[string]string{"X-User-Roles": "user"}, false},
		{"no space after the comma is not split, like getUserId", map[string]string{"X-User-Roles": "user,admin"}, false},
		{"admin token", map[string]string{HeaderAuthorization: "Bearer " + adminToken}, true},
		{"user token", map[string]string{HeaderAuthorization: "Bearer " + userToken}, false},
		{"the header wins over an admin token", map[string]string{"X-User-Roles": "user", HeaderAuthorization: "Bearer " + adminToken}, false},
		{"malformed token", map[string]string{HeaderAuthorization: "not.a.jwt"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAdmin(requestContext(t, "/operator", tc.headers)); got != tc.want {
				t.Errorf("isAdmin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeleteOptions(t *testing.T) {
	admin := map[string]string{"X-User-Roles": "user, admin"}
	user := map[string]string{"X-User-Roles": "user"}
	tests := []struct {
		name      string
		target    string
		headers   map[string]string
		wantForce bool
		wantErr   error
	}{
		{"no force", "/operator/op-1", user, false, nil},
		{"force false", "/operator/op-1?force=false", user, false, nil},
		{"force true by an admin", "/operator/op-1?force=true", admin, true, nil},
		{"force true by a user", "/operator/op-1?force=true", user, false, lib.ErrMissingRights},
		{"force true without any role", "/operator/op-1?force=true", nil, false, lib.ErrMissingRights},
		{"force that is no boolean", "/operator/op-1?force=maybe", admin, false, lib.ErrInvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := requestContext(t, tc.target, tc.headers)
			c.Set(UserIdKey, "user-a")

			opts, err := deleteOptions(c)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if opts.Force != tc.wantForce {
				t.Errorf("Force = %v, want %v", opts.Force, tc.wantForce)
			}
			if err == nil && opts.UserId != "user-a" {
				t.Errorf("UserId = %q, want the caller", opts.UserId)
			}
		})
	}
}

// deleteEngine mounts the real delete handlers behind the real error handling. The service is
// empty: every request in these tests ends before the service is used.
func deleteEngine() *gin.Engine {
	engine := gin.New()
	engine.Use(gin_mw.ErrorHandler(statusCode, ", "))
	engine.DELETE("/operator/:id", deleteOperatorHandler(service.Service{}))
	_, _, batch := deleteOperators(service.Service{})
	engine.DELETE("/operator", batch)
	return engine
}

func TestForceFromANonAdminIsForbiddenOverHTTP(t *testing.T) {
	engine := deleteEngine()
	tests := []struct {
		name, method, target, body string
		headers                    map[string]string
		want                       int
	}{
		{"single, user", http.MethodDelete, "/operator/op-1?force=true", "", map[string]string{"X-User-Roles": "user"}, http.StatusForbidden},
		{"batch, user", http.MethodDelete, "/operator?force=true", `["op-1"]`, map[string]string{"X-User-Roles": "user"}, http.StatusForbidden},
		{"single, bad value", http.MethodDelete, "/operator/op-1?force=maybe", "", map[string]string{"X-User-Roles": "admin"}, http.StatusBadRequest},
		{"batch, bad value", http.MethodDelete, "/operator?force=maybe", `["op-1"]`, map[string]string{"X-User-Roles": "admin"}, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()

			engine.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func failedDelete(t *testing.T, err error, batch bool) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	engine := gin.New()
	engine.Use(gin_mw.ErrorHandler(statusCode, ", "))
	engine.DELETE("/x", func(gc *gin.Context) { deleteFailed(gc, "error deleting operator", err, batch) })
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/x", nil))
	return rec
}

func TestDeleteFailedBodyOfASingleRefusal(t *testing.T) {
	usage := lib.UsedOperator{
		Id:            "op-1",
		OperatorUsage: lib.OperatorUsage{Flows: 3, Readable: []lib.FlowRef{{Id: "f1", Name: "one"}}},
		PipelineUsage: lib.PipelineUsage{Pipelines: 2, ReadablePipelines: []lib.PipelineRef{{Id: "p1", Name: "pipe"}}},
	}
	tests := []struct {
		name      string
		err       *lib.InUseError
		wantError string
	}{
		{"operator", &lib.InUseError{Operators: []lib.UsedOperator{usage}}, "operator is still used by flows or pipelines"},
		{"version", &lib.InUseError{Operators: []lib.UsedOperator{usage}, Version: "v1.0.0"}, "version is still used by flows or pipelines"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := failedDelete(t, tc.err, false)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409", rec.Code)
			}
			var body map[string]json.RawMessage
			if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
				t.Fatalf("body %q: %v", rec.Body.String(), e)
			}
			// The keys of the flow-only body keep their meaning; the pipeline keys come on top.
			want := map[string]string{
				"error":              `"` + tc.wantError + `"`,
				"flows":              `3`,
				"readable":           `[{"id":"f1","name":"one"}]`,
				"pipelines":          `2`,
				"readable_pipelines": `[{"id":"p1","name":"pipe"}]`,
			}
			if len(body) != len(want) {
				t.Errorf("body %s has keys other than %v", rec.Body.String(), want)
			}
			for key, value := range want {
				if string(body[key]) != value {
					t.Errorf("%s = %s, want %s", key, body[key], value)
				}
			}
		})
	}
}

func TestDeleteFailedBodyOfAnUnusedListIsEmptyNotNull(t *testing.T) {
	err := &lib.InUseError{Operators: []lib.UsedOperator{{
		Id:            "op-1",
		OperatorUsage: lib.OperatorUsage{Flows: 0, Readable: []lib.FlowRef{}},
		PipelineUsage: lib.PipelineUsage{Pipelines: 4, ReadablePipelines: []lib.PipelineRef{}},
	}}}

	rec := failedDelete(t, err, false)

	for _, key := range []string{`"readable":[]`, `"readable_pipelines":[]`, `"flows":0`, `"pipelines":4`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("body %s lacks %s", rec.Body.String(), key)
		}
	}
}

func TestDeleteFailedBodyOfABatchRefusalNamesEachUsedOperator(t *testing.T) {
	err := &lib.InUseError{Operators: []lib.UsedOperator{
		{Id: "op-1", OperatorUsage: lib.OperatorUsage{Flows: 1, Readable: []lib.FlowRef{}}},
		{Id: "op-3", OperatorUsage: lib.OperatorUsage{Flows: 2, Readable: []lib.FlowRef{{Id: "f", Name: "n"}}},
			PipelineUsage: lib.PipelineUsage{Pipelines: 1, ReadablePipelines: []lib.PipelineRef{{Id: "p", Name: "q"}}}},
	}}

	rec := failedDelete(t, err, true)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var body lib.InUseBatchResponse
	if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), e)
	}
	if body.Error == "" || len(body.Operators) != 2 || body.Operators[0].Id != "op-1" || body.Operators[1].Id != "op-3" ||
		body.Operators[1].Flows != 2 || len(body.Operators[1].Readable) != 1 ||
		body.Operators[1].Pipelines != 1 || len(body.Operators[1].ReadablePipelines) != 1 || body.Operators[1].ReadablePipelines[0].Name != "q" {
		t.Errorf("body = %+v", body)
	}
	if !strings.Contains(rec.Body.String(), `"readable_pipelines":[{"id":"p","name":"q"}]`) {
		t.Errorf("body %s lacks the pipelines of op-3", rec.Body.String())
	}
}

func TestDeleteFailedWithAnUnreachableServiceIsBadGatewayNamingIt(t *testing.T) {
	tests := []struct {
		err   error
		names string
	}{
		{lib.ErrFlowRepoUnavailable, "flow repository"},
		{lib.ErrPipelineRegistryUnavailable, "pipeline registry"},
	}
	for _, tc := range tests {
		t.Run(tc.names, func(t *testing.T) {
			rec := failedDelete(t, tc.err, false)

			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502 (body %q)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.err.Error()) || !strings.Contains(rec.Body.String(), tc.names) {
				t.Errorf("body = %q, want the message naming the %s", rec.Body.String(), tc.names)
			}
		})
	}
}

func TestDeleteFailedKeepsRedactingOtherErrors(t *testing.T) {
	rec := failedDelete(t, errors.New(`mongo: auth error for user "operator-repo"`), false)

	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "mongo") {
		t.Errorf("status = %d, body %q", rec.Code, rec.Body.String())
	}
}

// TestIntegrationDeleteWithoutRightsAsksNoUsage runs the delete through the real handler, service,
// permissions and database, with usage stubs that would answer 409 if they were asked. A caller
// without the Administrate permission must get 403 and leave the stubs unasked.
func TestIntegrationDeleteWithoutRightsAsksNoUsage(t *testing.T) {
	flows, flowsURL := newUsageServer(t, map[string]string{"": `{"flows":2,"readable":[]}`})
	pipelines, pipelinesURL := newUsageServer(t, map[string]string{"": `{"pipelines":1,"readable":[]}`})
	engine := testEngineWith(t, flowsURL, pipelinesURL)
	createOperators(t, engine, "alpha", "bravo")
	var ids []string
	for _, op := range getOperators(t, engine, "/operator", "user-a").Operators {
		ids = append(ids, op.Id.Hex())
	}
	batch, err := json.Marshal(ids)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	requests := []struct{ name, method, target, body string }{
		{"single", http.MethodDelete, "/operator/" + ids[0], ""},
		{"single with a trailing slash", http.MethodDelete, "/operator/" + ids[0] + "/", ""},
		{"single, forced", http.MethodDelete, "/operator/" + ids[0] + "?force=true", ""},
		{"batch", http.MethodDelete, "/operator", string(batch)},
		{"batch, forced", http.MethodDelete, "/operator?force=true", string(batch)},
	}
	for _, tc := range requests {
		if rec := send(t, engine, tc.method, tc.target, tc.body, "user-b"); rec.Code != http.StatusForbidden {
			t.Errorf("%s = %d, want 403 (body %q)", tc.name, rec.Code, rec.Body.String())
		}
	}
	if got := append(flows.asked(), pipelines.asked()...); len(got) != 0 {
		t.Errorf("usage was asked for a caller without rights: %v", got)
	}

	// A caller with rights still gets the usage; the stubs answer for it.
	if rec := send(t, engine, http.MethodDelete, "/operator/"+ids[0], "", "user-a"); rec.Code != http.StatusConflict {
		t.Errorf("owner = %d, want 409 (body %q)", rec.Code, rec.Body.String())
	}
	if len(flows.asked()) == 0 || len(pipelines.asked()) == 0 {
		t.Errorf("the owner's delete did not ask the usage: flows %v, pipelines %v", flows.asked(), pipelines.asked())
	}
}
