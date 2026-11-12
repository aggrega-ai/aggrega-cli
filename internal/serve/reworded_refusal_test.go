// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateRendersARewordedRefusalWithoutTheDecodersText is the invocation
// proof that the editor's diagnostics carry a refusal reworded in the author's
// words and stop there: a Recipe whose Workload writes a port as text, checked in
// its Release's terms over the real framing, answers with the sentence naming the
// key, the line and the two shapes, and with nothing of the decoder's own report —
// which a diagnostic rendering every cause beneath the refusal would otherwise
// print after it.
func TestValidateRendersARewordedRefusalWithoutTheDecodersText(t *testing.T) {
	t.Parallel()
	srv, root := entityCheckWorkspace(t)
	recipe := filepath.Join(root, "variant-recipe", "recipe", "helm", "service", "1.0.0", "recipe.yaml")
	content := mustRead(t, recipe)
	edited := strings.Replace(content, "          http: 8080\n", "          http: abc\n", 1)
	if edited == content {
		t.Fatal("the fixture Recipe no longer has the port this test rewrites, so the row would check a clean Recipe")
	}
	res := validateContentOverTheWire(t, srv, recipe, edited, "release-recipe.yaml")
	if len(res.Diagnostics) == 0 {
		t.Fatal("a port written as text drew no diagnostic")
	}
	joined := strings.Join(diagMessages(res.Diagnostics), "\n")
	const want = "text `abc` is written where a number is expected; a port is a number, or a map of port and protocol"
	if !strings.Contains(joined, want) {
		t.Errorf("no diagnostic says %q:\n%s", want, joined)
	}
	for _, leak := range []string{"cannot unmarshal", "unmarshal errors", "!!str"} {
		if strings.Contains(joined, leak) {
			t.Errorf("a diagnostic carries the decoder's own text %q after the reworded sentence:\n%s", leak, joined)
		}
	}
}
