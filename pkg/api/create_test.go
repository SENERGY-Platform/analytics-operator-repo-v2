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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	gin_mw "github.com/SENERGY-Platform/gin-middleware"
	"github.com/gin-gonic/gin"
)

func putOperatorRequest(t *testing.T, create operatorCreator, body string) *httptest.ResponseRecorder {
	t.Helper()
	engine := gin.New()
	engine.Use(gin_mw.ErrorHandler(statusCode, ", "))
	engine.PUT("/operator/", putOperatorHandler(create))
	req := httptest.NewRequest(http.MethodPut, "/operator/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestPutOperatorHandlerAnswersWithTheNewId(t *testing.T) {
	var gotName string
	rec := putOperatorRequest(t, func(_ context.Context, operator lib.Operator, _ string) (string, error) {
		gotName = operator.Name
		return "65f0c0ffee0123456789abcd", nil
	}, `{"name":"alpha"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %q)", rec.Code, rec.Body.String())
	}
	if gotName != "alpha" {
		t.Errorf("service got name %q, want alpha", gotName)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if len(body) != 1 || body["_id"] != "65f0c0ffee0123456789abcd" {
		t.Errorf("body = %v, want only _id 65f0c0ffee0123456789abcd", body)
	}
}

func TestPutOperatorHandlerAnswersNoIdOnFailure(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{"a malformed body", `{"name":`, nil, http.StatusBadRequest, false},
		{"invalid input from the service", `{"name":"alpha"}`, lib.ErrInvalidInput, http.StatusBadRequest, true},
		{"a failing store", `{"name":"alpha"}`, errors.New("connection reset"), http.StatusInternalServerError, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			rec := putOperatorRequest(t, func(context.Context, lib.Operator, string) (string, error) {
				called = true
				return "", tt.err
			}, tt.body)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if called != tt.wantCalled {
				t.Errorf("service called = %v, want %v", called, tt.wantCalled)
			}
			if strings.Contains(rec.Body.String(), "_id") {
				t.Errorf("body %q carries an id although nothing was created", rec.Body.String())
			}
		})
	}
}
