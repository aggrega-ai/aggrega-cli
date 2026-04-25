// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"testing"
)

func TestAsNumber(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      interface{}
		want    float64
		wantOK  bool
	}{
		{name: "int", in: int(42), want: 42, wantOK: true},
		{name: "int64", in: int64(42), want: 42, wantOK: true},
		{name: "int32", in: int32(42), want: 42, wantOK: true},
		{name: "float64", in: float64(42.5), want: 42.5, wantOK: true},
		{name: "float32", in: float32(42.5), want: 42.5, wantOK: true},
		{name: "json.Number-int", in: json.Number("42"), want: 42, wantOK: true},
		{name: "json.Number-float", in: json.Number("42.5"), want: 42.5, wantOK: true},
		{name: "json.Number-malformed", in: json.Number("not-a-number"), want: 0, wantOK: false},
		{name: "string-numeric", in: "42", want: 0, wantOK: false},
		{name: "string-non-numeric", in: "hello", want: 0, wantOK: false},
		{name: "bool", in: true, want: 0, wantOK: false},
		{name: "nil", in: nil, want: 0, wantOK: false},
		{name: "struct", in: struct{ X int }{X: 1}, want: 0, wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := asNumber(tc.in)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v (input %v %T)", ok, tc.wantOK, tc.in, tc.in)
			}
			if got != tc.want {
				t.Errorf("got = %v, want %v (input %v %T)", got, tc.want, tc.in, tc.in)
			}
		})
	}
}
