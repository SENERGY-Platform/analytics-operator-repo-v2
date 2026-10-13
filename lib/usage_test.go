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

package lib

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestUsageUnavailableErrorsNameTheServiceAndShareTheCategory(t *testing.T) {
	tests := []struct {
		err     error
		names   string
		notThat error
	}{
		{ErrFlowRepoUnavailable, "flow repository", ErrPipelineRegistryUnavailable},
		{ErrPipelineRegistryUnavailable, "pipeline registry", ErrFlowRepoUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.names, func(t *testing.T) {
			wrapped := fmt.Errorf("delete: %w", tc.err)
			if !errors.Is(wrapped, ErrUsageUnavailable) || !errors.Is(wrapped, tc.err) {
				t.Errorf("%v does not match its category and itself", wrapped)
			}
			if errors.Is(tc.err, tc.notThat) {
				t.Errorf("%v matches %v", tc.err, tc.notThat)
			}
			if !strings.Contains(tc.err.Error(), tc.names) || !strings.Contains(tc.err.Error(), "nothing was deleted") {
				t.Errorf("text = %q, want it to name the %s and say that nothing was deleted", tc.err, tc.names)
			}
		})
	}
}

func TestUsedOperatorInUse(t *testing.T) {
	tests := []struct {
		name string
		used UsedOperator
		want bool
	}{
		{"unused", UsedOperator{}, false},
		{"flows only", UsedOperator{OperatorUsage: OperatorUsage{Flows: 1}}, true},
		{"pipelines only", UsedOperator{PipelineUsage: PipelineUsage{Pipelines: 1}}, true},
		{"both", UsedOperator{OperatorUsage: OperatorUsage{Flows: 2}, PipelineUsage: PipelineUsage{Pipelines: 3}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.used.InUse(); got != tc.want {
				t.Errorf("InUse = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInUseErrorText(t *testing.T) {
	if got := (&InUseError{}).Error(); got != "operator is still used by flows or pipelines" {
		t.Errorf("operator text = %q", got)
	}
	if got := (&InUseError{Version: "v1.0.0"}).Error(); got != "version is still used by flows or pipelines" {
		t.Errorf("version text = %q", got)
	}
}
