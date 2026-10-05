// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// TestSpliceArtifactServiceProvenance_AMarkedFragmentWithholdsTheParsersText
// places a fragment the parser refuses with the entry's marking off and on: the
// unmarked refusal quotes the content back, the marked one names the artifact and
// withholds the parser's text. A fragment that parses is stamped byte for byte the
// same either way.
func TestSpliceArtifactServiceProvenance_AMarkedFragmentWithholdsTheParsersText(t *testing.T) {
	t.Parallel()
	const anchor = "PLANTED-ANCHOR"
	const frame = `compose: artifact "frag.yaml" template output is not valid YAML`

	broken := []byte("services:\n  a: *" + anchor + "\n")
	_, _, plainErr := SpliceArtifactServiceProvenance("frag.yaml", broken, hostLabels(), hostAnnotations(), 2, false)
	_, _, markedErr := SpliceArtifactServiceProvenance("frag.yaml", broken, hostLabels(), hostAnnotations(), 2, true)
	if plainErr == nil || markedErr == nil {
		t.Fatalf("the broken fragment placed: unmarked %v, marked %v", plainErr, markedErr)
	}
	plain, marked := rendercore.FailureOf(plainErr).Message, rendercore.FailureOf(markedErr).Message
	if !strings.Contains(plain, frame+": yaml: unknown anchor '"+anchor+"' referenced") {
		t.Fatalf("the unmarked refusal does not quote the content, so this test proves nothing: %s", plain)
	}
	if !strings.Contains(marked, frame+": the parser's text is withheld") {
		t.Errorf("the marked refusal does not withhold the parser's text: %s", marked)
	}
	if strings.Contains(marked, anchor) {
		t.Errorf("the marked refusal carries the content: %s", marked)
	}

	fragment := []byte("services:\n  db:\n    image: postgres:16\n")
	plainBody, plainWarn, plainErr := SpliceArtifactServiceProvenance("frag.yaml", fragment, hostLabels(), hostAnnotations(), 2, false)
	markedBody, markedWarn, markedErr := SpliceArtifactServiceProvenance("frag.yaml", fragment, hostLabels(), hostAnnotations(), 2, true)
	if plainErr != nil || markedErr != nil {
		t.Fatalf("placement failed: unmarked %v, marked %v", plainErr, markedErr)
	}
	if !bytes.Equal(plainBody, markedBody) || plainWarn != markedWarn {
		t.Errorf("the marking moved the placed bytes:\nunmarked: %s\n  marked: %s", plainBody, markedBody)
	}
}
