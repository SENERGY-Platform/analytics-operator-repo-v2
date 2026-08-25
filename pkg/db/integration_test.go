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

package db

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// The address is read from MONGO_TEST_URL rather than MONGO_URL on purpose:
// MONGO_URL is the service's own variable, and a shell that has it pointed at a
// deployment would otherwise make these tests write into it. The tests create
// their own database and drop it again, so they must never reach a real one.
const (
	envTestMongoURL  = "MONGO_TEST_URL"
	envRequireMongo  = "REQUIRE_MONGO"
	defaultTestMongo = "localhost:27017"
)

var probed *mongo.Client

// sharedClient connects once per package run. Probing per test would cost the
// server-selection timeout for every one of them on a workstation without a
// database — a minute of waiting to skip.
var sharedClient = sync.OnceValues(func() (*mongo.Client, error) {
	url := testMongoURL()
	client, err := mongo.Connect(options.Client().
		ApplyURI("mongodb://" + url).
		SetServerSelectionTimeout(2 * time.Second))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	probed = client
	return client, nil
})

func TestMain(m *testing.M) {
	// Repo code logs through this package-level var, which main initialises.
	util.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	code := m.Run()
	if probed != nil {
		_ = probed.Disconnect(context.Background())
	}
	os.Exit(code)
}

func testMongoURL() string {
	if v := os.Getenv(envTestMongoURL); v != "" {
		return v
	}
	return defaultTestMongo
}

// requireMongo hands out the shared client. A missing database skips the test on
// a workstation and fails it wherever REQUIRE_MONGO is set — the strict mode is
// the one the pipeline opts into, so a green pipeline cannot mean "never
// attempted".
func requireMongo(t *testing.T) *mongo.Client {
	t.Helper()
	client, err := sharedClient()
	if err != nil {
		if os.Getenv(envRequireMongo) != "" {
			t.Fatalf("%s is set but no MongoDB at %s: %v", envRequireMongo, testMongoURL(), err)
		}
		t.Skipf("no MongoDB at %s (set %s, or %s=1 to make this a failure): %v",
			testMongoURL(), envTestMongoURL, envRequireMongo, err)
	}
	return client
}

// testCollection returns a collection in a database of its own, dropped when the
// test ends.
func testCollection(t *testing.T) *mongo.Collection {
	t.Helper()
	client := requireMongo(t)

	dbName := testDatabaseName(t)

	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if err := client.Database(dbName).Drop(dropCtx); err != nil {
			t.Errorf("drop test database %s: %v", dbName, err)
		}
	})
	return client.Database(dbName).Collection("operators")
}

// testDatabaseName derives a database of its own for each test, so that two runs
// against the same MongoDB — two developers, or CI overlapping with a local run —
// cannot drop each other's data mid-test. Database names are limited to 63 bytes
// and cannot carry every character a test name may hold.
func testDatabaseName(t *testing.T) string {
	t.Helper()
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, t.Name())
	if len(name) > 40 {
		name = name[:40]
	}
	return "operators_test_" + name
}

// testRepo wires the repository against a throwaway collection and the
// in-process permissions-v2 controller, which needs neither a database nor
// Kafka. Permission decisions are therefore the real ones, not a stub's.
func testRepo(t *testing.T) (*MongoRepo, *mongo.Collection) {
	t.Helper()
	coll := testCollection(t)
	perm, err := permV2Client.NewTestClient(t.Context())
	if err != nil {
		t.Fatalf("permissions-v2 test client: %v", err)
	}
	repo, err := NewMongoRepo(perm, coll)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	return repo, coll
}

// userToken mints what the gateway would forward. permissions-v2 parses it
// unverified, so no key material is involved — the same property that makes the
// gateway load-bearing in production.
func userToken(t *testing.T, userId string, roles ...string) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal token part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	if roles == nil {
		roles = []string{"user"}
	}
	header := enc(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload := enc(map[string]any{
		"sub":          userId,
		"realm_access": map[string][]string{"roles": roles},
	})
	return "Bearer " + header + "." + payload + ".not-a-signature"
}

