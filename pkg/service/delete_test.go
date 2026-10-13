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
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/db"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
)

// fakeFlows answers the usage per operator id, or per "id@version" when a version is asked; an id
// in failing gets an error.
type fakeFlows struct {
	usage    map[string]lib.OperatorUsage
	failing  map[string]bool
	asked    []string
	versions []string
	auths    []string
}

func (f *fakeFlows) Usage(_ context.Context, operatorId string, version string, auth string) (lib.OperatorUsage, error) {
	f.asked = append(f.asked, operatorId)
	f.versions = append(f.versions, version)
	f.auths = append(f.auths, auth)
	if f.failing[operatorId] {
		return lib.OperatorUsage{}, errors.New("dial tcp 10.0.0.1:5000: connection refused")
	}
	key := operatorId
	if version != "" {
		key += "@" + version
	}
	return f.usage[key], nil
}

// fakePipelines is fakeFlows for the pipeline registry, keyed by "id@image" when an image is asked.
type fakePipelines struct {
	usage   map[string]lib.PipelineUsage
	failing map[string]bool
	asked   []string
	images  []string
	auths   []string
}

func (f *fakePipelines) Usage(_ context.Context, operatorId string, image string, auth string) (lib.PipelineUsage, error) {
	f.asked = append(f.asked, operatorId)
	f.images = append(f.images, image)
	f.auths = append(f.auths, auth)
	if f.failing[operatorId] {
		return lib.PipelineUsage{}, errors.New("dial tcp 10.0.0.2:8000: connection refused")
	}
	key := operatorId
	if image != "" {
		key += "@" + image
	}
	return f.usage[key], nil
}

func noPipelines() *fakePipelines {
	return &fakePipelines{usage: map[string]lib.PipelineUsage{}}
}

// fakeOperators records what reaches the database layer. DeleteOperatorVersion works on versions
// like the real one: refusals from lib.RemoveVersion come before the check.
type fakeOperators struct {
	db.OperatorRepository
	deleted         []string
	versions        []lib.OperatorVersion
	deletedVersions []string
	err             error
	// denied is what CheckAdministrate answers; checked records the ids it was asked about.
	denied  error
	checked [][]string
}

func (f *fakeOperators) CheckAdministrate(_ context.Context, ids []string, _ string) error {
	f.checked = append(f.checked, ids)
	return f.denied
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

func (f *fakeOperators) DeleteOperatorVersion(_ context.Context, id string, label string, _ string, check func(lib.OperatorVersion) error) error {
	if f.err != nil {
		return f.err
	}
	rest, removed, err := lib.RemoveVersion(f.versions, label)
	if err != nil {
		return err
	}
	if err = check(removed); err != nil {
		return err
	}
	f.versions = rest
	f.deletedVersions = append(f.deletedVersions, id+"@"+label)
	return nil
}

func used(flows int, names ...string) lib.OperatorUsage {
	usage := lib.OperatorUsage{Flows: flows, Readable: []lib.FlowRef{}}
	for _, n := range names {
		usage.Readable = append(usage.Readable, lib.FlowRef{Id: "id-" + n, Name: n})
	}
	return usage
}

func run(pipelines int, names ...string) lib.PipelineUsage {
	usage := lib.PipelineUsage{Pipelines: pipelines, ReadablePipelines: []lib.PipelineRef{}}
	for _, n := range names {
		usage.ReadablePipelines = append(usage.ReadablePipelines, lib.PipelineRef{Id: "id-" + n, Name: n})
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
	srv := &Service{dbRepo: repo, flows: flows, pipelines: noPipelines()}

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
	srv := &Service{dbRepo: repo, flows: flows, pipelines: noPipelines()}

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
	srv := &Service{dbRepo: repo, flows: flows, pipelines: noPipelines()}

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
	srv := &Service{dbRepo: repo, flows: flows, pipelines: noPipelines()}

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
			srv := &Service{dbRepo: repo, flows: flows, pipelines: noPipelines()}
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
	srv := &Service{dbRepo: repo, flows: flows, pipelines: noPipelines()}
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
		if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == "deleted an operator that flows or pipelines still use" {
			warnings = append(warnings, record)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want one for the used operator:\n%s", len(warnings), buf.String())
	}
	w := warnings[0]
	if w["level"] != "WARN" || w["operator_id"] != "op-1" || w["user_id"] != "admin-1" || w["flows"] != float64(4) || w["pipelines"] != float64(0) {
		t.Errorf("warning = %v, want WARN with operator_id op-1, user_id admin-1, flows 4, pipelines 0", w)
	}
}

func TestForcedDeleteOfAnUnusedOperatorLogsNoWarning(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(0)}}
	srv := &Service{dbRepo: &fakeOperators{}, flows: flows, pipelines: noPipelines()}
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
	srv := &Service{dbRepo: &fakeOperators{err: lib.ErrMissingRights}, flows: flows, pipelines: noPipelines()}
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
	if _, err := New(t.Context(), "mock", "", "http://pipelines.invalid", db.MongoDB{}); err == nil {
		t.Error("an empty flow repository address was accepted")
	}
}

