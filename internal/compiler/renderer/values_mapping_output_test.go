// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
)

// TestValuesMappingOutput_KeepsAnAuthoredTagAsTheTextWritten reads a values
// mapping's rendered document through the same decode the mapping's evaluation
// uses, and holds every value written under a tag of its author's own to the text
// written — tag included, a collection on one line in flow form whether it was
// written in flow or in block form — so a value written bare reaches the provider
// as the same text as one written inside quotes.
// YAML's own tags, untagged values and keys decode as they always have.
func TestValuesMappingOutput_KeepsAnAuthoredTagAsTheTextWritten(t *testing.T) {
	t.Parallel()
	doc := `bare: !GetAtt Queue.Arn
quoted: "!GetAtt Queue.Arn"
joined: !Join [",", [one, !Ref Two]]
block-joined: !Join
  - ","
  - - one
    - !Ref Two
block-if: !If
  - HasName
  - Name: !Ref Name
    Size: 5
  - !Ref NoValue
block-mapping: !Transform
  Name: Include
  Parameters:
    Location: !Sub "s3://${Bucket}/k"
sub: !Sub "x: ${A}"
empty: !Ref
nested:
  inner: !Ref Bucket
listed: [!Ref First, plain]
yaml-string: !!str 5
number: 5
anchored: &a !Ref Shared
aliased: *a
`
	var out valuesMappingOutput
	if err := parse.TemplateOutput([]byte(doc), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]any{
		"bare":          "!GetAtt Queue.Arn",
		"quoted":        "!GetAtt Queue.Arn",
		"joined":        `!Join [",", [one, !Ref Two]]`,
		"block-joined":  `!Join [",", [one, !Ref Two]]`,
		"block-if":      `!If [HasName, {Name: !Ref Name, Size: 5}, !Ref NoValue]`,
		"block-mapping": `!Transform {Name: Include, Parameters: {Location: !Sub "s3://${Bucket}/k"}}`,
		"sub":           `!Sub "x: ${A}"`,
		"empty":         "!Ref",
		"nested":        map[string]any{"inner": "!Ref Bucket"},
		"listed":        []any{"!Ref First", "plain"},
		"yaml-string":   "5",
		"number":        5,
		"anchored":      "!Ref Shared",
		"aliased":       "!Ref Shared",
	}
	if got := map[string]any(out); !reflect.DeepEqual(got, want) {
		for k, w := range want {
			if !reflect.DeepEqual(got[k], w) {
				t.Errorf("%s = %#v, want %#v", k, got[k], w)
			}
		}
		for k := range got {
			if _, expected := want[k]; !expected {
				t.Errorf("unexpected key %s = %#v", k, got[k])
			}
		}
	}
}
