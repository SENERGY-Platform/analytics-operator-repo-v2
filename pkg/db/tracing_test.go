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
	"testing"
	"time"

	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestIntegrationQuerySpansHangUnderTheCallersSpan is the point of instrumenting
// the driver: a query run for a request has to appear under that request's span,
// not as a root of its own. Confirmed against a real server, because the spans
// come from the driver's command events and an in-memory double emits none.
func TestIntegrationQuerySpansHangUnderTheCallersSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	restore := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(restore) })

	// Through New rather than testRepo: the monitor is installed on the client New
	// builds, and the shared client the other integration tests use has none.
	repo := tracedRepo(t)
	insert(t, repo, "user-a", "alpha")

	ctx, parent := provider.Tracer("test").Start(t.Context(), "request")
	if _, err := repo.All(ctx, "user-a", false, map[string][]string{}, userToken(t, "user-a")); err != nil {
		t.Fatalf("list: %v", err)
	}
	parent.End()

	var found bool
	for _, span := range recorder.Ended() {
		if span.Name() != "operators.find" {
			continue
		}
		found = true
		if span.Parent().SpanID() != parent.SpanContext().SpanID() {
			t.Errorf("%s hangs under %s, want the caller's span %s",
				span.Name(), span.Parent().SpanID(), parent.SpanContext().SpanID())
		}
		if !hasAttribute(span.Attributes(), "db.system", "mongodb") ||
			!hasAttribute(span.Attributes(), "db.operation", "find") ||
			!hasAttribute(span.Attributes(), "db.mongodb.collection", "operators") {
			t.Errorf("attributes = %v, want the database, the operation and the collection", span.Attributes())
		}
	}
	if !found {
		t.Fatalf("no span for the query; recorded %v", spanNames(recorder))
	}
}

// tracedRepo opens a database of its own through New, so the client carries the
// command monitor, and drops it again afterwards.
func tracedRepo(t *testing.T) *MongoRepo {
	t.Helper()
	requireMongo(t)
	name := testDatabaseName(t)
	database, err := New(testMongoURL(), name)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := probed.Database(name).Drop(dropCtx); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
		database.Disconnect()
	})
	perm, err := permV2Client.NewTestClient(t.Context())
	if err != nil {
		t.Fatalf("permissions-v2 test client: %v", err)
	}
	repo, err := NewMongoRepo(perm, database.OperatorCollection())
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	return repo
}

func hasAttribute(attrs []attribute.KeyValue, key, value string) bool {
	for _, attr := range attrs {
		if string(attr.Key) == key && attr.Value.AsString() == value {
			return true
		}
	}
	return false
}

func spanNames(recorder *tracetest.SpanRecorder) []string {
	names := []string{}
	for _, span := range recorder.Ended() {
		names = append(names, span.Name())
	}
	return names
}
