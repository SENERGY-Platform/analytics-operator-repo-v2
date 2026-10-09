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

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/db"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
)

// fakeFlows answers the usage per operator id; an id in failing gets an error.
type fakeFlows struct {
	usage   map[string]lib.OperatorUsage
	failing map[string]bool
	asked   []string
	auths   []string
}

func (f *fakeFlows) Usage(_ context.Context, operatorId string, auth string) (lib.OperatorUsage, error) {
	f.asked = append(f.asked, operatorId)
	f.auths = append(f.auths, auth)
	if f.failing[operatorId] {
		return lib.OperatorUsage{}, errors.New("dial tcp 10.0.0.1:5000: connection refused")
	}
	return f.usage[operatorId], nil
}

// fakeOperators records what reaches the database layer.
type fakeOperators struct {
	db.OperatorRepository
	deleted []string
	err     error
}

func (f *fakeOperators) DeleteOperator(_ context.Context, id string, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeOperators) DeleteOperators(_ context.Context, ids []string, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, ids...)
	return nil
}

func used(flows int, names ...string) lib.OperatorUsage {
	usage := lib.OperatorUsage{Flows: flows, Readable: []lib.FlowRef{}}
	for _, n := range names {
		usage.Readable = append(usage.Readable, lib.FlowRef{Id: "id-" + n, Name: n})
	}
	return usage
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	restore := util.Logger
	util.Logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { util.Logger = restore })
	return &buf
}

func TestDeleteOperatorRefusedWhileFlowsUseIt(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(3, "mine")}}
	repo := &fakeOperators{}
	srv := &Service{dbRepo: repo, flows: flows}

	err := srv.DeleteOperator(t.Context(), "op-1", "Bearer a", DeleteOptions{UserId: "user-a"})

	var inUse *lib.InUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("err = %v, want *lib.InUseError", err)
	}
	if len(inUse.Operators) != 1 || inUse.Operators[0].Id != "op-1" || inUse.Operators[0].Flows != 3 ||
		len(inUse.Operators[0].Readable) != 1 || inUse.Operators[0].Readable[0].Name != "mine" {
		t.Errorf("operators = %+v", inUse.Operators)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("deleted %v, want nothing", repo.deleted)
	}
	if !slices.Equal(flows.auths, []string{"Bearer a"}) {
		t.Errorf("flow repository was asked with %v, want the caller's token", flows.auths)
	}
}

func TestDeleteOperatorDeletesAnUnusedOperator(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(0)}}
	repo := &fakeOperators{}
	srv := &Service{dbRepo: repo, flows: flows}

	if err := srv.DeleteOperator(t.Context(), "op-1", "Bearer a", DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repo.deleted, []string{"op-1"}) {
		t.Errorf("deleted %v, want [op-1]", repo.deleted)
	}
}

func TestDeleteOperatorsIsAllOrNothing(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{
		"op-free":  used(0),
		"op-used1": used(1, "f1"),
		"op-used2": used(2, "f2", "f3"),
	}}
	repo := &fakeOperators{}
	srv := &Service{dbRepo: repo, flows: flows}

	err := srv.DeleteOperators(t.Context(), []string{"op-free", "op-used1", "op-used2"}, "Bearer a", DeleteOptions{})

	var inUse *lib.InUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("err = %v, want *lib.InUseError", err)
	}
	var names []string
	for _, o := range inUse.Operators {
		names = append(names, o.Id)
	}
	if !slices.Equal(names, []string{"op-used1", "op-used2"}) {
		t.Errorf("named operators = %v, want exactly the used ones", names)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("deleted %v, want nothing, not even the unused operator", repo.deleted)
	}
}

func TestDeleteOperatorsDeletesWhenNoneIsUsed(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{}}
	repo := &fakeOperators{}
	srv := &Service{dbRepo: repo, flows: flows}

	if err := srv.DeleteOperators(t.Context(), []string{"op-1", "op-2"}, "Bearer a", DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repo.deleted, []string{"op-1", "op-2"}) {
		t.Errorf("deleted %v", repo.deleted)
	}
}

