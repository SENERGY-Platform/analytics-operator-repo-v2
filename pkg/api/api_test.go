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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	// util.Logger is a package-level var that main initialises before anything
	// logs. Handler code reaches for it unconditionally, so a test that walks an
	// error path panics on nil unless it is set here. Discarded, not silenced by
	// level: InitStructLogger writes to os.Stdout and cannot be redirected.
	util.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	m.Run()
}

func TestStatusCode(t *testing.T) {
	// The handlers wrap the sentinels with fmt.Errorf, so the wrapped forms have
	// to map like the bare ones — errors.Is, never ==.
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"invalid input", lib.ErrInvalidInput, http.StatusBadRequest},
		{"invalid input, wrapped", fmt.Errorf("%w: malformed request body", lib.ErrInvalidInput), http.StatusBadRequest},
		{"missing rights", lib.ErrMissingRights, http.StatusForbidden},
		{"missing rights, wrapped", fmt.Errorf("%w: on read", lib.ErrMissingRights), http.StatusForbidden},
		{"not found", lib.ErrNotFound, http.StatusNotFound},
		{"unrecognised", errors.New("connection refused"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusCode(tc.err); got != tc.want {
				t.Errorf("statusCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestSafeErrorPassesOnlyOwnSentinels(t *testing.T) {
	for _, err := range []error{lib.ErrInvalidInput, lib.ErrMissingRights, lib.ErrNotFound} {
		if got := safeError(err); !errors.Is(got, err) {
			t.Errorf("safeError(%v) = %v, want the sentinel itself", err, got)
		}
	}
	// A wrapped sentinel keeps its own text: the wrap is written by a handler and
	// says something the caller can act on.
	wrapped := fmt.Errorf("%w: malformed operator id", lib.ErrInvalidInput)
	if got := safeError(wrapped).Error(); got != wrapped.Error() {
		t.Errorf("safeError dropped the wrap text: %q", got)
	}
}

func TestSafeErrorRedactsInternals(t *testing.T) {
	// ErrorHandler writes the error text into the response body, so anything not
	// built here must be replaced whole — not wrapped, not appended to.
	internal := errors.New(`mongo: auth error: sasl conversation error for user "operator-repo" on db "admin"`)
	got := safeError(internal).Error()
	if got != MessageSomethingWrong {
		t.Fatalf("safeError leaked an internal error: %q", got)
	}
	for _, leak := range []string{"mongo", "sasl", "operator-repo", "admin"} {
		if strings.Contains(got, leak) {
			t.Errorf("redacted message still contains %q: %q", leak, got)
		}
	}
}

// unsignedToken builds a token that jwt.Parse accepts. It parses unverified, so
// the signature segment is never read — the property the gateway has to make up
// for, and the reason this needs no key material.
func unsignedToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal token part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + enc(claims) + ".not-a-signature"
}

// requestContext builds a gin context for one request, the way AuthMiddleware
// sees it.
func requestContext(t *testing.T, target string, headers map[string]string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c
}

func TestGetUserId(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		headers map[string]string
		want    string
		wantErr bool
	}{
		{
			name:    "x-userid header",
			target:  "/operator",
			headers: map[string]string{"X-UserId": "user-a"},
			want:    "user-a",
		},
		{
			// for_user is how a service acts on a user's behalf; it has to win
			// over the caller's own id, otherwise the impersonation is silently
			// ignored rather than refused.
			name:    "for_user with admin role beats x-userid",
			target:  "/operator?for_user=user-b",
			headers: map[string]string{"X-UserId": "service-account", "X-User-Roles": "user, admin"},
			want:    "user-b",
		},
		{
			name:    "for_user without admin role is ignored",
			target:  "/operator?for_user=user-b",
			headers: map[string]string{"X-UserId": "user-a", "X-User-Roles": "user"},
			want:    "user-a",
		},
		{
			name:    "for_user without admin role and nothing to fall back on",
			target:  "/operator?for_user=user-b",
			headers: map[string]string{"X-User-Roles": "user"},
			wantErr: true,
		},
		{
			// The separator is ", " exactly, and the same split runs in the other
			// services that carry this code. A client sending "user,admin" is not
			// recognised as admin. Pinned so that changing it here alone — which
			// would diverge from the rest of the platform — has to be deliberate.
			name:    "roles without a space after the comma are not split",
			target:  "/operator?for_user=user-b",
			headers: map[string]string{"X-UserId": "user-a", "X-User-Roles": "user,admin"},
			want:    "user-a",
		},
		{
			name:    "single admin role",
			target:  "/operator?for_user=user-b",
			headers: map[string]string{"X-User-Roles": "admin"},
			want:    "user-b",
		},
		{
			name:    "empty for_user falls through to the header",
			target:  "/operator?for_user=",
			headers: map[string]string{"X-UserId": "user-a", "X-User-Roles": "admin"},
			want:    "user-a",
		},
		{
			name:    "no headers at all",
			target:  "/operator",
			headers: nil,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := getUserId(requestContext(t, tc.target, tc.headers))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got user id %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("user id = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetUserIdFromToken(t *testing.T) {
	token := unsignedToken(t, map[string]any{"sub": "user-from-token"})

	t.Run("bare token", func(t *testing.T) {
		got, err := getUserId(requestContext(t, "/operator", map[string]string{HeaderAuthorization: token}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "user-from-token" {
			t.Errorf("user id = %q, want %q", got, "user-from-token")
		}
	})

	t.Run("bearer prefix", func(t *testing.T) {
		got, err := getUserId(requestContext(t, "/operator", map[string]string{HeaderAuthorization: "Bearer " + token}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "user-from-token" {
			t.Errorf("user id = %q, want %q", got, "user-from-token")
		}
	})

	t.Run("x-userid wins over the token", func(t *testing.T) {
		got, err := getUserId(requestContext(t, "/operator", map[string]string{
			"X-UserId":          "user-from-header",
			HeaderAuthorization: token,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "user-from-header" {
			t.Errorf("user id = %q, want %q", got, "user-from-header")
		}
	})

	t.Run("malformed token", func(t *testing.T) {
		if _, err := getUserId(requestContext(t, "/operator", map[string]string{HeaderAuthorization: "not.a.jwt"})); err == nil {
			t.Error("expected an error for a malformed token")
		}
	})

	t.Run("token without a subject", func(t *testing.T) {
		// jwt.Parse does not require sub, so this yields an empty user id and no
		// error — the request then runs as nobody. Pinned because it is the one
		// path where a well-formed token produces no identity.
		got, err := getUserId(requestContext(t, "/operator", map[string]string{
			HeaderAuthorization: unsignedToken(t, map[string]any{"preferred_username": "nobody"}),
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("user id = %q, want empty", got)
		}
	})
}

func TestAuthMiddleware(t *testing.T) {
	t.Run("passes the user id on", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/operator", nil)
		c.Request.Header.Set("X-UserId", "user-a")

		AuthMiddleware()(c)

		if c.IsAborted() {
			t.Fatal("request was aborted")
		}
		if got := c.GetString(UserIdKey); got != "user-a" {
			t.Errorf("context %s = %q, want %q", UserIdKey, got, "user-a")
		}
	})

	t.Run("rejects a request without any identity", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/operator", nil)

		AuthMiddleware()(c)

		if !c.IsAborted() {
			t.Error("request was not aborted")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
		if body := rec.Body.String(); body != MessageUnauthorized {
			t.Errorf("body = %q, want %q", body, MessageUnauthorized)
		}
	})
}
