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

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sb_util "github.com/SENERGY-Platform/go-service-base/util"
)

// envVars are every variable Config binds. The defaults test has to clear all of
// them, or it would pass or fail depending on the shell it runs in.
var envVars = []string{
	"DEBUG", "SERVER_PORT", "LOGGER_CONFIG", "LOGGER_LEVEL", "MONGO_URL",
	"MONGO_USER", "MONGO_PASSWORD", "MONGO_AUTH_SOURCE", "MONGO_DATABASE",
	"HTTP_TIMEOUT", "PERMISSIONS_V2_URL", "URL_PREFIX",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envVars {
		if v, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { _ = os.Setenv(k, v) })
			if err := os.Unsetenv(k); err != nil {
				t.Fatalf("unset %s: %v", k, err)
			}
		}
	}
}

func TestNewDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := New("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The Dockerfile health check probes this port; the two have to agree.
	if cfg.ServerPort != 8000 {
		t.Errorf("ServerPort = %d, want 8000", cfg.ServerPort)
	}
	if cfg.MongoUrl != "mongodb://localhost:27017" {
		t.Errorf("MongoUrl = %q, want mongodb://localhost:27017", cfg.MongoUrl)
	}
	if cfg.MongoUser != "" || cfg.MongoPassword.Value() != "" {
		t.Errorf("MongoUser/MongoPassword set by default, want no authentication")
	}
	if cfg.MongoAuthSource != "admin" {
		t.Errorf("MongoAuthSource = %q, want admin", cfg.MongoAuthSource)
	}
	if cfg.MongoDatabase != "analytics_operator_repo" {
		t.Errorf("MongoDatabase = %q, want analytics_operator_repo", cfg.MongoDatabase)
	}
	if cfg.HttpTimeout != 30*time.Second {
		t.Errorf("HttpTimeout = %v, want 30s", cfg.HttpTimeout)
	}
	if cfg.Logger.Level != "info" {
		t.Errorf("Logger.Level = %q, want info", cfg.Logger.Level)
	}
	if cfg.Debug {
		t.Error("Debug = true, want false")
	}
	if cfg.URLPrefix != "" {
		t.Errorf("URLPrefix = %q, want empty", cfg.URLPrefix)
	}
}

func TestNewEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("MONGO_DATABASE", "operators_test")
	t.Setenv("SERVER_PORT", "9001")
	// A duration comes in as text and needs the type parser wired in New; a bare
	// integer would be parsed as nanoseconds or rejected, so pin the unit form.
	t.Setenv("HTTP_TIMEOUT", "5s")
	t.Setenv("URL_PREFIX", "/analytics-operator-repo")

	cfg, err := New("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MongoDatabase != "operators_test" {
		t.Errorf("MongoDatabase = %q, want operators_test", cfg.MongoDatabase)
	}
	if cfg.ServerPort != 9001 {
		t.Errorf("ServerPort = %d, want 9001", cfg.ServerPort)
	}
	if cfg.HttpTimeout != 5*time.Second {
		t.Errorf("HttpTimeout = %v, want 5s", cfg.HttpTimeout)
	}
	if cfg.URLPrefix != "/analytics-operator-repo" {
		t.Errorf("URLPrefix = %q, want /analytics-operator-repo", cfg.URLPrefix)
	}
}

func TestNewMongoCredentialsFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("MONGO_URL", "mongodb://mongo-0.example:27017,mongo-1.example:27017/?replicaSet=rs0")
	t.Setenv("MONGO_USER", "analytics-operator-repo")
	t.Setenv("MONGO_PASSWORD", "s3cret-pw")
	t.Setenv("MONGO_AUTH_SOURCE", "users")

	cfg, err := New("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MongoUrl != "mongodb://mongo-0.example:27017,mongo-1.example:27017/?replicaSet=rs0" {
		t.Errorf("MongoUrl = %q, want the variable unchanged", cfg.MongoUrl)
	}
	if cfg.MongoUser != "analytics-operator-repo" {
		t.Errorf("MongoUser = %q, want analytics-operator-repo", cfg.MongoUser)
	}
	// Holds only while the Secret parser is registered in envTypeParser.
	if cfg.MongoPassword.Value() != "s3cret-pw" {
		t.Errorf("MongoPassword.Value() = %q, want the variable's value", cfg.MongoPassword.Value())
	}
	if cfg.MongoAuthSource != "users" {
		t.Errorf("MongoAuthSource = %q, want users", cfg.MongoAuthSource)
	}
}

func TestLoggedConfigHidesMongoPassword(t *testing.T) {
	clearEnv(t)
	t.Setenv("MONGO_PASSWORD", "s3cret-pw")

	cfg, err := New("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// main logs the config through exactly this call on every start.
	if logged := sb_util.ToJsonStr(cfg); strings.Contains(logged, "s3cret-pw") {
		t.Errorf("the logged config contains the password: %s", logged)
	}
}

func TestNewFileThenEnv(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mongo_database":"from_file","server_port":9100}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("MONGO_DATABASE", "from_env")

	cfg, err := New(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The file is read first and the environment on top of it, which is what
	// makes a deployment able to override a baked-in config.
	if cfg.MongoDatabase != "from_env" {
		t.Errorf("MongoDatabase = %q, want from_env — the environment must win", cfg.MongoDatabase)
	}
	if cfg.ServerPort != 9100 {
		t.Errorf("ServerPort = %d, want 9100 from the file", cfg.ServerPort)
	}
}

func TestNewMissingFileIsAnError(t *testing.T) {
	clearEnv(t)
	// Silently falling back to defaults would start the service against the
	// wrong database when a mount is missing.
	if _, err := New(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("a missing config file was accepted")
	}
}
