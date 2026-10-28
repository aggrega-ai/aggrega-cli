// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestOrderedMap_DecodeFailuresNameWhatWasWritten decodes Workload documents an
// author could write wrongly, through the same decode the compiler applies to a
// workload template's output, and reads what the failure says: the key, the line,
// what was written and what the position takes — and never the name of a Go type
// or of the map type the document is decoded into.
func TestOrderedMap_DecodeFailuresNameWhatWasWritten(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "volume mounts written as a map",
			src: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\ncontainers:\n  main:\n    image: nginx\n" +
				"    volumeMounts:\n      config: /etc/config\n",
			want: []string{`under key "main" (line 5)`, "line 7: a map is written where a list of volume mounts is expected"},
		},
		{
			name: "a service key written twice",
			src: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\ncontainers:\n  main:\n    image: nginx\n" +
				"services:\n  http:\n    type: ClusterIP\n  http:\n    type: ClusterIP\n",
			want: []string{`line 9: duplicate key "http": a key is written once in a map`},
		},
		{
			name: "ports written as a number",
			src: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\ncontainers:\n  main:\n    image: nginx\n" +
				"    ports: 8080\n",
			want: []string{"line 6: a number is written where a map is expected"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var w Workload
			err := yaml.Unmarshal([]byte(tc.src), &w)
			if err == nil {
				t.Fatal("the document must fail to decode")
			}
			msg := err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("the failure must read %q:\n%s", want, msg)
				}
			}
			for _, leak := range []string{"OrderedMap", "types.", "[]", "!!", "unmarshal", "kind 8"} {
				if strings.Contains(msg, leak) {
					t.Errorf("the failure still carries %q:\n%s", leak, msg)
				}
			}
		})
	}
}
