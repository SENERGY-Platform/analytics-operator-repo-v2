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

package flowrepo

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestUsageAsksWithTheCallersTokenAndParsesTheAnswer(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotAuth, gotMethod = r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), r.Method
		_, _ = w.Write([]byte(`{"flows":3,"readable":[{"id":"f1","name":"one"}]}`))
	}))
	t.Cleanup(srv.Close)

	// A trailing slash on the configured address must not double up.
	usage, err := New(srv.URL+"/").Usage(t.Context(), "op/1", "", "Bearer token-a")

	if err != nil {
		t.Fatal(err)
	}
	// Without a version the whole operator is asked for, so no query at all.
	if gotMethod != http.MethodGet || gotPath != "/operators/op%2F1/usage" || gotQuery != "" || gotAuth != "Bearer token-a" {
		t.Errorf("request = %s %s?%s with %q", gotMethod, gotPath, gotQuery, gotAuth)
	}
	if usage.Flows != 3 || len(usage.Readable) != 1 || usage.Readable[0].Id != "f1" || usage.Readable[0].Name != "one" {
		t.Errorf("usage = %+v", usage)
	}
}

func TestUsageWithAVersionAsksForThatVersionOnly(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{"flows":1,"readable":[]}`))
	}))
	t.Cleanup(srv.Close)

	// A label is never this, but the parameter has to arrive as given whatever it holds.
	usage, err := New(srv.URL).Usage(t.Context(), "op-1", "v1.2.3&x=y", "Bearer a")

	if err != nil {
		t.Fatal(err)
	}
	if len(gotQuery) != 1 || gotQuery.Get("version") != "v1.2.3&x=y" {
		t.Errorf("query = %v, want only version=v1.2.3&x=y", gotQuery)
	}
	if usage.Flows != 1 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestUsageOfAnUnusedOperatorHasAnEmptyReadableList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"flows":0,"readable":null}`))
	}))
	t.Cleanup(srv.Close)

	usage, err := New(srv.URL).Usage(t.Context(), "op-1", "", "")

	if err != nil {
		t.Fatal(err)
	}
	if usage.Flows != 0 || usage.Readable == nil || len(usage.Readable) != 0 {
		t.Errorf("usage = %#v", usage)
	}
}

// Every one of these is an unknown answer and must never read as "no flow uses it".
func TestUsageFailsOnAnythingButACompleteAnswer(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		// A well-formed "unused" body, so that only the status can make these fail.
		{"unauthorized", http.StatusUnauthorized, `{"flows":0,"readable":[]}`},
		{"server error", http.StatusInternalServerError, `{"flows":0,"readable":[]}`},
		{"bad gateway text", http.StatusBadGateway, `bad gateway`},
		{"ok with an empty object", http.StatusOK, `{}`},
		{"ok with html from a proxy", http.StatusOK, `<html>maintenance</html>`},
		{"ok with an empty body", http.StatusOK, ``},
		{"ok with a negative count", http.StatusOK, `{"flows":-1,"readable":[]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			if usage, err := New(srv.URL).Usage(t.Context(), "op-1", "", "Bearer a"); err == nil {
				t.Errorf("no error, usage = %+v", usage)
			}
		})
	}
}

func TestUsageFailsWhenTheFlowRepoIsDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()

	if _, err := New(addr).Usage(t.Context(), "op-1", "", "Bearer a"); err == nil {
		t.Error("no error for a refused connection")
	}
}
