// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package envelope

import (
	"strings"
	"testing"
)

// TestARenderedPositionCountsItsColumnInCharacters pins the column the rendered
// message prints for a finding whose key follows a multi-byte character on its
// line. yaml.v3 counts a node's column in characters and the message prints
// that count, so a two-byte and a four-byte character before the key each take
// one column, as an ASCII character does. A position counted in bytes would
// print 3:54 on the two-byte line and 3:53 on the four-byte one.
//
// The characters are written as escapes so the source spells exactly one code
// point each: a precomposed character typed into an editor can be saved
// decomposed, as two code points, which would move the column for a reason this
// test is not about.
func TestARenderedPositionCountsItsColumnInCharacters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		description string
		want        string
	}{
		{name: "an ASCII line", description: "Cafe API", want: "; 3:53: metadata.version: "},
		{name: "a two-byte character before the key", description: "Café API", want: "; 3:53: metadata.version: "},
		{name: "a four-byte character before the key", description: "\U0001F680 API", want: "; 3:50: metadata.version: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := "apiVersion: aggrega.ai/v1beta1\nkind: Component\n" +
				`metadata: {name: core/bad, description: "` + tc.description + `", version: "bogus", ` +
				"labels: {component.aggrega.ai/type: server}}\nspec: {}\n"
			_, err := ValidateBytes(KindComponent, []byte(doc), "component.yaml")
			if err == nil {
				t.Fatalf("a version that is not a semantic version was accepted:\n%s", doc)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the rendered message does not print the version key at its character column (want %q):\n%s",
					tc.want, err.Error())
			}
		})
	}
}
