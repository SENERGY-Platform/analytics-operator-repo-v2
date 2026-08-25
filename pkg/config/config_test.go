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
	"testing"
	"time"
)

// envVars are every variable Config binds. The defaults test has to clear all of
// them, or it would pass or fail depending on the shell it runs in.
var envVars = []string{
	"DEBUG", "SERVER_PORT", "LOGGER_CONFIG", "LOGGER_LEVEL", "MONGO_URL",
	"MONGO_DATABASE", "HTTP_TIMEOUT", "PERMISSIONS_V2_URL", "URL_PREFIX",
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

	if cfg.ServerPort != 8000 {
		t.Errorf("ServerPort = %d, want 8000", cfg.ServerPort)
	}
	// The Dockerfile health check probes this port; the two have to agree.
	if cfg.MongoUrl != "localhost:27017" {
		t.Errorf("MongoUrl = %q, want localhost:27017", cfg.MongoUrl)
	}
	if cfg.MongoDatabase != "db" {
		t.Errorf("MongoDatabase = %q, want db", cfg.MongoDatabase)
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
