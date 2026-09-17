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
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/db"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/service"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	"github.com/SENERGY-Platform/go-service-base/struct-logger/handlers"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Same two variables the db and service integration tests use: MONGO_TEST_URL so
// that the service's own MONGO_URL cannot aim a test at a deployment, and
// REQUIRE_MONGO so that a pipeline cannot go green having skipped everything.
const (
	envTestMongoURL  = "MONGO_TEST_URL"
	envRequireMongo  = "REQUIRE_MONGO"
	defaultTestMongo = "localhost:27017"
)

// TestIntegrationGetAllQueryParametersOverHTTP pins what a caller of GET
// /operator gets back, status code and body, rather than what All returns. The
// db tests already cover All; what they cannot show is the half of the path the
// clients actually see — statusCode, safeError and ErrorHandler composing into
// the response. A rejected limit reaches the browser as a 400 with an empty
// operator list, which is how a query that v1 accepted turns into an empty
// screen instead of an error.
func TestIntegrationGetAllQueryParametersOverHTTP(t *testing.T) {
	engine := testEngine(t)

	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "no parameters",
			query:      "",
			wantStatus: http.StatusOK,
		},
		{
			name:       "limit at the cap",
			query:      "?limit=1000",
			wantStatus: http.StatusOK,
		},
		{
			// The documented way to ask for everything, and what user-management
			// sends when it collects the rows of a user it is deleting.
			name:       "limit zero",
			query:      "?limit=0",
			wantStatus: http.StatusOK,
		},
		{
			name:       "one above the cap",
			query:      "?limit=1001",
			wantStatus: http.StatusBadRequest,
			wantBody:   "limit exceeds maximum of 1000; use limit=0 for no limit",
		},
		{
			// The literal value the web-ui sends from six places (flow designer,
			// the two filter dialogs, deployments config, cost overview, the data
			// table widget) as its idiom for "all of them". v1 passed it straight
			// to MongoDB; here it is a 400, and the web-ui's error handler turns
			// that into an empty list without a message to the user.
			name:       "what the web-ui sends for all operators",
			query:      "?limit=9999",
			wantStatus: http.StatusBadRequest,
			wantBody:   "limit exceeds maximum of 1000; use limit=0 for no limit",
		},
		{
			name:       "negative limit",
			query:      "?limit=-1",
			wantStatus: http.StatusBadRequest,
			wantBody:   "limit must be a non-negative integer",
		},
		{
			name:       "limit that is not a number",
			query:      "?limit=abc",
			wantStatus: http.StatusBadRequest,
			wantBody:   "limit must be a non-negative integer",
		},
		{
			name:       "negative offset",
			query:      "?offset=-1",
			wantStatus: http.StatusBadRequest,
			wantBody:   "offset must be a non-negative integer",
		},
		{
			name:       "offset past the end is not an error",
			query:      "?offset=100",
			wantStatus: http.StatusOK,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, engine, "/operator"+tc.query, "user-a")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

// TestIntegrationGetAllPageAndTotalOverHTTP pins the distinction that makes the
// cap survivable for a client: operators is the page, totalCount is the whole
// filter. A client that asks for fewer rows than exist can tell a short page
// from a short collection — the only way a capped response is not a silent loss.
func TestIntegrationGetAllPageAndTotalOverHTTP(t *testing.T) {
	engine := testEngine(t)
	createOperators(t, engine, "alpha", "beta", "gamma")

	t.Run("a limited page reports the full total", func(t *testing.T) {
		resp := getOperators(t, engine, "/operator?limit=2&sort=name:asc", "user-a")
		if len(resp.Operators) != 2 {
			t.Errorf("page has %d operators, want 2", len(resp.Operators))
		}
		if resp.Total != 3 {
			t.Errorf("totalCount = %d, want 3", resp.Total)
		}
	})

	t.Run("limit zero returns every row", func(t *testing.T) {
		resp := getOperators(t, engine, "/operator?limit=0", "user-a")
		if len(resp.Operators) != 3 {
			t.Errorf("page has %d operators, want 3", len(resp.Operators))
		}
	})
}

// TestIntegrationRejectedQueryIsNotAnErrorLogEntry pins the level over the whole
// request path rather than over logError alone: a caller's own query mistake is
// what this service answers with a 400, and an ERROR entry for it is what made
// the real ones hard to find.
func TestIntegrationRejectedQueryIsNotAnErrorLogEntry(t *testing.T) {
	engine := testEngine(t)

	var buf bytes.Buffer
	restore := util.Logger
	util.Logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { util.Logger = restore })

	rec := get(t, engine, "/operator?limit=9999", "user-a")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Errorf("a rejected query produced an ERROR entry:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "error getting operators") {
		t.Errorf("the refusal was not logged at all:\n%s", buf.String())
	}
}

