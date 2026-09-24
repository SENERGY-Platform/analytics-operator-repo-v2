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
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestClientOptionsSetsAuthWhenUserGiven(t *testing.T) {
	opts := clientOptions(Config{
		URL:        "mongodb://mongo.example:27017",
		User:       "analytics-operator-repo",
		Password:   "s3cret",
		AuthSource: "admin",
	})
	if err := opts.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if opts.Auth == nil {
		t.Fatal("Auth = nil, want a credential")
	}
	if opts.Auth.Username != "analytics-operator-repo" || opts.Auth.Password != "s3cret" || opts.Auth.AuthSource != "admin" {
		t.Errorf("Auth = {Username:%q Password:%q AuthSource:%q}, want the configured values",
			opts.Auth.Username, opts.Auth.Password, opts.Auth.AuthSource)
	}
}

func TestClientOptionsNoAuthWithoutUser(t *testing.T) {
	// A password alone must not produce a credential with an empty user name.
	opts := clientOptions(Config{
		URL:        "mongodb://mongo.example:27017",
		Password:   "s3cret",
		AuthSource: "admin",
	})
	if opts.Auth != nil {
		t.Errorf("Auth = %+v, want nil when no user is configured", *opts.Auth)
	}
}

func TestClientOptionsUserOverridesURLCredentials(t *testing.T) {
	opts := clientOptions(Config{
		URL:        "mongodb://fromurl:urlpw@mongo.example:27017/?authSource=other",
		User:       "fromconfig",
		Password:   "configpw",
		AuthSource: "admin",
	})
	if opts.Auth == nil || opts.Auth.Username != "fromconfig" || opts.Auth.Password != "configpw" || opts.Auth.AuthSource != "admin" {
		t.Errorf("Auth = %+v, want the configured credential to replace the URL's", opts.Auth)
	}
}

func TestClientOptionsPassesURLUnchanged(t *testing.T) {
	url := "mongodb://a.example:27017,b.example:27017/?replicaSet=rs0&serverSelectionTimeoutMS=500"
	opts := clientOptions(Config{URL: url})
	if err := opts.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := opts.GetURI(); got != url {
		t.Errorf("GetURI() = %q, want %q", got, url)
	}
	if want := []string{"a.example:27017", "b.example:27017"}; !slices.Equal(opts.Hosts, want) {
		t.Errorf("Hosts = %v, want %v", opts.Hosts, want)
	}
	if opts.ReplicaSet == nil || *opts.ReplicaSet != "rs0" {
		t.Errorf("ReplicaSet = %v, want rs0", opts.ReplicaSet)
	}
	if opts.Monitor == nil {
		t.Error("Monitor = nil, want the tracing command monitor")
	}
}

func TestClientOptionsRejectsURLWithoutScheme(t *testing.T) {
	// The scheme is no longer prepended, so the old host:port form must fail
	// at startup rather than be patched up.
	if err := clientOptions(Config{URL: "localhost:27017"}).Validate(); err == nil {
		t.Error("a URL without scheme was accepted")
	}
}

func TestNewRejectsEmptyDatabase(t *testing.T) {
	// Fails before connecting, so no server is needed.
	_, err := New(Config{URL: "mongodb://127.0.0.1:1", Database: ""})
	if err == nil || !strings.Contains(err.Error(), "MONGO_DATABASE") {
		t.Errorf("New = %v, want an error naming MONGO_DATABASE", err)
	}
}

func TestNewRejectsUserWithoutPassword(t *testing.T) {
	_, err := New(Config{URL: "mongodb://127.0.0.1:1", User: "analytics-operator-repo", Database: "analytics_operator_repo"})
	if err == nil || !strings.Contains(err.Error(), "MONGO_PASSWORD") {
		t.Errorf("New = %v, want an error naming MONGO_PASSWORD", err)
	}
}

func TestConfigValidateAcceptsNoAuthAndFullAuth(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no auth":   {Database: "analytics_operator_repo"},
		"full auth": {User: "u", Password: "p", Database: "analytics_operator_repo"},
	} {
		if err := cfg.validate(); err != nil {
			t.Errorf("%s: validate = %v, want nil", name, err)
		}
	}
}

func TestOpenDisconnectsClientWhenPingFails(t *testing.T) {
	// Nothing listens on port 1; the short selection timeout keeps the ping fast.
	client, err := mongo.Connect(clientOptions(Config{URL: "mongodb://127.0.0.1:1/?serverSelectionTimeoutMS=200"}))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err = open(client, "", "operators_test_unreachable"); err == nil {
		t.Fatal("open accepted an address with nothing listening")
	}
	assertDisconnected(t, client)
}

// assertDisconnected relies on the driver refusing any operation on a
// disconnected client with ErrClientDisconnected, without contacting a server.
func assertDisconnected(t *testing.T, client *mongo.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx, nil); !errors.Is(err, mongo.ErrClientDisconnected) {
		_ = client.Disconnect(context.Background())
		t.Errorf("Ping after a failed open = %v, want %v", err, mongo.ErrClientDisconnected)
	}
}