func TestNewRefusesAnEmptyPipelineRegistryAddress(t *testing.T) {
	if _, err := New(t.Context(), "mock", "http://flow-repo.invalid", "", db.MongoDB{}); err == nil {
		t.Error("an empty pipeline registry address was accepted")
	}
}

func TestDeleteOperatorRefusedWhilePipelinesRunIt(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(0)}}
	pipelines := &fakePipelines{usage: map[string]lib.PipelineUsage{"op-1": run(2, "mine")}}
	repo := &fakeOperators{}
	srv := &Service{dbRepo: repo, flows: flows, pipelines: pipelines}

	single := srv.DeleteOperator(t.Context(), "op-1", "Bearer a", DeleteOptions{})
	batch := srv.DeleteOperators(t.Context(), []string{"op-1"}, "Bearer a", DeleteOptions{})

	for _, err := range []error{single, batch} {
		var inUse *lib.InUseError
		if !errors.As(err, &inUse) {
			t.Fatalf("err = %v, want *lib.InUseError", err)
		}
		u := inUse.Operators[0]
		if len(inUse.Operators) != 1 || u.Id != "op-1" || u.Flows != 0 || u.Pipelines != 2 ||
			len(u.ReadablePipelines) != 1 || u.ReadablePipelines[0].Name != "mine" || inUse.Version != "" {
			t.Errorf("in use = %+v", inUse)
		}
	}
	if len(repo.deleted) != 0 {
		t.Errorf("deleted %v, want nothing", repo.deleted)
	}
	// The whole operator: no image and no version narrow the question.
	if !slices.Equal(pipelines.auths, []string{"Bearer a", "Bearer a"}) || !slices.Equal(pipelines.images, []string{"", ""}) {
		t.Errorf("pipeline registry was asked with %v and images %v", pipelines.auths, pipelines.images)
	}
	if !slices.Equal(flows.versions, []string{"", ""}) {
		t.Errorf("flow repository was asked for versions %v, want none", flows.versions)
	}
}

func TestDeleteOperatorsNamesOperatorsUsedByEitherService(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-flows": used(1, "f")}}
	pipelines := &fakePipelines{usage: map[string]lib.PipelineUsage{"op-pipelines": run(1, "p")}}
	repo := &fakeOperators{}
	srv := &Service{dbRepo: repo, flows: flows, pipelines: pipelines}

	err := srv.DeleteOperators(t.Context(), []string{"op-flows", "op-free", "op-pipelines"}, "Bearer a", DeleteOptions{})

	var inUse *lib.InUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("err = %v, want *lib.InUseError", err)
	}
	var ids []string
	for _, o := range inUse.Operators {
		ids = append(ids, o.Id)
	}
	if !slices.Equal(ids, []string{"op-flows", "op-pipelines"}) {
		t.Errorf("named operators = %v", ids)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("deleted %v, want nothing", repo.deleted)
	}
}

