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
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

var versionLabelPattern = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

type semver [3]int64

// parseVersionLabel accepts vMAJOR.MINOR.PATCH only, without pre-release or build suffix.
func parseVersionLabel(label string) (semver, error) {
	m := versionLabelPattern.FindStringSubmatch(label)
	if m == nil {
		return semver{}, fmt.Errorf("%w: version must look like v1.2.3", ErrInvalidInput)
	}
	var v semver
	for i := range v {
		n, err := strconv.ParseInt(m[i+1], 10, 64)
		if err != nil {
			return semver{}, fmt.Errorf("%w: version numbers are too large", ErrInvalidInput)
		}
		v[i] = n
	}
	return v, nil
}

// ValidateVersionLabel reports whether label is a valid vMAJOR.MINOR.PATCH.
func ValidateVersionLabel(label string) error {
	_, err := parseVersionLabel(label)
	return err
}

// compareVersions orders by number, so v1.10.0 is above v1.9.0. A stored label that does
// not parse cannot come from PutVersion; it sorts lowest instead of failing a read.
func compareVersions(a, b OperatorVersion) int {
	av, _ := parseVersionLabel(a.Version)
	bv, _ := parseVersionLabel(b.Version)
	return slices.Compare(av[:], bv[:])
}

// PutVersion returns versions with v added, or replacing the entry of the same label, sorted
// ascending. Pipelines are matched to versions by image, so another version of the operator
// may not use v's image. The input slice is not modified.
func PutVersion(versions []OperatorVersion, v OperatorVersion) (result []OperatorVersion, created bool, err error) {
	if err = ValidateVersionLabel(v.Version); err != nil {
		return nil, false, err
	}
	if v.Image == "" {
		return nil, false, fmt.Errorf("%w: a version needs an image", ErrInvalidInput)
	}
	// Clients iterate over the ports; an absent list is sent as [] rather than null.
	v.Inputs = nonNil(v.Inputs)
	v.Outputs = nonNil(v.Outputs)
	v.Config = nonNil(v.Config)
	result = make([]OperatorVersion, 0, len(versions)+1)
	created = true
	for _, other := range versions {
		if other.Version == v.Version {
			created = false
			continue
		}
		if other.Image == v.Image {
			return nil, false, fmt.Errorf("%w: another version already uses this image", ErrInvalidInput)
		}
		result = append(result, other)
	}
	result = append(result, v)
	slices.SortStableFunc(result, compareVersions)
	return result, created, nil
}

// NewestVersion is the highest version that is not deprecated, or the highest overall when
// all are deprecated. ok is false for an empty list.
func NewestVersion(versions []OperatorVersion) (newest OperatorVersion, ok bool) {
	for _, v := range versions {
		switch {
		case !ok:
			newest, ok = v, true
		case newest.Deprecated && !v.Deprecated:
			newest = v
		case !newest.Deprecated && v.Deprecated:
			// A live version beats a deprecated one whatever the numbers say.
		case compareVersions(v, newest) > 0:
			newest = v
		}
	}
	return newest, ok
}

// CheckTopLevel refuses a write body whose image, inputs, outputs or config_values differ from
// the newest version of the stored operator. An absent image or list passes, since a client
// that does not know about versions leaves it out; an explicit [] must match, so clearing the
// ports is refused rather than ignored. Order matters.
func CheckTopLevel(body Operator, newest OperatorVersion) error {
	if (body.Image == "" || body.Image == newest.Image) &&
		sameOrAbsent(body.Inputs, newest.Inputs) &&
		sameOrAbsent(body.Outputs, newest.Outputs) &&
		sameOrAbsent(body.Config, newest.Config) {
		return nil
	}
	return fmt.Errorf("%w: image, inputs, outputs and config_values of an operator with versions follow the newest version; change them through /operator/{id}/versions/{version}", ErrInvalidInput)
}

func nonNil(values []Value) []Value {
	if values == nil {
		return []Value{}
	}
	return values
}

// sameOrAbsent treats nil as absent; slices.Equal already treats [] and nil as equal.
func sameOrAbsent(got, want []Value) bool {
	return got == nil || slices.Equal(got, want)
}
