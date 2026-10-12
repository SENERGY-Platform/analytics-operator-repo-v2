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
	"slices"
	"testing"
)

func TestValidateVersionLabel(t *testing.T) {
	tests := []struct {
		label string
		valid bool
	}{
		{"v0.0.0", true},
		{"v1.10.3", true},
		{"v9223372036854775807.0.0", true},
		{"1.0.0", false},
		{"v1.0", false},
		{"v01.0.0", false},
		{"v1.0.0-rc.1", false},
		{"v1.0.0+build", false},
		{"v9223372036854775808.0.0", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			err := ValidateVersionLabel(tc.label)
			if tc.valid && err != nil {
				t.Errorf("err = %v, want valid", err)
			}
			if !tc.valid && !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func labels(versions []OperatorVersion) []string {
	out := make([]string, 0, len(versions))
	for _, v := range versions {
		out = append(out, v.Version)
	}
	return out
}

func TestPutVersion(t *testing.T) {
	v := func(label, image string) OperatorVersion { return OperatorVersion{Version: label, Image: image} }

	t.Run("adds and sorts numerically", func(t *testing.T) {
		var versions []OperatorVersion
		for _, label := range []string{"v1.9.0", "v1.10.0", "v0.2.0", "v1.9.1"} {
			var created bool
			var err error
			versions, created, err = PutVersion(versions, v(label, "img:"+label))
			if err != nil || !created {
				t.Fatalf("put %s: created = %v, err = %v", label, created, err)
			}
		}
		want := []string{"v0.2.0", "v1.9.0", "v1.9.1", "v1.10.0"}
		if got := labels(versions); !slices.Equal(got, want) {
			t.Errorf("labels = %v, want %v", got, want)
		}
	})

	t.Run("same label replaces, even with the image it already has", func(t *testing.T) {
		in := []OperatorVersion{v("v1.0.0", "img:1"), v("v1.1.0", "img:2")}
		out, created, err := PutVersion(in, OperatorVersion{Version: "v1.0.0", Image: "img:1", Deprecated: true})
		if err != nil || created {
			t.Fatalf("created = %v, err = %v, want a replacement", created, err)
		}
		if len(out) != 2 || !out[0].Deprecated || out[1].Deprecated {
			t.Errorf("out = %+v", out)
		}
	})

	t.Run("does not modify its input", func(t *testing.T) {
		in := []OperatorVersion{v("v1.1.0", "img:2")}
		if _, _, err := PutVersion(in, v("v1.0.0", "img:1")); err != nil {
			t.Fatal(err)
		}
		if len(in) != 1 || in[0].Version != "v1.1.0" {
			t.Errorf("input changed: %+v", in)
		}
	})

	t.Run("absent port lists become empty ones", func(t *testing.T) {
		out, _, err := PutVersion(nil, v("v1.0.0", "img:1"))
		if err != nil {
			t.Fatal(err)
		}
		if out[0].Inputs == nil || out[0].Outputs == nil || out[0].Config == nil {
			t.Errorf("out = %+v, want non-nil inputs, outputs and config_values", out[0])
		}
	})

	rejected := []struct {
		name string
		in   []OperatorVersion
		put  OperatorVersion
	}{
		{"empty image", nil, v("v1.0.0", "")},
		{"bad label", nil, v("1.0.0", "img:1")},
		{"image of another version", []OperatorVersion{v("v1.0.0", "img:1")}, v("v1.1.0", "img:1")},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			if _, _, err := PutVersion(tc.in, tc.put); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNewestVersion(t *testing.T) {
	v := func(label string, deprecated bool) OperatorVersion {
		return OperatorVersion{Version: label, Image: "img:" + label, Deprecated: deprecated}
	}
	tests := []struct {
		name     string
		versions []OperatorVersion
		want     string
	}{
		{"highest wins", []OperatorVersion{v("v1.9.0", false), v("v1.10.0", false), v("v0.1.0", false)}, "v1.10.0"},
		{"deprecated is skipped", []OperatorVersion{v("v1.0.0", false), v("v2.0.0", true)}, "v1.0.0"},
		{"all deprecated gives the highest", []OperatorVersion{v("v1.0.0", true), v("v2.0.0", true), v("v1.5.0", true)}, "v2.0.0"},
		{"a hotfix below a higher major is not newest", []OperatorVersion{v("v2.0.0", false), v("v1.0.1", false)}, "v2.0.0"},
		{"stored order does not matter", []OperatorVersion{v("v2.0.0", true), v("v1.0.0", false), v("v1.2.0", false)}, "v1.2.0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NewestVersion(tc.versions)
			if !ok || got.Version != tc.want {
				t.Errorf("newest = %q (ok %v), want %q", got.Version, ok, tc.want)
			}
		})
	}
	if _, ok := NewestVersion(nil); ok {
		t.Error("ok = true for an empty list")
	}
}

func TestCheckTopLevel(t *testing.T) {
	in := []Value{{Name: "a", Type: "string"}, {Name: "b", Type: "float"}}
	newest := OperatorVersion{Version: "v1.0.0", Image: "img:1", Inputs: in, Outputs: []Value{{Name: "o", Type: "string"}}}
	tests := []struct {
		name string
		body Operator
		ok   bool
	}{
		{"empty body", Operator{}, true},
		{"equal image and inputs", Operator{Image: "img:1", Inputs: slices.Clone(in)}, true},
		{"empty list where the version has none", Operator{Config: []Value{}}, true},
		{"empty list where the version has inputs", Operator{Inputs: []Value{}}, false},
		{"different image", Operator{Image: "img:2"}, false},
		{"different inputs", Operator{Inputs: []Value{{Name: "a", Type: "string"}}}, false},
		{"inputs in another order", Operator{Inputs: []Value{in[1], in[0]}}, false},
		{"outputs where the version has other ones", Operator{Outputs: []Value{{Name: "x", Type: "string"}}}, false},
		{"config where the version has none", Operator{Config: []Value{{Name: "c", Type: "string"}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckTopLevel(tc.body, newest)
			if tc.ok && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}
