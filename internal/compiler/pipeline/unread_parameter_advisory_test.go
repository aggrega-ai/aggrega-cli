// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// declareUnreadParameter returns the aggregate bytes with one optional
// parameter declaration spliced in that no reference reads. The splice is
// in-memory only: the fixture on disk is shared with other proofs and must keep
// producing exactly what they expect.
func declareUnreadParameter(t *testing.T, aggBytes []byte) []byte {
	t.Helper()
	const anchor = "spec:\n"
	const declaration = "spec:\n" +
		"  parameters:\n" +
		"    unread_probe:\n" +
		"      type: string\n"
	if !bytes.Contains(aggBytes, []byte(anchor)) {
		t.Fatalf("fixture no longer carries the %q anchor; the splice would be silently inert", anchor)
	}
	out := bytes.Replace(aggBytes, []byte(anchor), []byte(declaration), 1)
	if bytes.Equal(out, aggBytes) {
		t.Fatal("splice changed nothing; the probe parameter would not be declared")
	}
	return out
}

// TestUnreadParameterAdvisoryReachesTheDiagnosticStream is the user-visible
// proof for the declared-parameter reading: an Aggregate declaring a parameter
// no reference reads produces an advisory line on the process diagnostic stream,
// through the same drain the catalog-aware advisories use.
//
// It also pins the drain against a specific silent regression. The advisories a
// resolve computes for itself arrive on the resolve result already populated, and
// the catalog-aware producer here must EXTEND that slice. Were it to assign over
// it, this fixture — which yields no catalog-aware advisory of its own — would
// print nothing at all, and the reading would be lost with every test still
// green. So the assertion below is what stands between an append and an
// assignment.
func TestUnreadParameterAdvisoryReachesTheDiagnosticStream(t *testing.T) {
	rel, err := resolver.LoadRelease(fullstackReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fullstackManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(fullstackAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.LoadAll("", fullstackCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}

	inputs := pipeline.Inputs{
		Release:           rel,
		Manifest:          manifest,
		AggregateRawBytes: declareUnreadParameter(t, aggBytes),
		AggregatePath:     fullstackAggregatePath,
		WorkspaceConfig:   workspaceconfig.Default(),
		CompilerVersion:   fixtureCompilerVersion,
	}

	var advisories []*resolver.ResolverError
	stderr := captureStderr(t, func() {
		if rerr := pipeline.ResolveAndValidate(context.Background(), inputs,
			wiredLookup(cat, manifest),
			func(rr *resolver.ResolveResult) error {
				advisories = rr.LintAdvisories
				return nil
			},
		); rerr != nil {
			t.Errorf("ResolveAndValidate: %v", rerr)
		}
	})

	if len(advisories) == 0 {
		t.Fatal("resolve carried no advisories; the declared-parameter reading did not survive to the drain")
	}
	if !strings.Contains(stderr, "unread_probe") {
		t.Errorf("diagnostic stream does not name the unread parameter.\nstderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "lint:") {
		t.Errorf("advisory did not reach the advisory drain.\nstderr:\n%s", stderr)
	}

	// Control: the same run over the UNSPLICED fixture must print no such line,
	// or the assertion above would pass on unrelated output.
	inputs.AggregateRawBytes = aggBytes
	clean := captureStderr(t, func() {
		if rerr := pipeline.ResolveAndValidate(context.Background(), inputs,
			wiredLookup(cat, manifest),
			func(*resolver.ResolveResult) error { return nil },
		); rerr != nil {
			t.Errorf("ResolveAndValidate (control): %v", rerr)
		}
	})
	if strings.Contains(clean, "unread_probe") {
		t.Errorf("control run named the probe parameter; the fixture is not clean.\nstderr:\n%s", clean)
	}
}
