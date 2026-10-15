// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package yamlpos

import (
	"testing"
)

// TestKeyRange_ReportsAKeyOnlyWhereTheDocumentCarriesIt pins the one difference
// from FieldRange that the function exists for: a key the document lacks is
// reported as missing, never as the enclosing key the walk reached.
func TestKeyRange_ReportsAKeyOnlyWhereTheDocumentCarriesIt(t *testing.T) {
	t.Parallel()
	const content = "# a comment\napiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  version: \"1.0.0\"\nspec: [1, 2]\n"
	doc := parseFieldDoc(t, content)
	idx := BuildLineIndexWithEncoding([]byte(content), PositionEncodingUTF8)

	for _, row := range []struct {
		name string
		keys []string
		want FieldSpan
		ok   bool
	}{
		// "kind" sits on line 3 from column 1 and is 4 characters long.
		{name: "a top-level key spans its token", keys: []string{"kind"},
			want: FieldSpan{Line: 3, Column: 1, EndLine: 3, EndColumn: 5}, ok: true},
		// "version" sits on line 5 from column 3 and is 7 characters long.
		{name: "a nested key spans its token", keys: []string{"metadata", "version"},
			want: FieldSpan{Line: 5, Column: 3, EndLine: 5, EndColumn: 10}, ok: true},
		{name: "a missing nested key is missing, not its enclosing key", keys: []string{"metadata", "name"}},
		{name: "a missing top-level key is missing", keys: []string{"name"}},
		{name: "a key beneath a value that is not a mapping is missing", keys: []string{"spec", "0"}},
		{name: "no keys name nothing", keys: nil},
	} {
		span, ok := KeyRange(doc, idx, row.keys...)
		if ok != row.ok || span != row.want {
			t.Errorf("%s: KeyRange(%q) = %+v, %v; want %+v, %v", row.name, row.keys, span, ok, row.want, row.ok)
		}
	}

	if span, ok := KeyRange(nil, idx, "kind"); ok || span != (FieldSpan{}) {
		t.Errorf("no document carries no key; got %+v, %v", span, ok)
	}
	// FieldRange, by contrast, settles on the enclosing key, which is the case
	// KeyRange must not reproduce.
	if span, ok := FieldRange(doc, idx, "metadata.name"); !ok || span.Line != 4 {
		t.Errorf("FieldRange no longer settles on the enclosing key (%+v, %v), so the contrast above proves nothing", span, ok)
	}
}
