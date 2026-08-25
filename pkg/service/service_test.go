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

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/db"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Same two variables the db package's integration tests use, for the same
// reasons: MONGO_TEST_URL so that the service's own MONGO_URL cannot aim a test
// at a deployment, and REQUIRE_MONGO so that a pipeline cannot go green having
// skipped everything.
const (
	envTestMongoURL  = "MONGO_TEST_URL"
	envRequireMongo  = "REQUIRE_MONGO"
	defaultTestMongo = "localhost:27017"
)

func TestMain(m *testing.M) {
	// newPermissionsClient logs, and main is what normally initialises this.
	util.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	os.Exit(m.Run())
}

func testMongoURL() string {
	if v := os.Getenv(envTestMongoURL); v != "" {
		return v
	}
	return defaultTestMongo
}

// TestNewPermissionsClient covers the branch that used to be in main, where the
// error of the mock constructor was discarded. It needs no infrastructure: the
// in-process client brings its own storage, and the remote one does not connect
// until it is used.
func TestNewPermissionsClient(t *testing.T) {
	t.Run("mock", func(t *testing.T) {
		client, err := newPermissionsClient(t.Context(), "mock")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client == nil {
			t.Fatal("client is nil while the error is nil — the combination the caller used to run into")
		}
		// It has to be the real client interface, not a stub: the topic
		// registration in NewMongoRepo goes through it.
		if _, err, _ = client.ListTopics(permV2Client.InternalAdminToken, permV2Client.ListOptions{}); err != nil {
			t.Errorf("the in-process client does not answer: %v", err)
		}
	})

	t.Run("an address", func(t *testing.T) {
		client, err := newPermissionsClient(t.Context(), "http://permv2.permissions:8080")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client == nil {
			t.Fatal("client is nil while the error is nil")
		}
	})

	t.Run("an empty address is refused", func(t *testing.T) {
		// A client for an empty address would be built happily and then fail on
		// every request, which is a much later and much worse place to notice a
		// missing configuration value.
		client, err := newPermissionsClient(t.Context(), "")
		if err == nil {
			t.Fatal("an empty address was accepted")
		}
		if client != nil {
			t.Error("a client was returned alongside the error")
		}
	})
}

// TestNewWithMockPermissions covers New itself, which reaches the database
// through the reconciliation that NewMongoRepo runs.
func TestNewWithMockPermissions(t *testing.T) {
	database := testDatabase(t)

	srv, err := New(t.Context(), "mock", *database)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv == nil {
		t.Fatal("service is nil while the error is nil")
	}

	// A round trip proves the wiring, not just that the constructor returned.
	if err = srv.CreateOperator(operator("alpha"), "user-a"); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, err := srv.GetOperators("user-a", map[string][]string{}, userToken(t, "user-a"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(resp.Operators) != 1 || resp.Operators[0].Name != "alpha" {
		t.Errorf("listed %d operators (%v), want the one that was created", len(resp.Operators), resp.Operators)
	}
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

func operator(name string) lib.Operator {
	return lib.Operator{Name: name}
}

// userToken mints what the gateway would forward. permissions-v2 parses it
// unverified, so no key material is involved. The db package's integration tests
// carry the same helper; it is four lines and crossing a package boundary for it
// would need an exported test-only API.
func userToken(t *testing.T, userId string) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal token part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := enc(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload := enc(map[string]any{
		"sub":          userId,
		"realm_access": map[string][]string{"roles": {"user"}},
	})
	return "Bearer " + header + "." + payload + ".not-a-signature"
}