func insert(t *testing.T, repo *MongoRepo, userId string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := repo.InsertOperator(lib.Operator{Name: name, UserId: userId}); err != nil {
			t.Fatalf("insert %q: %v", name, err)
		}
	}
}

// idOf reads an operator's id straight from the collection, because InsertOperator
// does not report it — a gap the API inherits.
func idOf(t *testing.T, coll *mongo.Collection, name string) string {
	t.Helper()
	var op lib.Operator
	if err := coll.FindOne(context.Background(), bson.M{"name": name}).Decode(&op); err != nil {
		t.Fatalf("look up %q: %v", name, err)
	}
	if op.Id == nil {
		t.Fatalf("operator %q has no id", name)
	}
	return op.Id.Hex()
}

func names(ops []lib.Operator) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.Name)
	}
	return out
}

func TestIntegrationInsertAndFind(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha")
	id := idOf(t, coll, "alpha")

	t.Run("owner reads it", func(t *testing.T) {
		op, err := repo.FindOperator(id, userToken(t, "user-a"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if op.Name != "alpha" {
			t.Errorf("name = %q, want alpha", op.Name)
		}
		if op.Version == nil || *op.Version != 1 {
			t.Errorf("version = %v, want 1", op.Version)
		}
		if op.DateCreated.IsZero() || op.DateUpdated.IsZero() {
			t.Error("timestamps were not set on insert")
		}
	})

	t.Run("another user is refused", func(t *testing.T) {
		_, err := repo.FindOperator(id, userToken(t, "user-b"))
		if !errors.Is(err, lib.ErrMissingRights) {
			t.Errorf("error = %v, want ErrMissingRights", err)
		}
	})

	t.Run("an unknown id is refused, not reported as missing", func(t *testing.T) {
		// 403 and not 404: permissions-v2 cannot tell "no rights" from "does not
		// exist", and saying which would let a caller enumerate ids.
		_, err := repo.FindOperator(bson.NewObjectID().Hex(), userToken(t, "user-a"))
		if !errors.Is(err, lib.ErrMissingRights) {
			t.Errorf("error = %v, want ErrMissingRights", err)
		}
	})

	t.Run("rights held but document gone is a 404", func(t *testing.T) {
		// The other half of that decision: once the permission check passes, a
		// missing document is a genuine not-found.
		if _, err := coll.DeleteOne(context.Background(), bson.M{"name": "alpha"}); err != nil {
			t.Fatalf("delete document: %v", err)
		}
		_, err := repo.FindOperator(id, userToken(t, "user-a"))
		if !errors.Is(err, lib.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("a malformed id never reaches the database", func(t *testing.T) {
		_, err := repo.FindOperator("not-an-id", userToken(t, "user-a"))
		if !errors.Is(err, lib.ErrInvalidInput) {
			t.Errorf("error = %v, want ErrInvalidInput", err)
		}
	})
}

func TestIntegrationAllSortingAndPaging(t *testing.T) {
	repo, _ := testRepo(t)
	insert(t, repo, "user-a", "delta", "alpha", "charlie", "bravo")
	auth := userToken(t, "user-a")

	tests := []struct {
		name string
		args map[string][]string
		want []string
		// unordered marks the cases that pass no sort. MongoDB returns those in
		// storage order, which it does not promise to keep — a document that is
		// rewritten can move. Asserting the insertion order there would fail on a
		// code path that did not change, so only the contents are checked.
		unordered bool
	}{
		{
			name:      "no arguments",
			args:      map[string][]string{},
			want:      []string{"alpha", "bravo", "charlie", "delta"},
			unordered: true,
		},
		{
			name: "sort ascending",
			args: map[string][]string{"sort": {"name:asc"}},
			want: []string{"alpha", "bravo", "charlie", "delta"},
		},
		{
			name: "sort descending",
			args: map[string][]string{"sort": {"name:desc"}},
			want: []string{"delta", "charlie", "bravo", "alpha"},
		},
		{
			// A bare field name means ascending, which is what the documented
			// field[:asc|desc] form implies.
			name: "sort without a direction",
			args: map[string][]string{"sort": {"name"}},
			want: []string{"alpha", "bravo", "charlie", "delta"},
		},
		{
			// Only name is sortable. An unsupported field is ignored rather than
			// rejected, and must not reach the driver — passing it through used
			// to panic on a sort query without a direction. Ignored means no sort
			// is applied at all, so the order is again not the point here.
			name:      "sort by an unsupported field",
			args:      map[string][]string{"sort": {"userId:desc"}},
			want:      []string{"alpha", "bravo", "charlie", "delta"},
			unordered: true,
		},
		{
			name: "limit",
			args: map[string][]string{"sort": {"name:asc"}, "limit": {"2"}},
			want: []string{"alpha", "bravo"},
		},
		{
			name: "offset",
			args: map[string][]string{"sort": {"name:asc"}, "offset": {"2"}},
			want: []string{"charlie", "delta"},
		},
		{
			name: "limit and offset together",
			args: map[string][]string{"sort": {"name:asc"}, "limit": {"1"}, "offset": {"1"}},
			want: []string{"bravo"},
		},
		{
			name: "offset past the end",
			args: map[string][]string{"offset": {"99"}},
			want: []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := repo.All("user-a", false, tc.args, auth)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := names(resp.Operators)
			want := tc.want
			if tc.unordered {
				got = slices.Sorted(slices.Values(got))
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("operators = %v, want %v (order %s)", names(resp.Operators), want,
					map[bool]string{true: "ignored", false: "significant"}[tc.unordered])
			}
			// Total counts what matches the filter, not what the page returned.
			if resp.Total != 4 {
				t.Errorf("Total = %d, want 4", resp.Total)
			}
		})
	}
}

func TestIntegrationAllRejectsBadParameters(t *testing.T) {
	repo, _ := testRepo(t)
	insert(t, repo, "user-a", "alpha")
	auth := userToken(t, "user-a")

	for _, args := range []map[string][]string{
		{"limit": {"-1"}},
		{"limit": {"abc"}},
		{"limit": {""}},
		{"offset": {"-1"}},
		{"offset": {"abc"}},
		{"limit": {fmt.Sprint(MaxLimit + 1)}},
	} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			_, err := repo.All("user-a", false, args, auth)
			if !errors.Is(err, lib.ErrInvalidInput) {
				t.Errorf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestIntegrationAllLimitContract is the reason this file exists. limit=0 means
// "all elements" to MongoDB, and user-management calls /operator?limit=0 to find
// the rows of a user it is deleting. Clamping it to MaxLimit would make deletion
// silently miss everything past the cap, so the two behaviours are pinned side
// by side: absent means capped, zero means unlimited.
func TestIntegrationAllLimitContract(t *testing.T) {
	repo, coll := testRepo(t)
	const total = MaxLimit + 1

	// Written straight to the collection: going through InsertOperator would add
	// a permissions-v2 round trip per row for no gain here, since the owner term
	// of the filter already matches.
	docs := make([]any, 0, total)
	version := int64(1)
	for i := range total {
		docs = append(docs, lib.Operator{
			Name:        fmt.Sprintf("operator-%04d", i),
			UserId:      "user-a",
			Version:     &version,
			DateCreated: time.Now(),
			DateUpdated: time.Now(),
		})
	}
	if _, err := coll.InsertMany(context.Background(), docs); err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
	auth := userToken(t, "user-a")

	t.Run("no limit caps at MaxLimit", func(t *testing.T) {
		resp, err := repo.All("user-a", false, map[string][]string{}, auth)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.Operators) != MaxLimit {
			t.Errorf("returned %d operators, want %d", len(resp.Operators), MaxLimit)
		}
		// The cap applies to the page, not to the count — a client has to be able
		// to see that there is more.
		if resp.Total != total {
			t.Errorf("Total = %d, want %d", resp.Total, total)
		}
	})

	t.Run("limit=0 returns everything", func(t *testing.T) {
		resp, err := repo.All("user-a", false, map[string][]string{"limit": {"0"}}, auth)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.Operators) != total {
			t.Errorf("returned %d operators, want all %d — user deletion depends on this", len(resp.Operators), total)
		}
	})
}

// TestIntegrationSearchIsLiteral covers the regex injection: the search value is
// interpolated into a $regex, so without QuoteMeta a caller could run arbitrary
// expressions on the server.
func TestIntegrationSearchIsLiteral(t *testing.T) {
	repo, _ := testRepo(t)
	insert(t, repo, "user-a", "alpha", "a.pha", "a+b", "(paren)", "ALPHA")
	auth := userToken(t, "user-a")

	tests := []struct {
		search string
		want   []string
	}{
		// A dot must match a dot, not any character.
		{search: "a.pha", want: []string{"a.pha"}},
		// The classic probe: as a regex this matches everything.
		{search: ".*", want: []string{}},
		{search: "a+b", want: []string{"a+b"}},
		{search: "(paren)", want: []string{"(paren)"}},
		// Substring, and case-sensitive as documented.
		{search: "lph", want: []string{"alpha"}},
		{search: "LPH", want: []string{"ALPHA"}},
		{search: "nothing here", want: []string{}},
	}
	for _, tc := range tests {
		t.Run("search="+tc.search, func(t *testing.T) {
			resp, err := repo.All("user-a", false, map[string][]string{
				"search": {tc.search},
				"sort":   {"name:asc"},
			}, auth)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := names(resp.Operators)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("operators = %v, want %v", got, tc.want)
			}
			if resp.Total != int64(len(tc.want)) {
				t.Errorf("Total = %d, want %d", resp.Total, len(tc.want))
			}
		})
	}
}

func TestIntegrationUpdate(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha")
	id := idOf(t, coll, "alpha")

	t.Run("owner updates and the version moves", func(t *testing.T) {
		before, err := repo.FindOperator(id, userToken(t, "user-a"))
		if err != nil {
			t.Fatalf("read before: %v", err)
		}
		if err = repo.UpdateOperator(id, lib.Operator{Name: "alpha renamed", Image: "img:1"}, userToken(t, "user-a")); err != nil {
			t.Fatalf("update: %v", err)
		}
		after, err := repo.FindOperator(id, userToken(t, "user-a"))
		if err != nil {
			t.Fatalf("read after: %v", err)
		}
		if after.Name != "alpha renamed" {
			t.Errorf("name = %q, want %q", after.Name, "alpha renamed")
		}
		if after.Version == nil || *after.Version != 2 {
			t.Errorf("version = %v, want 2", after.Version)
		}
		if !after.DateUpdated.After(before.DateUpdated) {
			t.Errorf("dateUpdated did not move: %v then %v", before.DateUpdated, after.DateUpdated)
		}
		// The owner is not in the $set list, so an update cannot reassign it.
		if after.UserId != "user-a" {
			t.Errorf("userId = %q, want user-a", after.UserId)
		}
		if !after.DateCreated.Equal(before.DateCreated) {
			t.Error("dateCreated was changed by an update")
		}
	})

	t.Run("another user is refused and changes nothing", func(t *testing.T) {
		err := repo.UpdateOperator(id, lib.Operator{Name: "hijacked"}, userToken(t, "user-b"))
		if !errors.Is(err, lib.ErrMissingRights) {
			t.Fatalf("error = %v, want ErrMissingRights", err)
		}
		op, err := repo.FindOperator(id, userToken(t, "user-a"))
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if op.Name == "hijacked" {
			t.Error("the refused update was applied anyway")
		}
	})

	t.Run("rights held but document gone is a 404", func(t *testing.T) {
		if _, err := coll.DeleteOne(context.Background(), bson.M{"_id": mustObjectID(t, id)}); err != nil {
			t.Fatalf("delete document: %v", err)
		}
		err := repo.UpdateOperator(id, lib.Operator{Name: "x"}, userToken(t, "user-a"))
		if !errors.Is(err, lib.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	})
}

func mustObjectID(t *testing.T, id string) bson.ObjectID {
	t.Helper()
	objID, err := objectID(id)
	if err != nil {
		t.Fatalf("parse id %q: %v", id, err)
	}
	return objID
}

func TestIntegrationDelete(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha", "bravo")
	alpha := idOf(t, coll, "alpha")

	t.Run("another user is refused and the document stays", func(t *testing.T) {
		err := repo.DeleteOperator(alpha, userToken(t, "user-b"))
		if !errors.Is(err, lib.ErrMissingRights) {
			t.Fatalf("error = %v, want ErrMissingRights", err)
		}
		n, err := coll.CountDocuments(context.Background(), bson.M{"_id": mustObjectID(t, alpha)})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Error("the refused delete removed the document")
		}
	})

	t.Run("owner deletes it and the permission goes with it", func(t *testing.T) {
		if err := repo.DeleteOperator(alpha, userToken(t, "user-a")); err != nil {
			t.Fatalf("delete: %v", err)
		}
		n, err := coll.CountDocuments(context.Background(), bson.M{"_id": mustObjectID(t, alpha)})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 0 {
			t.Error("the document is still there")
		}
		// A permission entry left behind would keep granting access to an id that
		// a later insert could reuse.
		if _, err = repo.FindOperator(alpha, userToken(t, "user-a")); !errors.Is(err, lib.ErrMissingRights) {
			t.Errorf("error after delete = %v, want ErrMissingRights", err)
		}
	})

	t.Run("deleting it twice reports missing rights", func(t *testing.T) {
		if err := repo.DeleteOperator(alpha, userToken(t, "user-a")); !errors.Is(err, lib.ErrMissingRights) {
			t.Errorf("error = %v, want ErrMissingRights", err)
		}
	})
}

func TestIntegrationDeleteOperators(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha", "bravo")
	insert(t, repo, "user-b", "charlie")
	alpha := idOf(t, coll, "alpha")
	bravo := idOf(t, coll, "bravo")
	charlie := idOf(t, coll, "charlie")

	t.Run("one id the caller may not touch refuses the whole call", func(t *testing.T) {
		// The rights of every id are checked before the first delete, so nothing
		// is removed when one of them is refused.
		err := repo.DeleteOperators([]string{alpha, charlie}, userToken(t, "user-a"))
		if !errors.Is(err, lib.ErrMissingRights) {
			t.Fatalf("error = %v, want ErrMissingRights", err)
		}
		n, err := coll.CountDocuments(context.Background(), bson.M{})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 3 {
			t.Errorf("%d documents left, want all 3 — a refused batch must not delete part of itself", n)
		}
	})

	t.Run("all permitted", func(t *testing.T) {
		if err := repo.DeleteOperators([]string{alpha, bravo}, userToken(t, "user-a")); err != nil {
			t.Fatalf("delete: %v", err)
		}
		n, err := coll.CountDocuments(context.Background(), bson.M{})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Errorf("%d documents left, want 1", n)
		}
	})
}

// TestIntegrationValidateOperatorPermissionsBeyondMaxLimit is the regression that
// the MaxLimit work introduced once: reconciliation read the collection through
// the caller-facing query, saw MaxLimit rows, and deleted the permissions of
// everything past it. allOperatorOwners exists to be immune to that cap.
func TestIntegrationValidateOperatorPermissionsBeyondMaxLimit(t *testing.T) {
	repo, coll := testRepo(t)
	const total = MaxLimit + 1

	docs := make([]any, 0, total)
	version := int64(1)
	for i := range total {
		docs = append(docs, lib.Operator{
			Name:        fmt.Sprintf("operator-%04d", i),
			UserId:      fmt.Sprintf("user-%04d", i),
			Version:     &version,
			DateCreated: time.Now(),
			DateUpdated: time.Now(),
		})
	}
	if _, err := coll.InsertMany(context.Background(), docs); err != nil {
		t.Fatalf("bulk insert: %v", err)
	}

	if err := repo.ValidateOperatorPermissions(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	resources, err, _ := repo.perm.ListResourcesWithAdminPermission(
		permV2Client.InternalAdminToken, PermV2InstanceTopic, permV2Client.ListOptions{})
	if err != nil {
		t.Fatalf("list permission resources: %v", err)
	}
	if len(resources) != total {
		t.Fatalf("%d permission entries for %d operators — the cap leaked into reconciliation", len(resources), total)
	}

	// Spot-check that the owner, not just any user, was granted the rights.
	var last lib.Operator
	if err = coll.FindOne(context.Background(), bson.M{"name": fmt.Sprintf("operator-%04d", total-1)}).Decode(&last); err != nil {
		t.Fatalf("read last operator: %v", err)
	}
	ok, err, _ := repo.perm.CheckPermission(userToken(t, last.UserId), PermV2InstanceTopic, last.Id.Hex(), permV2Client.Administrate)
	if err != nil {
		t.Fatalf("check permission: %v", err)
	}
	if !ok {
		t.Error("the owner of the last operator holds no rights on it")
	}
}

func TestIntegrationValidateOperatorPermissionsRemovesOrphans(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha")

	orphan := bson.NewObjectID().Hex()
	perms := permV2Client.ResourcePermissions{
		UserPermissions:  map[string]permV2Client.PermissionsMap{},
		GroupPermissions: map[string]permV2Client.PermissionsMap{},
	}
	SetDefaultPermissions(lib.Operator{UserId: "user-a"}, perms)
	if _, err, _ := repo.perm.SetPermission(permV2Client.InternalAdminToken, PermV2InstanceTopic, orphan, perms); err != nil {
		t.Fatalf("plant orphan: %v", err)
	}

	if err := repo.ValidateOperatorPermissions(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	resources, err, _ := repo.perm.ListResourcesWithAdminPermission(
		permV2Client.InternalAdminToken, PermV2InstanceTopic, permV2Client.ListOptions{})
	if err != nil {
		t.Fatalf("list permission resources: %v", err)
	}
	alpha := idOf(t, coll, "alpha")
	for _, r := range resources {
		if r.Id == orphan {
			t.Error("the permission entry without an operator was kept")
		}
	}
	found := false
	for _, r := range resources {
		if r.Id == alpha {
			found = true
		}
	}
	if !found {
		t.Error("the entry of an existing operator was removed")
	}
}

func TestIntegrationNewPingsAndCreatesIndexes(t *testing.T) {
	// Uses New rather than testCollection, because the indexes and the ping are
	// what is under test here. The shared client only decides whether to skip.
	requireMongo(t)

	dbName := testDatabaseName(t)
	database, err := New(testMongoURL(), dbName)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if err := database.client.Database(dbName).Drop(dropCtx); err != nil {
			t.Errorf("drop %s: %v", dbName, err)
		}
		database.Disconnect()
	})

	cur, err := database.OperatorCollection().Indexes().List(context.Background())
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	var idx []struct {
		Name string `bson:"name"`
	}
	if err = cur.All(context.Background(), &idx); err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	have := map[string]bool{}
	for _, i := range idx {
		have[i.Name] = true
	}
	// The two access patterns All relies on: the sort and the owner term.
	for _, want := range []string{"name_1", "userId_1"} {
		if !have[want] {
			t.Errorf("index %s is missing, got %v", want, idx)
		}
	}

	// Running it again must not fail — the service creates the indexes on every
	// start.
	second, err := New(testMongoURL(), dbName)
	if err != nil {
		t.Fatalf("New on an existing database: %v", err)
	}
	second.Disconnect()
}

func TestIntegrationNewFailsOnUnreachableDatabase(t *testing.T) {
	// mongo.Connect is lazy, so this only fails because New pings. Without it an
	// unreachable database surfaced on the first request, after the service had
	// already logged itself as connected.
	//
	// The address carries serverSelectionTimeoutMS because New builds the URI
	// from it verbatim: the driver otherwise retries until the 10s deadline of
	// getTimeoutContext, which is right in production and ten wasted seconds in
	// every test run. The code path under test is unchanged.
	if _, err := New("127.0.0.1:1/?serverSelectionTimeoutMS=200", "operators_test_unreachable"); err == nil {
		t.Error("New accepted an address with nothing listening")
	}
}
