// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof of where an ARM template carries the build's provenance: on
// each resource the template deploys, in the resource's own metadata object,
// whichever origin supplied the resource, and never on the template as a whole.
//
// A resource Resource Manager only reads (an existing one) is deployed by
// nobody, so it carries nothing; a fragment that deploys no resource at all
// reports that its provenance had nothing to describe.

package compiler_test

import (
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

const (
	armProvenanceDir     = "testdata/arm-provenance/"
	armProvenanceCatalog = armProvenanceDir + "catalog"
)

func armProvenanceInput(release string) pipelinetest.Input {
	return armInput(armProvenanceDir+release, armProvenanceCatalog)
}

// TestArmProvenanceOnEveryResource builds a template whose resources come from
// a Component's Recipe, from a component-scope attachment and from a
// boundary-scope attachment, and asserts every one of them carries the same
// provenance keys in its own metadata object, each naming the contributor that
// supplied the resource.
//
// The expected component identities are written out in full: a Feature's
// resource is its own contributor, named by its host and its attachment, so its
// identity differs from the host's and the stamp has to say so.
func TestArmProvenanceOnEveryResource(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armProvenanceInput("release.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")
	t.Logf("assembled template:\n%s", raw)

	if _, present := template["metadata"]; present {
		t.Errorf("the template carries a top-level metadata object; provenance belongs on each resource\n--- template ---\n%s", raw)
	}

	wantIdentity := map[string]string{
		"Store":         "store",
		"App":           "app",
		"StoreDiag":     "cfr--store--diag",
		"StackSettings": "bfr--stack--settings",
	}
	resources := armSection(t, template, "resources")
	if got := sortedAnyKeys(resources); len(got) != len(wantIdentity) {
		t.Fatalf("the template declares resources %v, want one for each origin %v", got, wantIdentity)
	}

	var reference map[string]any
	for symbol, identity := range wantIdentity {
		block, ok := armProvenanceOf(armResourceBody(t, template, symbol))
		if !ok {
			t.Errorf("resource %s carries no provenance in its metadata\n--- template ---\n%s", symbol, raw)
			continue
		}
		if block["aggrega.ai/component-id"] != identity {
			t.Errorf("resource %s carries the component identity %#v, want %q", symbol, block["aggrega.ai/component-id"], identity)
		}
		if block["aggrega.ai/provider"] != "arm" || block["aggrega.ai/boundary-path"] != "stack" {
			t.Errorf("resource %s carries provider %#v and boundary %#v, want \"arm\" and \"stack\"",
				symbol, block["aggrega.ai/provider"], block["aggrega.ai/boundary-path"])
		}
		// Every resource carries the same set of keys whichever origin supplied it;
		// only the values naming the contributor differ.
		if reference == nil {
			reference = block
			continue
		}
		if len(block) != len(reference) {
			t.Errorf("resource %s carries %d provenance keys, another resource carries %d", symbol, len(block), len(reference))
		}
		for key := range reference {
			if _, ok := block[key]; !ok {
				t.Errorf("resource %s lacks the provenance key %q another resource carries", symbol, key)
			}
		}
	}
}

// TestArmProvenanceSkipsExistingResources builds a fragment that deploys one
// resource and reads an existing one. The deployed resource carries provenance;
// the existing one is emitted exactly as authored, with no metadata, and the
// build says nothing about it because the provenance still had a resource to
// describe.
func TestArmProvenanceSkipsExistingResources(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armProvenanceInput("release-existing.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")

	if _, ok := armProvenanceOf(armResourceBody(t, template, "Alpha")); !ok {
		t.Errorf("the deployed resource carries no provenance\n--- template ---\n%s", raw)
	}
	vault := armResourceBody(t, template, "SharedVault")
	if _, present := vault["metadata"]; present {
		t.Errorf("the existing resource carries metadata %#v; nothing deploys it, so it carries no provenance", vault["metadata"])
	}
	if vault["existing"] != true {
		t.Errorf("the existing resource lost its existing marker: %#v", vault)
	}
	for _, d := range result.Advisories {
		if d.Code == rendercore.ArtifactProvenanceSkippedCode {
			t.Errorf("an advisory reports provenance skipped though one resource was stamped: %+v", d)
		}
	}
}

// TestArmFragmentWithoutResourcesAdvises builds a fragment that declares a
// parameter and no resource. The build completes, the parameter reaches the
// template, and the author is told through the provenance-skipped warning that
// the provenance had no resource to describe.
func TestArmFragmentWithoutResourcesAdvises(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armProvenanceInput("release-no-resources.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")

	if got := sortedAnyKeys(armSection(t, template, "parameters")); !equalStringSlice(got, []string{"location"}) {
		t.Errorf("the template carries the parameters %v, want exactly [location]\n--- template ---\n%s", got, raw)
	}
	if got := armSection(t, template, "resources"); len(got) != 0 {
		t.Errorf("the template carries resources %v from a fragment that declares none", mapKeys(got))
	}

	var skipped []string
	for _, d := range result.Advisories {
		if d.Code != rendercore.ArtifactProvenanceSkippedCode {
			continue
		}
		skipped = append(skipped, d.Message)
		if d.Severity != abi.SeverityWarn {
			t.Errorf("the provenance-skipped advisory is %q-severity, want warn", d.Severity)
		}
	}
	if len(skipped) != 1 {
		t.Fatalf("want exactly one provenance-skipped advisory; got %v (all advisories %+v)", skipped, result.Advisories)
	}
	t.Logf("advisory: %s", skipped[0])
}

// TestArmProvenanceIsDeterministic asserts stamping provenance on every origin's
// resources assembles the same bytes across repeated runs.
func TestArmProvenanceIsDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, armProvenanceInput("release.yaml"))
}
