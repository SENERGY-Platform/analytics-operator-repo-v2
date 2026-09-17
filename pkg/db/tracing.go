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
	"sync"

	"go.mongodb.org/mongo-driver/v2/event"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// tracerName labels the instrumentation, not the service; the collector shows it
// as the scope a span was produced by.
const tracerName = "github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/db"

// commandTracer opens a span per MongoDB command and closes it when the server
// answers. It stands in for otelmongo, which exists only for the v1 driver: its
// NewMonitor returns that module's event.CommandMonitor, which is a different
// type from the one the v2 driver takes.
type commandTracer struct {
	mu    sync.Mutex
	spans map[commandKey]trace.Span
}

// A request id is only unique per connection, so both belong in the key.
type commandKey struct {
	connectionID string
	requestID    int64
}

// newCommandMonitor returns the monitor to hand to options.Client().SetMonitor.
func newCommandMonitor() *event.CommandMonitor {
	t := &commandTracer{spans: map[commandKey]trace.Span{}}
	return &event.CommandMonitor{
		Started:   t.started,
		Succeeded: t.succeeded,
		Failed:    t.failed,
	}
}

func (t *commandTracer) started(ctx context.Context, e *event.CommandStartedEvent) {
	attrs := []attribute.KeyValue{
		attribute.String("db.system", "mongodb"),
		attribute.String("db.name", e.DatabaseName),
		attribute.String("db.operation", e.CommandName),
	}
	name := e.CommandName
	// For most commands the value of the field named like the command is the
	// collection; for the rest — getMore carries a cursor id there — there is none.
	if collection, ok := e.Command.Lookup(e.CommandName).StringValueOK(); ok {
		attrs = append(attrs, attribute.String("db.mongodb.collection", collection))
		name = collection + "." + e.CommandName
	}
	// The provider is resolved per command rather than when the monitor is built,
	// so that a monitor constructed before OpenTelemetry is set up does not hold
	// the no-op provider for the life of the process.
	_, span := otel.GetTracerProvider().Tracer(tracerName).Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...))

	t.mu.Lock()
	defer t.mu.Unlock()
	t.spans[commandKey{e.ConnectionID, e.RequestID}] = span
}

func (t *commandTracer) succeeded(_ context.Context, e *event.CommandSucceededEvent) {
	if span := t.take(commandKey{e.ConnectionID, e.RequestID}); span != nil {
		span.End()
	}
}

func (t *commandTracer) failed(_ context.Context, e *event.CommandFailedEvent) {
	span := t.take(commandKey{e.ConnectionID, e.RequestID})
	if span == nil {
		return
	}
	span.SetStatus(codes.Error, e.Failure.Error())
	span.End()
}

// take hands out the span and forgets it, so that a command cannot end twice and
// the map cannot grow with finished commands.
func (t *commandTracer) take(key commandKey) trace.Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	span, ok := t.spans[key]
	if !ok {
		return nil
	}
	delete(t.spans, key)
	return span
}
