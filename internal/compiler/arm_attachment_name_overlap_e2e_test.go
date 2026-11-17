// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof of how an attached Feature's fragment joins its host's ARM
// template: its resource lands in the host's template as a contributor of its
// own, and its outputs are scoped to the host component rather than to the
// attachment, so a host and its attachment share one output namespace.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const armAttachmentsDir = "testdata/arm-attachments/"

// TestArmAttachmentFragmentJoinsItsHostsTemplate builds a datastore carrying one
// attachment and asserts the attachment's fragment is carried by the host's
// template alone, that its outputs sit under the host component's name beside the
// host's own, and that the attachment's resource names itself as its own
// contributor.
//
// Both expected output names are written out in full rather than composed here
// from the basis and the entry, so a rename of the derivation cannot move the
// assertion with it. The two entries carry different keys, so the set can tell
// which contributor each came from: the host's own `host`, the attachment's
// `setting`.
func TestArmAttachmentFragmentJoinsItsHostsTemplate(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armInput(armAttachmentsDir+"release.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")
	t.Logf("assembled template:\n%s", raw)

	outputs := armSection(t, template, "outputs")
	if got, want := sortedAnyKeys(outputs), []string{"store__host", "store__setting"}; !equalStringSlice(got, want) {
		t.Fatalf("the template declares outputs %v, want exactly %v: the attachment's output sits under the host component's name", got, want)
	}
	if got, want := armSection(t, outputs, "store__setting")["value"], "[reference('StoreDiag', '2021-05-01-preview').name]"; got != want {
		t.Errorf("the attachment's output reads %#v, want %q: the name and the body travel separately", got, want)
	}

	if got, want := sortedAnyKeys(armSection(t, template, "resources")), []string{"Store", "StoreDiag"}; !equalStringSlice(got, want) {
		t.Fatalf("the template declares resources %v, want exactly %v", got, want)
	}
	block, ok := armProvenanceOf(armResourceBody(t, template, "StoreDiag"))
	if !ok || block["aggrega.ai/component-id"] != "cfr--store--diag" {
		t.Errorf("the attachment's resource carries the provenance %#v, want its own contributor identity \"cfr--store--diag\"", block)
	}

	// The fragment reached the template and nothing beside it: a build that merged
	// the fragment and also wrote it out would deliver the content twice.
	var withResource []string
	for _, p := range sortedKeys(result.Artifacts) {
		if strings.Contains(string(result.Artifacts[p]), "diagnosticSettings") {
			withResource = append(withResource, p)
		}
	}
	if len(withResource) != 1 {
		t.Errorf("the attachment's resource must be carried by the assembled template alone; it appears in %v", withResource)
	}
	for _, p := range withResource {
		if strings.Contains(p, "features/") {
			t.Errorf("a discrete attachment file still carries the attachment's resource at %q: the fragment was merged and written both", p)
		}
	}
}

// TestArmAttachmentBuildIsDeterministic asserts the attachment build is
// byte-identical across repeated runs.
func TestArmAttachmentBuildIsDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, armInput(armAttachmentsDir+"release.yaml"))
}
