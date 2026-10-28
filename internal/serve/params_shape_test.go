// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParamsOfTheWrongShapeNameTheFieldAndTheShapes holds a request whose params
// carry a value of the wrong shape to a refusal a client can act on: the field
// the value was sent under, the shape it was sent in and the shape the field
// takes — never the program's params type or the decoder's own phrasing. One
// fault per row.
func TestParamsOfTheWrongShapeNameTheFieldAndTheShapes(t *testing.T) {
	t.Parallel()
	type kindParams struct {
		Kind string `json:"kind"`
	}
	cases := []struct {
		name   string
		params string
		read   func(raw json.RawMessage) error
		want   string
	}{
		{
			name:   "a method's own field",
			params: `{"kind": 5}`,
			read: func(raw json.RawMessage) error {
				var p kindParams
				return decodeParams(&jsonRPCRequest{Method: "aggrega/getSchema", Params: raw}, &p)
			},
			want: `"kind": a number is written where text is expected`,
		},
		{
			name:   "the workspace root every method reads",
			params: `{"workspaceRoot": 7}`,
			read: func(raw json.RawMessage) error {
				_, err := workspaceFrom(raw)
				return err
			},
			want: "the `workspaceRoot` parameter must be a string: \"workspaceRoot\": a number is written where text is expected",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.read(json.RawMessage(tc.params))
			if err == nil {
				t.Fatalf("params %s were accepted", tc.params)
			}
			if err.Error() != tc.want {
				t.Errorf("got  %q\nwant %q", err.Error(), tc.want)
			}
			for _, leak := range []string{"Go struct field", "json:", "cannot unmarshal", "Params"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("%q carries %q, which names nothing a client sent", err, leak)
				}
			}
		})
	}
}