func TestDeleteWithUnreachablePipelineRegistryDeletesNothing(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "forced"}[force], func(t *testing.T) {
			flows := &fakeFlows{usage: map[string]lib.OperatorUsage{}}
			pipelines := &fakePipelines{usage: map[string]lib.PipelineUsage{}, failing: map[string]bool{"op-2": true}}
			repo := &fakeOperators{}
			srv := &Service{dbRepo: repo, flows: flows, pipelines: pipelines}
			buf := captureLog(t)
			opts := DeleteOptions{Force: force}

			single := srv.DeleteOperator(t.Context(), "op-2", "Bearer a", opts)
			batch := srv.DeleteOperators(t.Context(), []string{"op-1", "op-2"}, "Bearer a", opts)

			for _, err := range []error{single, batch} {
				if !errors.Is(err, lib.ErrPipelineRegistryUnavailable) || errors.Is(err, lib.ErrFlowRepoUnavailable) {
					t.Errorf("err = %v, want lib.ErrPipelineRegistryUnavailable only", err)
					continue
				}
				if strings.Contains(err.Error(), "10.0.0.2") {
					t.Errorf("error text leaks the connection detail: %q", err)
				}
			}
			if len(repo.deleted) != 0 {
				t.Errorf("deleted %v on an unknown answer", repo.deleted)
			}
			if !strings.Contains(buf.String(), "10.0.0.2") {
				t.Errorf("the failure detail was not logged:\n%s", buf.String())
			}
		})
	}
}

func TestForcedDeleteOfAnOperatorPipelinesRunLogsBothCounts(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(1, "f")}}
	pipelines := &fakePipelines{usage: map[string]lib.PipelineUsage{"op-1": run(5)}}
	repo := &fakeOperators{}
	srv := &Service{dbRepo: repo, flows: flows, pipelines: pipelines}
	buf := captureLog(t)

	if err := srv.DeleteOperator(t.Context(), "op-1", "Bearer admin", DeleteOptions{Force: true, UserId: "admin-1"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repo.deleted, []string{"op-1"}) {
		t.Errorf("deleted %v", repo.deleted)
	}
	w := warning(t, buf, "deleted an operator that flows or pipelines still use")
	if w["operator_id"] != "op-1" || w["user_id"] != "admin-1" || w["flows"] != float64(1) || w["pipelines"] != float64(5) {
		t.Errorf("warning = %v, want operator_id op-1, user_id admin-1, flows 1, pipelines 5", w)
	}
}

// warning returns the one WARN record with the message.
func warning(t *testing.T, buf *bytes.Buffer, msg string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == msg && record["level"] == "WARN" {
			found = append(found, record)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d warnings %q, want one:\n%s", len(found), msg, buf.String())
	}
	return found[0]
}

func threeVersions() []lib.OperatorVersion {
	return []lib.OperatorVersion{
		{Version: "v1.0.0", Image: "img:1"},
		{Version: "v1.1.0", Image: "img:2"},
		{Version: "v2.0.0", Image: "img:3"},
	}
}

func TestDeleteOperatorVersionAsksForThatVersionOnly(t *testing.T) {
	// The operator as a whole is used; the version is not.
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(4, "f"), "op-1@v1.1.0": used(0)}}
	pipelines := &fakePipelines{usage: map[string]lib.PipelineUsage{"op-1": run(3), "op-1@img:2": run(0)}}
	repo := &fakeOperators{versions: threeVersions()}
	srv := &Service{dbRepo: repo, flows: flows, pipelines: pipelines}
	buf := captureLog(t)

	if err := srv.DeleteOperatorVersion(t.Context(), "op-1", "v1.1.0", "Bearer a", DeleteOptions{UserId: "user-a"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repo.deletedVersions, []string{"op-1@v1.1.0"}) || len(repo.versions) != 2 {
		t.Errorf("deleted %v, left %+v", repo.deletedVersions, repo.versions)
	}
	if !slices.Equal(flows.versions, []string{"v1.1.0"}) || !slices.Equal(flows.auths, []string{"Bearer a"}) {
		t.Errorf("flow repository was asked for %v with %v, want v1.1.0 with the caller's token", flows.versions, flows.auths)
	}
	if !slices.Equal(pipelines.images, []string{"img:2"}) || !slices.Equal(pipelines.auths, []string{"Bearer a"}) {
		t.Errorf("pipeline registry was asked for %v with %v, want img:2 with the caller's token", pipelines.images, pipelines.auths)
	}
	if strings.Contains(buf.String(), "still use") {
		t.Errorf("a warning was logged for an unused version:\n%s", buf.String())
	}
}

