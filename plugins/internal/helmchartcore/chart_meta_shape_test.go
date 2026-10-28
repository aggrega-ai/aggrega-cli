// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestChartMetaOfTheWrongShapeIsRefusedInTheAuthorsWords holds the refusal of a
// chart's Chart.yaml whose field is written in the wrong shape to the line, the
// field and the two shapes, carried in the refusal itself so nothing rendering the
// whole chain reaches the decoder's own report. One fault per row.
func TestChartMetaOfTheWrongShapeIsRefusedInTheAuthorsWords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, manifest, want string
	}{
		{"name written as a list", "apiVersion: v2\nname: [upstream]\nversion: 0.1.0\n",
			"line 2 (name): a list is written where text is expected"},
		{"version written as a map", "apiVersion: v2\nname: upstream\nversion: {major: 1}\n",
			"line 3 (version): a map is written where text is expected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := ReadChartMetaBytes([]byte(tc.manifest), "web")
			if err == nil {
				t.Fatalf("a Chart.yaml with %s was accepted", tc.name)
			}
			text := err.Error() + "\n" + cerrors.CauseChain(err)
			if !strings.Contains(text, tc.want) {
				t.Errorf("refusal does not say %q:\n%s", tc.want, text)
			}
			for _, leak := range []string{"cannot unmarshal", "unmarshal errors", "!!"} {
				if strings.Contains(text, leak) {
					t.Errorf("refusal carries %q, which names nothing the author wrote:\n%s", leak, text)
				}
			}
		})
	}
}
