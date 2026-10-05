// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The anchor a broken artifact names, which a YAML parser quotes back when it
// refuses the content.
const plantedAnchor = "PLANTED-ANCHOR"

// TestSpliceArtifactProvenance_AMarkedEntryWithholdsTheParsersText places one
// entry per row with the entry's marking off and on. A parse failure quotes the
// content when unmarked and carries the withheld sentence when marked, under the
// same frame and category; content that parses, and content an alternate-delimiter
// guest passes through, are placed byte for byte the same either way, because the
// marking only ever reaches text a failure composes.
func TestSpliceArtifactProvenance_AMarkedEntryWithholdsTheParsersText(t *testing.T) {
	t.Parallel()
	labels := map[string]string{"aggrega.ai/component-id": "api"}
	const frame = `artifact "cm.yaml" template output is not valid YAML`

	t.Run("a parse failure quotes the content unmarked and withholds it marked", func(t *testing.T) {
		t.Parallel()
		broken := []byte("reference: *" + plantedAnchor + "\n")
		_, _, plainErr := SpliceArtifactProvenance("cm.yaml", broken, labels, nil, true, 2, false)
		_, _, markedErr := SpliceArtifactProvenance("cm.yaml", broken, labels, nil, true, 2, true)
		if plainErr == nil || markedErr == nil {
			t.Fatalf("the broken content placed: unmarked %v, marked %v", plainErr, markedErr)
		}
		plain, marked := FailureOf(plainErr).Message, FailureOf(markedErr).Message
		if !strings.Contains(plain, frame+": yaml: unknown anchor '"+plantedAnchor+"' referenced") {
			t.Fatalf("the unmarked failure does not quote the content, so this row proves nothing: %s", plain)
		}
		if !strings.Contains(marked, frame+": "+artifactParseWithheld) {
			t.Errorf("the marked failure does not withhold the parser's text: %s", marked)
		}
		if strings.Contains(marked, plantedAnchor) {
			t.Errorf("the marked failure carries the content: %s", marked)
		}
		var plainCE, markedCE *cerrors.CompilerError
		if !errors.As(plainErr, &plainCE) || !errors.As(markedErr, &markedCE) || plainCE.Category() != markedCE.Category() {
			t.Errorf("the marked failure's category moved: unmarked %v, marked %v", plainErr, markedErr)
		}
	})

	t.Run("content that parses is placed identically marked and unmarked", func(t *testing.T) {
		t.Parallel()
		doc := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  x: \"1\"\n")
		plain, plainWarn, plainErr := SpliceArtifactProvenance("cm.yaml", doc, labels, nil, true, 2, false)
		marked, markedWarn, markedErr := SpliceArtifactProvenance("cm.yaml", doc, labels, nil, true, 2, true)
		if plainErr != nil || markedErr != nil {
			t.Fatalf("placement failed: unmarked %v, marked %v", plainErr, markedErr)
		}
		if !bytes.Equal(plain, marked) || plainWarn != markedWarn {
			t.Errorf("the marking moved the placed bytes:\nunmarked: %s\n  marked: %s", plain, marked)
		}
	})

	t.Run("content an alternate-delimiter guest passes through is skipped identically", func(t *testing.T) {
		t.Parallel()
		broken := []byte("metadata:\n  labels:\n    {{- include \"x\" . }}\n")
		plain, plainWarn, plainErr := SpliceArtifactProvenance("cm.yaml", broken, labels, nil, false, 2, false)
		marked, markedWarn, markedErr := SpliceArtifactProvenance("cm.yaml", broken, labels, nil, false, 2, true)
		if plainErr != nil || markedErr != nil {
			t.Fatalf("a pass-through refused: unmarked %v, marked %v", plainErr, markedErr)
		}
		if !bytes.Equal(plain, marked) || plainWarn != markedWarn || plainWarn == "" {
			t.Errorf("the pass-through moved: warnings %q / %q", plainWarn, markedWarn)
		}
	})
}

// TestArtifactNotYAML_FramesEveryPlacerAlike pins the one refusal both placers
// raise: the provider's word prefixes the frame where it has one, and the
// marking replaces the cause and nothing else.
func TestArtifactNotYAML_FramesEveryPlacerAlike(t *testing.T) {
	t.Parallel()
	cause := errors.New("yaml: unknown anchor '" + plantedAnchor + "' referenced")
	cases := []struct {
		name     string
		provider string
		marked   bool
		want     string
	}{
		{"no provider word, unmarked", "", false, `artifact "a.yaml" template output is not valid YAML: ` + cause.Error()},
		{"no provider word, marked", "", true, `artifact "a.yaml" template output is not valid YAML: ` + artifactParseWithheld},
		{"a provider word, unmarked", "compose", false, `compose: artifact "a.yaml" template output is not valid YAML: ` + cause.Error()},
		{"a provider word, marked", "compose", true, `compose: artifact "a.yaml" template output is not valid YAML: ` + artifactParseWithheld},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cerrors.CauseChain(ArtifactNotYAML(tc.provider, "a.yaml", cause, tc.marked))
			if !strings.Contains(got, tc.want) {
				t.Errorf("refusal:\n got: %s\nwant it to carry: %s", got, tc.want)
			}
			if tc.marked && strings.Contains(got, plantedAnchor) {
				t.Errorf("the marked refusal carries the content: %s", got)
			}
		})
	}
}