func TestDeleteWithUnreachableFlowRepoDeletesNothing(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "forced"}[force], func(t *testing.T) {
			flows := &fakeFlows{usage: map[string]lib.OperatorUsage{}, failing: map[string]bool{"op-2": true}}
			repo := &fakeOperators{}
			srv := &Service{dbRepo: repo, flows: flows}
			buf := captureLog(t)
			opts := DeleteOptions{Force: force}

			single := srv.DeleteOperator(t.Context(), "op-2", "Bearer a", opts)
			batch := srv.DeleteOperators(t.Context(), []string{"op-1", "op-2"}, "Bearer a", opts)

			for _, err := range []error{single, batch} {
				if !errors.Is(err, lib.ErrFlowRepoUnavailable) {
					t.Errorf("err = %v, want lib.ErrFlowRepoUnavailable", err)
					continue
				}
				// The response text is the sentinel's; the connection detail stays in the log.
				if strings.Contains(err.Error(), "10.0.0.1") {
					t.Errorf("error text leaks the connection detail: %q", err)
				}
			}
			if len(repo.deleted) != 0 {
				t.Errorf("deleted %v on an unknown answer", repo.deleted)
			}
			if !strings.Contains(buf.String(), "10.0.0.1") {
				t.Errorf("the failure detail was not logged:\n%s", buf.String())
			}
		})
	}
}

func TestForcedDeleteGoesAheadAndLogsAWarning(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(4, "mine"), "op-2": used(0)}}
	repo := &fakeOperators{}
	srv := &Service{dbRepo: repo, flows: flows}
	buf := captureLog(t)

	err := srv.DeleteOperators(t.Context(), []string{"op-1", "op-2"}, "Bearer admin", DeleteOptions{Force: true, UserId: "admin-1"})

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repo.deleted, []string{"op-1", "op-2"}) {
		t.Errorf("deleted %v, want both", repo.deleted)
	}
	var warnings []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == "deleted an operator that flows still use" {
			warnings = append(warnings, record)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want one for the used operator:\n%s", len(warnings), buf.String())
	}
	w := warnings[0]
	if w["level"] != "WARN" || w["operator_id"] != "op-1" || w["user_id"] != "admin-1" || w["flows"] != float64(4) {
		t.Errorf("warning = %v, want WARN with operator_id op-1, user_id admin-1, flows 4", w)
	}
}

func TestForcedDeleteOfAnUnusedOperatorLogsNoWarning(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(0)}}
	srv := &Service{dbRepo: &fakeOperators{}, flows: flows}
	buf := captureLog(t)

	if err := srv.DeleteOperator(t.Context(), "op-1", "Bearer admin", DeleteOptions{Force: true, UserId: "admin-1"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "still use") {
		t.Errorf("a warning was logged for an unused operator:\n%s", buf.String())
	}
}

func TestForcedDeleteThatFailsLogsNoWarning(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(2, "f")}}
	srv := &Service{dbRepo: &fakeOperators{err: lib.ErrMissingRights}, flows: flows}
	buf := captureLog(t)

	err := srv.DeleteOperator(t.Context(), "op-1", "Bearer admin", DeleteOptions{Force: true, UserId: "admin-1"})

	if !errors.Is(err, lib.ErrMissingRights) {
		t.Fatalf("err = %v, want the database layer's error", err)
	}
	if strings.Contains(buf.String(), "still use") {
		t.Errorf("a deletion that did not happen was logged:\n%s", buf.String())
	}
}

func TestNewRefusesAnEmptyFlowRepoAddress(t *testing.T) {
	// It fails before the database is touched, so no MongoDB is needed.
	if _, err := New(t.Context(), "mock", "", db.MongoDB{}); err == nil {
		t.Error("an empty flow repository address was accepted")
	}
}
