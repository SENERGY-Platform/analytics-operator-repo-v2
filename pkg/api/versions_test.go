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
	"testing"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/service"
	gin_mw "github.com/SENERGY-Platform/gin-middleware"
	"github.com/gin-gonic/gin"
)

func TestPutOperatorVersionRouteIsRegistered(t *testing.T) {
	engine := gin.New()
	if _, err := routesAuth.Set(service.Service{}, engine.Group("")); err != nil {
		t.Fatalf("set routes: %v", err)
	}
	found := slices.ContainsFunc(engine.Routes(), func(r gin.RouteInfo) bool {
		return r.Method == http.MethodPut && r.Path == "/operator/:id/versions/:version"
	})
	if !found {
		t.Errorf("PUT /operator/:id/versions/:version is not registered: %v", engine.Routes())
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