// TestIntegrationLogRecordCarriesTheCallersBaggage is what the OpenTelemetry
// wiring is for: a log line written while serving a request has to say which
// caller it belongs to. The logger is composed the way InitStructLogger composes
// it, because the attribute comes from the handler wrapper and not from the call.
func TestIntegrationLogRecordCarriesTheCallersBaggage(t *testing.T) {
	engine := testEngine(t)

	var buf bytes.Buffer
	restore := util.Logger
	util.Logger = slog.New(handlers.NewOpenTelemetryHandler(
		slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { util.Logger = restore })

	if rec := get(t, engine, "/operator?limit=9999", "user-a"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if record["msg"] != "error getting operators" {
			continue
		}
		found = true
		if record["user_id"] != "user-a" {
			t.Errorf("record = %v, want the caller's user_id from the request baggage", record)
		}
	}
	if !found {
		t.Fatalf("the refusal was not logged:\n%s", buf.String())
	}
}

// get sends the request the way the gateway would: an X-UserId header and a
// token, both of which AuthMiddleware and the permission client read.
func get(t *testing.T, engine *gin.Engine, target string, userId string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("X-UserId", userId)
	req.Header.Set(HeaderAuthorization, userToken(t, userId))
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func getOperators(t *testing.T, engine *gin.Engine, target string, userId string) lib.OperatorResponse {
	t.Helper()
	rec := get(t, engine, target, userId)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %q)", target, rec.Code, rec.Body.String())
	}
	var resp lib.OperatorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return resp
}

func createOperators(t *testing.T, engine *gin.Engine, names ...string) {
	t.Helper()
	for _, name := range names {
		body, err := json.Marshal(lib.Operator{Name: name})
		if err != nil {
			t.Fatalf("marshal operator: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/operator/", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-UserId", "user-a")
		req.Header.Set(HeaderAuthorization, userToken(t, "user-a"))
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("PUT /operator/ %s = %d, want 201 (body %q)", name, rec.Code, rec.Body.String())
		}
	}
}

// testEngine builds the whole handler stack over a database of its own, with the
// in-process permissions client. Nothing is stubbed between the request and
// MongoDB, because the parameters under test are read in the query layer and a
// stub would answer for them itself.
func testEngine(t *testing.T) *gin.Engine {
	t.Helper()
	database := testDatabase(t)
	srv, err := service.New(t.Context(), "mock", *database)
	if err != nil {
		t.Fatalf("build service: %v", err)
	}
	engine, err := New(t.Context(), *srv, "", "")
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	return engine
}

// testDatabase opens a database of its own and drops it afterwards. A missing
// MongoDB skips on a workstation and fails wherever REQUIRE_MONGO is set.
func testDatabase(t *testing.T) *db.MongoDB {
	t.Helper()
	url := testMongoURL()
	probe, err := mongo.Connect(options.Client().
		ApplyURI("mongodb://" + url).
		SetServerSelectionTimeout(2 * time.Second))
	if err != nil {
		t.Fatalf("build probe client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = probe.Ping(ctx, nil); err != nil {
		_ = probe.Disconnect(context.Background())
		if os.Getenv(envRequireMongo) != "" {
			t.Fatalf("%s is set but no MongoDB at %s: %v", envRequireMongo, url, err)
		}
		t.Skipf("no MongoDB at %s (set %s, or %s=1 to make this a failure): %v",
			url, envTestMongoURL, envRequireMongo, err)
	}

	name := "operators_test_" + strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, t.Name())

	database, err := db.New(url, name)
	if err != nil {
		_ = probe.Disconnect(context.Background())
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if err := probe.Database(name).Drop(dropCtx); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
		_ = probe.Disconnect(context.Background())
		database.Disconnect()
	})
	return database
}

func testMongoURL() string {
	if v := os.Getenv(envTestMongoURL); v != "" {
		return v
	}
	return defaultTestMongo
}

// userToken mints what the gateway would forward. permissions-v2 parses it
// unverified, so no key material is involved. The role is user and never admin:
// an admin token passes every check before the resource is looked up.
func userToken(t *testing.T, userId string) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal token part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return fmt.Sprintf("%s.%s.not-a-signature",
		enc(map[string]string{"alg": "RS256", "typ": "JWT"}),
		enc(map[string]any{
			"sub":          userId,
			"realm_access": map[string][]string{"roles": {"user"}},
		}))
}