func TestDeleteOperatorVersionRefusedWhileInUse(t *testing.T) {
	tests := []struct {
		name          string
		flows         lib.OperatorUsage
		pipelines     lib.PipelineUsage
		wantFlows     int
		wantPipelines int
	}{
		{"pinned by flows", used(2, "mine"), run(0), 2, 0},
		{"run by pipelines", used(0), run(1, "mine"), 0, 1},
		{"both", used(1), run(1), 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1@v2.0.0": tc.flows}}
			pipelines := &fakePipelines{usage: map[string]lib.PipelineUsage{"op-1@img:3": tc.pipelines}}
			repo := &fakeOperators{versions: threeVersions()}
			srv := &Service{dbRepo: repo, flows: flows, pipelines: pipelines}

			err := srv.DeleteOperatorVersion(t.Context(), "op-1", "v2.0.0", "Bearer a", DeleteOptions{})

			var inUse *lib.InUseError
			if !errors.As(err, &inUse) {
				t.Fatalf("err = %v, want *lib.InUseError", err)
			}
			if inUse.Version != "v2.0.0" || len(inUse.Operators) != 1 || inUse.Operators[0].Flows != tc.wantFlows ||
				inUse.Operators[0].Pipelines != tc.wantPipelines {
				t.Errorf("in use = %+v", inUse)
			}
			if len(repo.deletedVersions) != 0 || len(repo.versions) != 3 {
				t.Errorf("deleted %v, want nothing", repo.deletedVersions)
			}
		})
	}
}

func TestDeleteOperatorVersionWithAnUnreachableServiceDeletesNothing(t *testing.T) {
	tests := []struct {
		name      string
		flows     map[string]bool
		pipelines map[string]bool
		want      error
		leak      string
	}{
		{"flow repository", map[string]bool{"op-1": true}, nil, lib.ErrFlowRepoUnavailable, "10.0.0.1"},
		{"pipeline registry", nil, map[string]bool{"op-1": true}, lib.ErrPipelineRegistryUnavailable, "10.0.0.2"},
	}
	for _, tc := range tests {
		for _, force := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: ", plain", true: ", forced"}[force], func(t *testing.T) {
				flows := &fakeFlows{usage: map[string]lib.OperatorUsage{}, failing: tc.flows}
				pipelines := &fakePipelines{usage: map[string]lib.PipelineUsage{}, failing: tc.pipelines}
				repo := &fakeOperators{versions: threeVersions()}
				srv := &Service{dbRepo: repo, flows: flows, pipelines: pipelines}
				buf := captureLog(t)

				err := srv.DeleteOperatorVersion(t.Context(), "op-1", "v1.0.0", "Bearer a", DeleteOptions{Force: force})

				if !errors.Is(err, tc.want) || strings.Contains(err.Error(), tc.leak) {
					t.Errorf("err = %v, want %v without the connection detail", err, tc.want)
				}
				if len(repo.deletedVersions) != 0 {
					t.Errorf("deleted %v on an unknown answer", repo.deletedVersions)
				}
				if !strings.Contains(buf.String(), tc.leak) {
					t.Errorf("the failure detail was not logged:\n%s", buf.String())
				}
			})
		}
	}
}

