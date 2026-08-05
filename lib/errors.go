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

// Sentinel errors the API maps to status codes. Their text reaches the client,
// so only ever wrap them with static strings — never with an internal error.
var (
	// ErrInvalidInput is returned for anything the caller can correct: a
	// malformed body, an unparsable id, a bad query parameter.
	ErrInvalidInput = errors.New("invalid request")
	// ErrMissingRights covers both "no permission" and "does not exist",
	// which permissions-v2 cannot tell apart.
	ErrMissingRights = errors.New("requested instance nonexistent or missing rights")
	// ErrNotFound means the permission check passed but the document is gone.
	ErrNotFound = errors.New("operator not found")
)
