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
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	permV2Model "github.com/SENERGY-Platform/permissions-v2/pkg/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestParseQueryInt(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{name: "zero", raw: "0", want: 0},
		{name: "positive", raw: "42", want: 42},
		{name: "at the cap", raw: "1000", want: MaxLimit},
		{name: "negative", raw: "-1", wantErr: true},
		{name: "not a number", raw: "abc", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
		{name: "float", raw: "1.5", wantErr: true},
		{name: "leading plus", raw: "+5", want: 5},
		{name: "whitespace", raw: " 5", wantErr: true},
		{name: "int64 overflow", raw: "99999999999999999999", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseQueryInt("limit", tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseQueryInt(%q) = %d, want an error", tc.raw, got)
				}
				// A rejected parameter must reach the client as 400, which the
				// api package decides from this sentinel alone.
				if !errors.Is(err, lib.ErrInvalidInput) {
					t.Errorf("error %v does not wrap ErrInvalidInput", err)
				}
				// The name is what tells the caller which parameter to correct.
				if !strings.Contains(err.Error(), "limit") {
					t.Errorf("error %q does not name the parameter", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseQueryInt(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// TestParseQueryIntAcceptsZero pins the contract user-management depends on:
// limit=0 must survive parsing, because All passes it to the driver where it
// means "no limit". Clamping it to MaxLimit would make the deletion of a user
// with more than MaxLimit operators silently miss the rest.
func TestParseQueryIntAcceptsZero(t *testing.T) {
	got, err := parseQueryInt("limit", "0")
	if err != nil {
		t.Fatalf("limit=0 was rejected: %v", err)
	}
	if got != 0 {
		t.Fatalf("limit=0 parsed as %d — it must stay 0 to mean unlimited", got)
	}
}

func TestObjectID(t *testing.T) {
	valid := bson.NewObjectID()
	got, err := objectID(valid.Hex())
	if err != nil {
		t.Fatalf("unexpected error for a valid id: %v", err)
	}
	if got != valid {
		t.Errorf("objectID round trip = %v, want %v", got, valid)
	}

	for _, raw := range []string{"", "zzz", "not-an-object-id", valid.Hex() + "00", strings.ToUpper(valid.Hex())[:23]} {
		t.Run("rejects "+raw, func(t *testing.T) {
			_, err := objectID(raw)
			if err == nil {
				t.Fatalf("objectID(%q) accepted a malformed id", raw)
			}
			if !errors.Is(err, lib.ErrInvalidInput) {
				t.Errorf("error %v does not wrap ErrInvalidInput", err)
			}
			// The driver's own message would reach the client through
			// ErrorHandler, so it must not be carried along.
			if strings.Contains(err.Error(), "hex") {
				t.Errorf("driver message leaked into %q", err)
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	if got := notFound(mongo.ErrNoDocuments); !errors.Is(got, lib.ErrNotFound) {
		t.Errorf("notFound(ErrNoDocuments) = %v, want ErrNotFound", got)
	}
	// FindOne errors arrive wrapped often enough that identity comparison is not
	// enough here.
	wrapped := fmt.Errorf("decode operator: %w", mongo.ErrNoDocuments)
	if got := notFound(wrapped); !errors.Is(got, lib.ErrNotFound) {
		t.Errorf("notFound(wrapped ErrNoDocuments) = %v, want ErrNotFound", got)
	}
	// Everything else has to pass through untouched: mapping an unrelated
	// failure onto 404 would report a broken database as an empty result.
	other := errors.New("connection reset by peer")
	if got := notFound(other); !errors.Is(got, other) {
		t.Errorf("notFound(%v) = %v, want it unchanged", other, got)
	}
	if errors.Is(notFound(other), lib.ErrNotFound) {
		t.Error("an unrelated error was mapped onto ErrNotFound")
	}
}

func full() permV2Model.PermissionsMap {
	return permV2Model.PermissionsMap{Read: true, Write: true, Execute: true, Administrate: true}
}

func resourceWith(perms map[string]permV2Model.PermissionsMap) permV2Client.Resource {
	return permV2Client.Resource{
		Id:                  "operator-1",
		ResourcePermissions: permV2Client.ResourcePermissions{UserPermissions: perms},
	}
}

func TestOwnerHasFullPermissions(t *testing.T) {
	tests := []struct {
		name     string
		resource permV2Client.Resource
		userId   string
		want     bool
	}{
		{
			name:     "owner holds everything",
			resource: resourceWith(map[string]permV2Model.PermissionsMap{"user-a": full()}),
			userId:   "user-a",
			want:     true,
		},
		{
			name:     "owner absent",
			resource: resourceWith(map[string]permV2Model.PermissionsMap{"user-b": full()}),
			userId:   "user-a",
			want:     false,
		},
		{
			name:     "no user permissions at all",
			resource: resourceWith(map[string]permV2Model.PermissionsMap{}),
			userId:   "user-a",
			want:     false,
		},
		{
			name:     "nil map",
			resource: resourceWith(nil),
			userId:   "user-a",
			want:     false,
		},
		{
			// Every single missing flag has to force the write, otherwise
			// reconciliation would leave a partially stripped owner as it is.
			name:     "administrate missing",
			resource: resourceWith(map[string]permV2Model.PermissionsMap{"user-a": {Read: true, Write: true, Execute: true}}),
			userId:   "user-a",
			want:     false,
		},
		{
			name:     "execute missing",
			resource: resourceWith(map[string]permV2Model.PermissionsMap{"user-a": {Read: true, Write: true, Administrate: true}}),
			userId:   "user-a",
			want:     false,
		},
		{
			name:     "write missing",
			resource: resourceWith(map[string]permV2Model.PermissionsMap{"user-a": {Read: true, Execute: true, Administrate: true}}),
			userId:   "user-a",
			want:     false,
		},
		{
			name:     "read missing",
			resource: resourceWith(map[string]permV2Model.PermissionsMap{"user-a": {Write: true, Execute: true, Administrate: true}}),
			userId:   "user-a",
			want:     false,
		},
		{
			name:     "empty owner id against an empty entry",
			resource: resourceWith(map[string]permV2Model.PermissionsMap{"": {}}),
			userId:   "",
			want:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ownerHasFullPermissions(tc.resource, tc.userId); got != tc.want {
				t.Errorf("ownerHasFullPermissions = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSetDefaultPermissions(t *testing.T) {
	perms := permV2Client.ResourcePermissions{
		UserPermissions:  map[string]permV2Client.PermissionsMap{},
		GroupPermissions: map[string]permV2Client.PermissionsMap{},
		RolePermissions:  map[string]permV2Model.PermissionsMap{},
	}
	SetDefaultPermissions(lib.Operator{UserId: "user-a"}, perms)

	// The parameter is passed by value, so the mutation is only visible because
	// the map header is shared — a caller that hands over a nil map gets a panic
	// instead. Both call sites initialise it; this pins that expectation.
	got, ok := perms.UserPermissions["user-a"]
	if !ok {
		t.Fatal("owner did not receive an entry")
	}
	if got != full() {
		t.Errorf("owner permissions = %+v, want all four set", got)
	}

	// An entry for someone else must survive, otherwise reconciliation would
	// strip shared access on every restart.
	perms.UserPermissions["user-b"] = permV2Client.PermissionsMap{Read: true}
	SetDefaultPermissions(lib.Operator{UserId: "user-a"}, perms)
	if other := perms.UserPermissions["user-b"]; other != (permV2Client.PermissionsMap{Read: true}) {
		t.Errorf("permissions of another user were changed: %+v", other)
	}
}

// TestOwnerHasFullPermissionsAgreesWithSetDefaultPermissions ties the two
// together: the skip in ValidateOperatorPermissions is only correct while it
// checks exactly what the write would have granted. If SetDefaultPermissions
// ever grants more, the skip starts hiding a missing permission.
func TestOwnerHasFullPermissionsAgreesWithSetDefaultPermissions(t *testing.T) {
	perms := permV2Client.ResourcePermissions{
		UserPermissions: map[string]permV2Client.PermissionsMap{},
	}
	SetDefaultPermissions(lib.Operator{UserId: "user-a"}, perms)
	if !ownerHasFullPermissions(resourceWith(perms.UserPermissions), "user-a") {
		t.Error("what SetDefaultPermissions writes is not recognised as complete")
	}
}
