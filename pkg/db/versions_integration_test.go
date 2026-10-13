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
	"errors"
	"slices"
	"testing"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func versionOf(label, image string, in ...lib.Value) lib.OperatorVersion {
	return lib.OperatorVersion{
		Version: label,
		Image:   image,
		Inputs:  in,
		Outputs: []lib.Value{{Name: "out-" + label, Type: "string"}},
		Config:  []lib.Value{{Name: "cfg-" + label, Type: "string"}},
	}
}

func readOperator(t *testing.T, repo *MongoRepo, id string, userId string) lib.Operator {
	t.Helper()
	op, err := repo.FindOperator(t.Context(), id, userToken(t, userId))
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return op
}

func versionLabels(op lib.Operator) []string {
	out := make([]string, 0, len(op.Versions))
	for _, v := range op.Versions {
		out = append(out, v.Version)
	}
	return out
}

// requireTopLevel checks that the top level carries the image, ports and config of v.
func requireTopLevel(t *testing.T, op lib.Operator, v lib.OperatorVersion) {
	t.Helper()
	if op.Image != v.Image || !slices.Equal(op.Inputs, v.Inputs) || !slices.Equal(op.Outputs, v.Outputs) || !slices.Equal(op.Config, v.Config) {
		t.Errorf("top level = {%q %v %v %v}, want the values of %s {%q %v %v %v}",
			op.Image, op.Inputs, op.Outputs, op.Config, v.Version, v.Image, v.Inputs, v.Outputs, v.Config)
	}
}

func requireRevision(t *testing.T, op lib.Operator, want int64) {
	t.Helper()
	if op.Version == nil || *op.Version != want {
		t.Errorf("revision = %v, want %d", op.Version, want)
	}
}

