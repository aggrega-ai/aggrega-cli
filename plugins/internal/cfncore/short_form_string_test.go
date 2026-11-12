// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestNormalizeFragment_AStringThatIsOneShortFormReadsAsThatIntrinsic normalizes a
// fragment writing each value twice — bare, as a tag, and quoted, as a string — and
// holds the two to one long-form intrinsic: a string that is wholly one short form
// is that intrinsic, the same reading a values mapping's binding gives it. A string
// that is not wholly one short form stays the text it is, and the template text an
// `!Sub` carries is its argument and is never itself re-read.
func TestNormalizeFragment_AStringThatIsOneShortFormReadsAsThatIntrinsic(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, bare, quoted string
	}{
		{"GetAtt", `!GetAtt Db.Endpoint.Address`, `"!GetAtt Db.Endpoint.Address"`},
		{"Ref", `!Ref Bucket`, `'!Ref Bucket'`},
		{"Sub", `!Sub "https://${Host}/api"`, `"!Sub https://${Host}/api"`},
		{"ImportValue", `!ImportValue shared-vpc`, `"!ImportValue shared-vpc"`},
		{"inside a list", `[!Ref A, plain]`, `["!Ref A", plain]`},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			bare := normalizedValue(t, row.bare)
			quoted := normalizedValue(t, row.quoted)
			if !reflect.DeepEqual(bare, quoted) {
				t.Errorf("bare and quoted differ:\nbare:   %#v\nquoted: %#v", bare, quoted)
			}
		})
	}

	for _, row := range []struct {
		name, value string
		want        any
	}{
		{"a value opening with a mark that names no short form", `"!NotATag x"`, "!NotATag x"},
		{"a short form's text after other text", `"see !Ref A"`, "see !Ref A"},
		{"plain text", `"orders.internal"`, "orders.internal"},
		{"a YAML tag of YAML's own", `"!!str 5"`, "!!str 5"},
		{"an Sub's template text", `!Sub "!Ref A"`, map[string]any{"Fn::Sub": "!Ref A"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizedValue(t, row.value); !reflect.DeepEqual(got, row.want) {
				t.Errorf("normalized to %#v, want %#v", got, row.want)
			}
		})
	}
}

// normalizedValue normalizes a one-resource fragment whose single property holds
// value as written, and returns the property as the normalized JSON reads it.
func normalizedValue(t *testing.T, value string) any {
	t.Helper()
	fragment := "Resources:\n  Task:\n    Type: AWS::ECS::TaskDefinition\n    Properties:\n      Value: " + value + "\n"
	out, err := NormalizeFragment([]byte(fragment))
	if err != nil {
		t.Fatalf("NormalizeFragment(%q): %v", value, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("decode the normalized fragment: %v", err)
	}
	return doc["Resources"].(map[string]any)["Task"].(map[string]any)["Properties"].(map[string]any)["Value"]
}
