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

import "errors"

// ErrUsageUnavailable is what ErrFlowRepoUnavailable and ErrPipelineRegistryUnavailable both
// match: one of the services could not tell whether the operator is in use, so nothing was
// deleted.
var ErrUsageUnavailable = errors.New("could not check whether the operator is in use, nothing was deleted")

// The texts reach the client, so wrap these only with static strings.
var (
	ErrFlowRepoUnavailable         error = &usageUnavailableError{service: "the flow repository"}
	ErrPipelineRegistryUnavailable error = &usageUnavailableError{service: "the pipeline registry"}
)

type usageUnavailableError struct {
	service string
}

func (e *usageUnavailableError) Error() string {
	return "could not ask " + e.service + " whether the operator is in use, nothing was deleted"
}

func (e *usageUnavailableError) Is(target error) bool {
	return target == ErrUsageUnavailable
}

// FlowRef names a flow the caller may read.
type FlowRef struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// OperatorUsage is the flow repository's answer: Flows counts the flows of all users, Readable
// lists only those the caller may read.
type OperatorUsage struct {
	Flows    int       `json:"flows"`
	Readable []FlowRef `json:"readable"`
}

// PipelineRef names a pipeline of the caller.
type PipelineRef struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// PipelineUsage is the pipeline registry's answer: Pipelines counts the pipelines of all users,
// ReadablePipelines lists only the caller's own. The registry calls that list "readable"; it is
// renamed here because it sits next to the flows' readable in the responses.
type PipelineUsage struct {
	Pipelines         int           `json:"pipelines"`
	ReadablePipelines []PipelineRef `json:"readable_pipelines"`
}

type UsedOperator struct {
	Id string `json:"id"`
	OperatorUsage
	PipelineUsage
}

// InUse reports whether any flow or pipeline uses the operator.
func (u UsedOperator) InUse() bool {
	return u.Flows > 0 || u.Pipelines > 0
}

// InUseError refuses a deletion because flows or pipelines still use the listed operators, or,
// with Version set, that version of the single listed operator. The text is static; the handlers
// build the response body from Operators.
type InUseError struct {
	Operators []UsedOperator
	Version   string
}

func (e *InUseError) Error() string {
	if e.Version != "" {
		return "version is still used by flows or pipelines"
	}
	return "operator is still used by flows or pipelines"
}

// InUseResponse is the 409 body of the single delete and of the version delete.
type InUseResponse struct {
	Error string `json:"error"`
	OperatorUsage
	PipelineUsage
}

// InUseBatchResponse is the 409 body of the batch delete; it names each used operator.
type InUseBatchResponse struct {
	Error     string         `json:"error"`
	Operators []UsedOperator `json:"operators"`
}