func TestIntegrationPutOperatorVersion(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha")
	id := idOf(t, coll, "alpha")
	owner := userToken(t, "user-a")
	in := lib.Value{Name: "in", Type: "string"}

	v1 := versionOf("v1.0.0", "img:1", in)
	v2 := versionOf("v1.1.0", "img:2", in)
	hotfix := versionOf("v0.9.0", "img:0", in)

	t.Run("the first version converts the operator and derives the top level", func(t *testing.T) {
		created, err := repo.PutOperatorVersion(t.Context(), id, v1, owner)
		if err != nil || !created {
			t.Fatalf("created = %v, err = %v", created, err)
		}
		op := readOperator(t, repo, id, "user-a")
		requireTopLevel(t, op, v1)
		requireRevision(t, op, 2)
		if got := versionLabels(op); !slices.Equal(got, []string{"v1.0.0"}) {
			t.Errorf("versions = %v", got)
		}
		if op.Name != "alpha" || op.UserId != "user-a" {
			t.Errorf("metadata changed: name %q, user %q", op.Name, op.UserId)
		}
	})

	t.Run("a higher version moves the top level", func(t *testing.T) {
		if _, err := repo.PutOperatorVersion(t.Context(), id, v2, owner); err != nil {
			t.Fatalf("put: %v", err)
		}
		op := readOperator(t, repo, id, "user-a")
		requireTopLevel(t, op, v2)
		requireRevision(t, op, 3)
	})

	t.Run("a lower label is stored in order and does not move the top level", func(t *testing.T) {
		if _, err := repo.PutOperatorVersion(t.Context(), id, hotfix, owner); err != nil {
			t.Fatalf("put: %v", err)
		}
		op := readOperator(t, repo, id, "user-a")
		requireTopLevel(t, op, v2)
		requireRevision(t, op, 4)
		if got := versionLabels(op); !slices.Equal(got, []string{"v0.9.0", "v1.0.0", "v1.1.0"}) {
			t.Errorf("versions = %v", got)
		}
	})

	t.Run("deprecating the newest falls back to the next one", func(t *testing.T) {
		deprecated := v2
		deprecated.Deprecated = true
		created, err := repo.PutOperatorVersion(t.Context(), id, deprecated, owner)
		if err != nil || created {
			t.Fatalf("created = %v, err = %v, want a replacement", created, err)
		}
		op := readOperator(t, repo, id, "user-a")
		requireTopLevel(t, op, v1)
		requireRevision(t, op, 5)
		if len(op.Versions) != 3 || !op.Versions[2].Deprecated {
			t.Errorf("versions = %+v", op.Versions)
		}
	})

	t.Run("a refused version changes nothing", func(t *testing.T) {
		before := readOperator(t, repo, id, "user-a")
		dup := versionOf("v2.0.0", "img:1", in)
		if _, err := repo.PutOperatorVersion(t.Context(), id, dup, owner); !errors.Is(err, lib.ErrInvalidInput) {
			t.Fatalf("error = %v, want ErrInvalidInput", err)
		}
		after := readOperator(t, repo, id, "user-a")
		requireRevision(t, after, *before.Version)
		if !slices.Equal(versionLabels(after), versionLabels(before)) {
			t.Errorf("versions = %v, want %v", versionLabels(after), versionLabels(before))
		}
	})

	t.Run("another user is refused and changes nothing", func(t *testing.T) {
		before := readOperator(t, repo, id, "user-a")
		_, err := repo.PutOperatorVersion(t.Context(), id, versionOf("v3.0.0", "img:3"), userToken(t, "user-b"))
		if !errors.Is(err, lib.ErrMissingRights) {
			t.Fatalf("error = %v, want ErrMissingRights", err)
		}
		after := readOperator(t, repo, id, "user-a")
		requireRevision(t, after, *before.Version)
		if len(after.Versions) != len(before.Versions) {
			t.Error("the refused put was applied anyway")
		}
	})

	t.Run("a stale revision is a conflict and writes nothing", func(t *testing.T) {
		stale := readOperator(t, repo, id, "user-a")
		if _, err := repo.PutOperatorVersion(t.Context(), id, versionOf("v1.2.0", "img:4", in), owner); err != nil {
			t.Fatalf("put: %v", err)
		}
		current := readOperator(t, repo, id, "user-a")

		_, err := repo.applyVersionPut(t.Context(), mustObjectID(t, id), stale, versionOf("v1.3.0", "img:5", in))

		if !errors.Is(err, lib.ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
		after := readOperator(t, repo, id, "user-a")
		requireRevision(t, after, *current.Version)
		if !slices.Equal(versionLabels(after), versionLabels(current)) {
			t.Errorf("versions = %v, want %v", versionLabels(after), versionLabels(current))
		}
	})

	t.Run("rights held but document gone is a 404", func(t *testing.T) {
		if _, err := coll.DeleteOne(context.Background(), bson.M{"_id": mustObjectID(t, id)}); err != nil {
			t.Fatalf("delete document: %v", err)
		}
		_, err := repo.PutOperatorVersion(t.Context(), id, v1, owner)
		if !errors.Is(err, lib.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	})
}

func TestIntegrationUpdateVersionedOperator(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha")
	id := idOf(t, coll, "alpha")
	owner := userToken(t, "user-a")
	in := lib.Value{Name: "in", Type: "string"}
	v1 := versionOf("v1.0.0", "img:1", in)
	if _, err := repo.PutOperatorVersion(t.Context(), id, v1, owner); err != nil {
		t.Fatalf("put: %v", err)
	}

	t.Run("an update with matching image and ports changes the metadata", func(t *testing.T) {
		body := lib.Operator{Name: "alpha renamed", Description: "d", Image: v1.Image, Inputs: v1.Inputs, Outputs: v1.Outputs, Config: v1.Config}
		if err := repo.UpdateOperator(t.Context(), id, body, owner); err != nil {
			t.Fatalf("update: %v", err)
		}
		op := readOperator(t, repo, id, "user-a")
		if op.Name != "alpha renamed" || op.Description != "d" {
			t.Errorf("name %q, description %q", op.Name, op.Description)
		}
		requireTopLevel(t, op, v1)
		requireRevision(t, op, 3)
		if len(op.Versions) != 1 {
			t.Errorf("versions = %v", versionLabels(op))
		}
	})

	t.Run("an update with empty image and ports keeps the top level", func(t *testing.T) {
		if err := repo.UpdateOperator(t.Context(), id, lib.Operator{Name: "alpha again"}, owner); err != nil {
			t.Fatalf("update: %v", err)
		}
		op := readOperator(t, repo, id, "user-a")
		if op.Name != "alpha again" {
			t.Errorf("name = %q", op.Name)
		}
		requireTopLevel(t, op, v1)
		requireRevision(t, op, 4)
	})

	t.Run("the versions in the body are ignored", func(t *testing.T) {
		body := lib.Operator{Name: "alpha again", Versions: []lib.OperatorVersion{versionOf("v9.0.0", "img:9")}}
		if err := repo.UpdateOperator(t.Context(), id, body, owner); err != nil {
			t.Fatalf("update: %v", err)
		}
		op := readOperator(t, repo, id, "user-a")
		if got := versionLabels(op); !slices.Equal(got, []string{"v1.0.0"}) {
			t.Errorf("versions = %v, want only v1.0.0", got)
		}
		requireTopLevel(t, op, v1)
	})

	t.Run("a differing image is refused and changes nothing", func(t *testing.T) {
		before := readOperator(t, repo, id, "user-a")
		err := repo.UpdateOperator(t.Context(), id, lib.Operator{Name: "changed", Image: "img:other"}, owner)
		if !errors.Is(err, lib.ErrInvalidInput) {
			t.Fatalf("error = %v, want ErrInvalidInput", err)
		}
		after := readOperator(t, repo, id, "user-a")
		if after.Name != before.Name || after.Image != before.Image {
			t.Errorf("name %q, image %q: the refused update was applied", after.Name, after.Image)
		}
		requireRevision(t, after, *before.Version)
	})

	t.Run("differing inputs are refused", func(t *testing.T) {
		err := repo.UpdateOperator(t.Context(), id, lib.Operator{Name: "changed", Inputs: []lib.Value{{Name: "x", Type: "float"}}}, owner)
		if !errors.Is(err, lib.ErrInvalidInput) {
			t.Errorf("error = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("another user is refused", func(t *testing.T) {
		err := repo.UpdateOperator(t.Context(), id, lib.Operator{Name: "hijacked"}, userToken(t, "user-b"))
		if !errors.Is(err, lib.ErrMissingRights) {
			t.Errorf("error = %v, want ErrMissingRights", err)
		}
	})
}

func TestIntegrationUpdateWithStaleRevisionIsAConflict(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha")
	id := idOf(t, coll, "alpha")
	owner := userToken(t, "user-a")
	if _, err := repo.PutOperatorVersion(t.Context(), id, versionOf("v1.0.0", "img:1"), owner); err != nil {
		t.Fatalf("put: %v", err)
	}
	stale := readOperator(t, repo, id, "user-a")
	if err := repo.UpdateOperator(t.Context(), id, lib.Operator{Name: "first"}, owner); err != nil {
		t.Fatalf("update: %v", err)
	}

	err := repo.applyUpdate(t.Context(), mustObjectID(t, id), stale, lib.Operator{Name: "second"})

	if !errors.Is(err, lib.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	op := readOperator(t, repo, id, "user-a")
	if op.Name != "first" {
		t.Errorf("name = %q, want first: the stale write went through", op.Name)
	}
	requireRevision(t, op, 3)
}

func TestIntegrationUpdateWithoutVersionsLastWriteWins(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha")
	id := idOf(t, coll, "alpha")
	owner := userToken(t, "user-a")
	stale := readOperator(t, repo, id, "user-a")
	if err := repo.UpdateOperator(t.Context(), id, lib.Operator{Name: "first"}, owner); err != nil {
		t.Fatalf("update: %v", err)
	}

	t.Run("a stale read still writes, as before versions", func(t *testing.T) {
		if err := repo.applyUpdate(t.Context(), mustObjectID(t, id), stale, lib.Operator{Name: "second"}); err != nil {
			t.Fatalf("error = %v, want the write to go through", err)
		}
		op := readOperator(t, repo, id, "user-a")
		if op.Name != "second" {
			t.Errorf("name = %q, want second", op.Name)
		}
		requireRevision(t, op, 3)
	})

	t.Run("a version added since the read is a conflict", func(t *testing.T) {
		if _, err := repo.PutOperatorVersion(t.Context(), id, versionOf("v1.0.0", "img:1"), owner); err != nil {
			t.Fatalf("put: %v", err)
		}
		err := repo.applyUpdate(t.Context(), mustObjectID(t, id), stale, lib.Operator{Name: "third", Image: "img:other"})
		if !errors.Is(err, lib.ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
		if op := readOperator(t, repo, id, "user-a"); op.Image != "img:1" {
			t.Errorf("image = %q, want img:1 from the version", op.Image)
		}
	})
}

func TestIntegrationUpdateOfAnOperatorDeletedSinceTheReadIsNotFound(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha")
	id := idOf(t, coll, "alpha")
	stale := readOperator(t, repo, id, "user-a")
	if _, err := coll.DeleteOne(context.Background(), bson.M{"_id": mustObjectID(t, id)}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := repo.applyUpdate(t.Context(), mustObjectID(t, id), stale, lib.Operator{Name: "x"}); !errors.Is(err, lib.ErrNotFound) {
		t.Errorf("update: error = %v, want ErrNotFound", err)
	}
	if _, err := repo.applyVersionPut(t.Context(), mustObjectID(t, id), stale, versionOf("v1.0.0", "img:1")); !errors.Is(err, lib.ErrNotFound) {
		t.Errorf("version put: error = %v, want ErrNotFound", err)
	}
}

func TestIntegrationUpdateOfAnOperatorWithoutARevision(t *testing.T) {
	repo, coll := testRepo(t)
	// A document from before the revision counter: the field is missing, not null.
	res, err := coll.InsertOne(context.Background(), bson.M{"name": "old", "userId": "user-a"})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id := res.InsertedID.(bson.ObjectID).Hex()
	owner := userToken(t, "user-a")
	// Reconciliation grants the owner of every stored document its permissions.
	if err = repo.ValidateOperatorPermissions(t.Context()); err != nil {
		t.Fatalf("validate permissions: %v", err)
	}

	if err = repo.UpdateOperator(t.Context(), id, lib.Operator{Name: "old renamed"}, owner); err != nil {
		t.Fatalf("update: %v", err)
	}

	op := readOperator(t, repo, id, "user-a")
	if op.Name != "old renamed" {
		t.Errorf("name = %q", op.Name)
	}
	requireRevision(t, op, 1)
}

func TestIntegrationVersionsInTheBodyAreIgnoredOnInsertAndUpdate(t *testing.T) {
	repo, coll := testRepo(t)
	body := lib.Operator{Name: "alpha", UserId: "user-a", Image: "img:top", Versions: []lib.OperatorVersion{versionOf("v1.0.0", "img:1")}}
	if _, err := repo.InsertOperator(t.Context(), body); err != nil {
		t.Fatalf("insert: %v", err)
	}
	id := idOf(t, coll, "alpha")
	owner := userToken(t, "user-a")

	op := readOperator(t, repo, id, "user-a")
	if len(op.Versions) != 0 || op.Image != "img:top" {
		t.Fatalf("after insert: versions %v, image %q", versionLabels(op), op.Image)
	}

	update := lib.Operator{Name: "alpha", Image: "img:top2", Versions: []lib.OperatorVersion{versionOf("v2.0.0", "img:2")}}
	if err := repo.UpdateOperator(t.Context(), id, update, owner); err != nil {
		t.Fatalf("update: %v", err)
	}
	op = readOperator(t, repo, id, "user-a")
	if len(op.Versions) != 0 || op.Image != "img:top2" {
		t.Errorf("after update: versions %v, image %q, want none and the body's image", versionLabels(op), op.Image)
	}
}

// noUse is a check that lets every removal through and counts its calls.
func noUse(calls *[]string) func(lib.OperatorVersion) error {
	return func(v lib.OperatorVersion) error {
		*calls = append(*calls, v.Version+" "+v.Image)
		return nil
	}
}

func TestIntegrationDeleteOperatorVersion(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha")
	id := idOf(t, coll, "alpha")
	owner := userToken(t, "user-a")
	in := lib.Value{Name: "in", Type: "string"}
	v1 := versionOf("v1.0.0", "img:1", in)
	v2 := versionOf("v1.1.0", "img:2", in)
	v3 := versionOf("v2.0.0", "img:3", in)
	for _, v := range []lib.OperatorVersion{v1, v2, v3} {
		if _, err := repo.PutOperatorVersion(t.Context(), id, v, owner); err != nil {
			t.Fatalf("put %s: %v", v.Version, err)
		}
	}

	refusals := []struct {
		name  string
		label string
		auth  string
		want  error
	}{
		{"another user, before the check", "v1.0.0", userToken(t, "user-b"), lib.ErrMissingRights},
		{"an unknown label", "v1.2.0", owner, lib.ErrVersionNotFound},
	}
	for _, tc := range refusals {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			before := readOperator(t, repo, id, "user-a")
			var calls []string
			err := repo.DeleteOperatorVersion(t.Context(), id, tc.label, tc.auth, noUse(&calls))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if len(calls) != 0 {
				t.Errorf("check called with %v", calls)
			}
			after := readOperator(t, repo, id, "user-a")
			requireRevision(t, after, *before.Version)
		})
	}

	t.Run("a refusing check writes nothing", func(t *testing.T) {
		before := readOperator(t, repo, id, "user-a")
		inUse := &lib.InUseError{Version: "v2.0.0"}
		err := repo.DeleteOperatorVersion(t.Context(), id, "v2.0.0", owner, func(lib.OperatorVersion) error { return inUse })
		if !errors.Is(err, inUse) {
			t.Fatalf("error = %v, want the check's", err)
		}
		after := readOperator(t, repo, id, "user-a")
		requireRevision(t, after, *before.Version)
		if !slices.Equal(versionLabels(after), []string{"v1.0.0", "v1.1.0", "v2.0.0"}) {
			t.Errorf("versions = %v", versionLabels(after))
		}
	})

	t.Run("removing the newest moves the top level to the next", func(t *testing.T) {
		before := readOperator(t, repo, id, "user-a")
		requireTopLevel(t, before, v3)
		var calls []string
		if err := repo.DeleteOperatorVersion(t.Context(), id, "v2.0.0", owner, noUse(&calls)); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if !slices.Equal(calls, []string{"v2.0.0 img:3"}) {
			t.Errorf("check called with %v, want the removed version and its image", calls)
		}
		after := readOperator(t, repo, id, "user-a")
		requireTopLevel(t, after, v2)
		requireRevision(t, after, *before.Version+1)
		if !slices.Equal(versionLabels(after), []string{"v1.0.0", "v1.1.0"}) {
			t.Errorf("versions = %v", versionLabels(after))
		}
	})

	t.Run("removing an older one keeps the top level", func(t *testing.T) {
		var calls []string
		if err := repo.DeleteOperatorVersion(t.Context(), id, "v1.0.0", owner, noUse(&calls)); err != nil {
			t.Fatalf("delete: %v", err)
		}
		after := readOperator(t, repo, id, "user-a")
		requireTopLevel(t, after, v2)
		if !slices.Equal(versionLabels(after), []string{"v1.1.0"}) {
			t.Errorf("versions = %v", versionLabels(after))
		}
	})

	t.Run("the only version is refused before the check", func(t *testing.T) {
		var calls []string
		err := repo.DeleteOperatorVersion(t.Context(), id, "v1.1.0", owner, noUse(&calls))
		if !errors.Is(err, lib.ErrInvalidInput) {
			t.Fatalf("error = %v, want ErrInvalidInput", err)
		}
		if len(calls) != 0 {
			t.Errorf("check called with %v", calls)
		}
		if got := versionLabels(readOperator(t, repo, id, "user-a")); !slices.Equal(got, []string{"v1.1.0"}) {
			t.Errorf("versions = %v", got)
		}
	})

	t.Run("a removed image can be used by a new version again", func(t *testing.T) {
		if _, err := repo.PutOperatorVersion(t.Context(), id, versionOf("v3.0.0", "img:3", in), owner); err != nil {
			t.Errorf("put with the image of the removed version: %v", err)
		}
	})

	t.Run("rights held but document gone is a 404", func(t *testing.T) {
		if _, err := coll.DeleteOne(context.Background(), bson.M{"_id": mustObjectID(t, id)}); err != nil {
			t.Fatalf("delete document: %v", err)
		}
		var calls []string
		if err := repo.DeleteOperatorVersion(t.Context(), id, "v1.1.0", owner, noUse(&calls)); !errors.Is(err, lib.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	})
}

// Two removals that each leave one version must not both go through and leave none.
func TestIntegrationConcurrentVersionDeletesKeepOneVersion(t *testing.T) {
	repo, coll := testRepo(t)
	insert(t, repo, "user-a", "alpha")
	id := idOf(t, coll, "alpha")
	owner := userToken(t, "user-a")
	v1 := versionOf("v1.0.0", "img:1")
	v2 := versionOf("v1.1.0", "img:2")
	for _, v := range []lib.OperatorVersion{v1, v2} {
		if _, err := repo.PutOperatorVersion(t.Context(), id, v, owner); err != nil {
			t.Fatalf("put %s: %v", v.Version, err)
		}
	}
	stale := readOperator(t, repo, id, "user-a")
	var calls []string
	if err := repo.DeleteOperatorVersion(t.Context(), id, "v1.1.0", owner, noUse(&calls)); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	current := readOperator(t, repo, id, "user-a")

	err := repo.applyVersionDelete(t.Context(), mustObjectID(t, id), stale, "v1.0.0", noUse(&calls))

	if !errors.Is(err, lib.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	after := readOperator(t, repo, id, "user-a")
	requireRevision(t, after, *current.Version)
	requireTopLevel(t, after, v1)
	if !slices.Equal(versionLabels(after), []string{"v1.0.0"}) {
		t.Errorf("versions = %v, want v1.0.0 left", versionLabels(after))
	}

	if _, err = coll.DeleteOne(context.Background(), bson.M{"_id": mustObjectID(t, id)}); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if err = repo.applyVersionDelete(t.Context(), mustObjectID(t, id), stale, "v1.0.0", noUse(&calls)); !errors.Is(err, lib.ErrNotFound) {
		t.Errorf("after the operator was deleted: error = %v, want ErrNotFound", err)
	}
}