func TestForcedDeleteOfAUsedVersionGoesAheadAndLogsAWarning(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1@v1.0.0": used(2)}}
	pipelines := &fakePipelines{usage: map[string]lib.PipelineUsage{"op-1@img:1": run(3)}}
	repo := &fakeOperators{versions: threeVersions()}
	srv := &Service{dbRepo: repo, flows: flows, pipelines: pipelines}
	buf := captureLog(t)

	if err := srv.DeleteOperatorVersion(t.Context(), "op-1", "v1.0.0", "Bearer admin", DeleteOptions{Force: true, UserId: "admin-1"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repo.deletedVersions, []string{"op-1@v1.0.0"}) {
		t.Errorf("deleted %v", repo.deletedVersions)
	}
	w := warning(t, buf, "deleted an operator version that flows or pipelines still use")
	if w["operator_id"] != "op-1" || w["version"] != "v1.0.0" || w["user_id"] != "admin-1" || w["flows"] != float64(2) || w["pipelines"] != float64(3) {
		t.Errorf("warning = %v, want operator_id op-1, version v1.0.0, user_id admin-1, flows 2, pipelines 3", w)
	}
}

func TestForcedDeleteOfAVersionThatFailsLogsNoWarning(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1@v1.0.0": used(2)}}
	srv := &Service{dbRepo: &fakeOperators{err: lib.ErrConflict}, flows: flows, pipelines: noPipelines()}
	buf := captureLog(t)

	err := srv.DeleteOperatorVersion(t.Context(), "op-1", "v1.0.0", "Bearer admin", DeleteOptions{Force: true, UserId: "admin-1"})

	if !errors.Is(err, lib.ErrConflict) {
		t.Fatalf("err = %v, want the database layer's error", err)
	}
	if strings.Contains(buf.String(), "still use") {
		t.Errorf("a deletion that did not happen was logged:\n%s", buf.String())
	}
}

// Every refusal that does not depend on the usage comes before the usage is asked.
func TestDeleteOperatorVersionRefusalsBeforeTheUsageIsAsked(t *testing.T) {
	tests := []struct {
		name  string
		repo  *fakeOperators
		label string
		want  error
	}{
		{"malformed label", &fakeOperators{versions: threeVersions()}, "1.0.0", lib.ErrInvalidInput},
		{"missing rights", &fakeOperators{err: lib.ErrMissingRights}, "v1.0.0", lib.ErrMissingRights},
		{"unknown version", &fakeOperators{versions: threeVersions()}, "v9.0.0", lib.ErrVersionNotFound},
		{"only version", &fakeOperators{versions: threeVersions()[:1]}, "v1.0.0", lib.ErrInvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flows := &fakeFlows{usage: map[string]lib.OperatorUsage{}}
			pipelines := noPipelines()
			srv := &Service{dbRepo: tc.repo, flows: flows, pipelines: pipelines}

			err := srv.DeleteOperatorVersion(t.Context(), "op-1", tc.label, "Bearer a", DeleteOptions{Force: true})

			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if len(flows.asked) != 0 || len(pipelines.asked) != 0 {
				t.Errorf("usage asked: flows %v, pipelines %v", flows.asked, pipelines.asked)
			}
			if len(tc.repo.deletedVersions) != 0 {
				t.Errorf("deleted %v", tc.repo.deletedVersions)
			}
		})
	}
}

// The usage counts cover all users, so a caller without the Administrate permission must be refused
// before either service is asked.
func TestDeleteWithoutRightsAsksNoUsage(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "forced"}[force], func(t *testing.T) {
			denied := fmt.Errorf("%w: id op-2", lib.ErrMissingRights)
			// Used operators: the usage would be a 409 if it were asked.
			flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(3, "f"), "op-2": used(1)}}
			pipelines := &fakePipelines{usage: map[string]lib.PipelineUsage{"op-1": run(2)}}
			repo := &fakeOperators{denied: denied}
			srv := &Service{dbRepo: repo, flows: flows, pipelines: pipelines}
			opts := DeleteOptions{Force: force}

			single := srv.DeleteOperator(t.Context(), "op-1", "Bearer a", opts)
			batch := srv.DeleteOperators(t.Context(), []string{"op-1", "op-2"}, "Bearer a", opts)

			for _, err := range []error{single, batch} {
				if !errors.Is(err, lib.ErrMissingRights) {
					t.Errorf("err = %v, want lib.ErrMissingRights", err)
				}
			}
			if len(flows.asked) != 0 || len(pipelines.asked) != 0 {
				t.Errorf("usage asked: flows %v, pipelines %v", flows.asked, pipelines.asked)
			}
			if len(repo.deleted) != 0 {
				t.Errorf("deleted %v", repo.deleted)
			}
			if want := [][]string{{"op-1"}, {"op-1", "op-2"}}; !slices.EqualFunc(repo.checked, want, slices.Equal) {
				t.Errorf("permission checked for %v, want %v", repo.checked, want)
			}
		})
	}
}

// The permission check does not replace the usage check: a caller with rights still gets the 409.
func TestDeleteWithRightsStillAsksTheUsage(t *testing.T) {
	flows := &fakeFlows{usage: map[string]lib.OperatorUsage{"op-1": used(1, "f")}}
	repo := &fakeOperators{}
	srv := &Service{dbRepo: repo, flows: flows, pipelines: noPipelines()}

	err := srv.DeleteOperators(t.Context(), []string{"op-1", "op-2"}, "Bearer a", DeleteOptions{})

	var inUse *lib.InUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("err = %v, want *lib.InUseError", err)
	}
	if !slices.Equal(flows.asked, []string{"op-1", "op-2"}) {
		t.Errorf("flow repository was asked about %v", flows.asked)
	}
}
