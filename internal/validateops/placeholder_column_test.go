// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validateops

import (
	"errors"
	"strings"
	"testing"
)

// TestAPrintedPlaceholderCountsItsColumnInCharacters pins the column the
// single-entity refusal prints for a placeholder whose key follows a multi-byte
// character on its line. yaml.v3 counts a node's column in characters and the
// refusal prints that count, so a two-byte and a four-byte character before the
// key each take one column, as an ASCII character does. A position counted in
// bytes would print 3:56 on the two-byte line and 3:55 on the four-byte one.
//
// The document's only envelope finding is the one at the placeholder's key,
// which the placeholder line replaces, so the one position the refusal prints
// is the reading's own.
//
// The characters are written as escapes so the source spells exactly one code
// point each: a precomposed character typed into an editor can be saved
// decomposed, as two code points, which would move the column for a reason this
// test is not about.
func TestAPrintedPlaceholderCountsItsColumnInCharacters(t *testing.T) {
	t.Parallel()
	const takes = " metadata.version takes a semantic version, such as 1.0.0"
	cases := []struct {
		name        string
		description string
		want        string
	}{
		{name: "an ASCII line", description: "Cafe API", want: "\n    3:55:" + takes},
		{name: "a two-byte character before the key", description: "Café API", want: "\n    3:55:" + takes},
		{name: "a four-byte character before the key", description: "\U0001F680 API", want: "\n    3:52:" + takes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := "apiVersion: aggrega.ai/v1beta1\nkind: Component\n" +
				`metadata: {name: core/draft, description: "` + tc.description + `", version: "", ` +
				"labels: {component.aggrega.ai/type: server}}\nspec: {}\n"
			_, err := validateDocument(t, doc)
			var refusal *placeholderError
			if !errors.As(err, &refusal) {
				t.Fatalf("the document was not refused for its placeholder: %v\n%s", err, doc)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not print the placeholder at its character column (want %q):\n%s",
					tc.want, err.Error())
			}
		})
	}
}
