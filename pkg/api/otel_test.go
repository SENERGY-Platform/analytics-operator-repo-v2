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
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	gin_mw "github.com/SENERGY-Platform/gin-middleware"
	"github.com/SENERGY-Platform/gin-middleware/otelx"
	"github.com/SENERGY-Platform/go-service-base/struct-logger/handlers"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/baggage"
)

// tokenWithUsername builds the token the way the platform's parser reads it: the
// claims are decoded, the signature never is.
func tokenWithUsername(t *testing.T, username string) string {
	t.Helper()
	return "Bearer " + unsignedToken(t, map[string]any{
		"sub":                "8fbd0e8a-0000-0000-0000-000000000000",
		"preferred_username": username,
	})
}

// TestBaggageThatCannotBeCarriedDoesNotBreakTheResponse drives the chain New
// installs, because the two middlewares are only interesting together: otelx
// reports a baggage value it cannot carry with c.Error, and ErrorHandler turns
// anything in c.Errors into the response. Without DiscardBaggageErrors a user
// whose username is a display name gets a 500 on every DELETE and a JSON body
// with an error message glued to the end of it on every GET.
func TestBaggageThatCannotBeCarriedDoesNotBreakTheResponse(t *testing.T) {
	usernames := map[string]string{
		"a space":     "Jonah Windolph",
		"a comma":     "Windolph,Jonah",
		"non-ascii":   "müller",
		"a semicolon": "a;b",
	}
	for name, username := range usernames {
		t.Run(name, func(t *testing.T) {
			router := routerLikeNew(t)

			t.Run("a body stays valid JSON", func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/operator/3c1f9b42", nil)
				req.Header.Set(HeaderAuthorization, tokenWithUsername(t, username))
				router.ServeHTTP(rec, req)

				if rec.Code != http.StatusOK {
					t.Errorf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
				}
				var decoded map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
					t.Fatalf("body must stay valid JSON, got %q: %v", rec.Body.String(), err)
				}
				if decoded["_id"] != "3c1f9b42" {
					t.Errorf("body = %v, want the operator that was asked for", decoded)
				}
			})

			t.Run("a bodyless route keeps its status", func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodDelete, "/operator/3c1f9b42", nil)
				req.Header.Set(HeaderAuthorization, tokenWithUsername(t, username))
				router.ServeHTTP(rec, req)

				if rec.Code != http.StatusNoContent {
					t.Errorf("status = %d, want 204 (body %q)", rec.Code, rec.Body.String())
				}
			})
		})
	}
}

// TestAValidUsernameStillReachesTheBaggage keeps the guard above from becoming a
// blanket suppression: what the format can carry has to arrive.
func TestAValidUsernameStillReachesTheBaggage(t *testing.T) {
	router := gin.New()
	otelHandler, err := otelx.GinOpenTelemetry(t.Context(), ServiceName, "")
	if err != nil {
		t.Fatalf("set up OpenTelemetry: %v", err)
	}
	var seen baggage.Baggage
	router.Use(otelHandler, DiscardBaggageErrors())
	router.GET("/x", func(gc *gin.Context) {
		seen = baggage.FromContext(gc.Request.Context())
		gc.Status(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(HeaderAuthorization, tokenWithUsername(t, "jonah"))
	router.ServeHTTP(rec, req)

	if got := seen.Member("username").Value(); got != "jonah" {
		t.Errorf("username in the baggage = %q, want %q", got, "jonah")
	}
	if got := seen.Member("user_id").Value(); got != "8fbd0e8a-0000-0000-0000-000000000000" {
		t.Errorf("user_id in the baggage = %q, want the token's subject", got)
	}
}

// TestAHandlerErrorStillBecomesAResponse is the other half of the same guard: an
// error this service reports itself must still reach the caller.
func TestAHandlerErrorStillBecomesAResponse(t *testing.T) {
	router := gin.New()
	otelHandler, err := otelx.GinOpenTelemetry(t.Context(), ServiceName, "")
	if err != nil {
		t.Fatalf("set up OpenTelemetry: %v", err)
	}
	router.Use(otelHandler, DiscardBaggageErrors(), gin_mw.ErrorHandler(statusCode, ", "))
	router.GET("/x", func(gc *gin.Context) {
		_ = gc.Error(lib.ErrNotFound)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	// A username that also fails the baggage, so both kinds of error are present.
	req.Header.Set(HeaderAuthorization, tokenWithUsername(t, "Jonah Windolph"))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (body %q)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != lib.ErrNotFound.Error() {
		t.Errorf("body = %q, want only the handler's own error", rec.Body.String())
	}
}

// TestLogRecordCarriesBaggageOnce pins the composition InitStructLogger builds.
// struct-logger v0.8.0 wrapped the base handler in a ContextHandler underneath the
// OpenTelemetry handler, and both loops added the same baggage, so every entry
// appeared twice; a downgrade would bring that back without failing anything else.
func TestLogRecordCarriesBaggageOnce(t *testing.T) {
	member, err := baggage.NewMember("user_id", "user-a")
	if err != nil {
		t.Fatalf("build baggage member: %v", err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatalf("build baggage: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(handlers.NewOpenTelemetryHandler(slog.NewJSONHandler(&buf, nil)))
	logger.WarnContext(baggage.ContextWithBaggage(t.Context(), bag), "error getting operators")

	var record map[string]any
	if err = json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("decode log record %q: %v", buf.String(), err)
	}
	if record["user_id"] != "user-a" {
		t.Fatalf("record = %v, want the baggage entry as an attribute", record)
	}
	if got := strings.Count(buf.String(), `"user_id"`); got != 1 {
		t.Errorf("user_id appears %d times in %q, want once", got, buf.String())
	}
}

// routerLikeNew mirrors the middleware chain New installs, minus the service: what
// is under test is the interaction of the handlers, not the endpoints.
func routerLikeNew(t *testing.T) *gin.Engine {
	t.Helper()
	router := gin.New()
	otelHandler, err := otelx.GinOpenTelemetry(t.Context(), ServiceName, "")
	if err != nil {
		t.Fatalf("set up OpenTelemetry: %v", err)
	}
	router.Use(otelHandler, DiscardBaggageErrors(), gin_mw.ErrorHandler(statusCode, ", "))
	router.GET("/operator/:id", func(gc *gin.Context) {
		gc.JSON(http.StatusOK, map[string]string{"_id": gc.Param("id")})
	})
	router.DELETE("/operator/:id", func(gc *gin.Context) {
		gc.Status(http.StatusNoContent)
	})
	return router
}
