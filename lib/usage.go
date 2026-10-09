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

// ErrFlowRepoUnavailable means the flow repository could not tell whether flows use an operator.
// Its text reaches the client, so wrap it only with static strings.
var ErrFlowRepoUnavailable = errors.New("could not check whether flows use the operator, nothing was deleted")

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

type UsedOperator struct {
	Id string `json:"id"`
	OperatorUsage
}

// InUseError refuses a deletion because flows still use the listed operators. The text is static;
// the handlers build the response body from Operators.
type InUseError struct {
	Operators []UsedOperator
}

func (e *InUseError) Error() string {
	return "operator is still used by flows"
}

// InUseResponse is the 409 body of the single delete.
type InUseResponse struct {
	Error string `json:"error"`
	OperatorUsage
}

// InUseBatchResponse is the 409 body of the batch delete; it names each used operator.
type InUseBatchResponse struct {
	Error     string         `json:"error"`
	Operators []UsedOperator `json:"operators"`
}
