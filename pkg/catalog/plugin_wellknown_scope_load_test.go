// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// providerEntity renders a provider Plugin entity declaring one well-known
// annotation at scopes, against an input-kind claim of sourced only.
func providerEntity(scopes string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: probe
  pluginType: provider
  version: 1.0.0
spec:
  abiVersion: v1beta1
  provider:
    capabilities:
      - probe
    wellKnownAnnotations:
      - key: aggrega.ai/probe
        purpose: A probe key.
        scope:
` + scopes + `    contract:
      inputKinds:
        sourced: true
`
}

// TestLoad_Plugin_ContradictoryAnnotationScopeRejectedAtLoad drives a violating
// entity through the real catalog load rather than calling the check directly.
// That is the property worth pinning: the rejection has to reach an author
// walking a catalog, which is the surface the over-claim was authored on, and a
// check that fires only where a plugin binary is loaded would miss it entirely.
func TestLoad_Plugin_ContradictoryAnnotationScopeRejectedAtLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(path, []byte(providerEntity("          - synthesized\n")), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadAll("", dir)
	if err == nil {
		t.Fatal("expected a rejection for a scope the contract does not render; got nil")
	}
	if !errors.Is(err, types.ErrWellKnownAnnotationScope()) {
		t.Fatalf("rejection does not carry the halting condition: %v", err)
	}
	// Read through the cause chain rather than the outermost frame. A catalog
	// load renders one frame per level by design, so the outermost says only
	// that a load failed; the frame naming the fault is beneath it, and the
	// flattened form is what a single-line context shows.
	got := cerrors.CauseChain(err)
	for _, want := range []string{"probe", "aggrega.ai/probe", "contract.inputKinds.synthesized"} {
		if !strings.Contains(got, want) {
			t.Errorf("rejection does not name %q: %s", want, got)
		}
	}
}

// TestLoad_Plugin_ConsistentAnnotationScopeAccepted is the positive control for
// the test above. Without it a rejection could come from any unrelated fault in
// the fixture and the negative test would keep passing while covering nothing.
func TestLoad_Plugin_ConsistentAnnotationScopeAccepted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(path, []byte(providerEntity("          - sourced\n")), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("a declaration consistent with its own contract was rejected: %v", err)
	}
	if len(c.Plugins) != 1 {
		t.Fatalf("indexed %d plugins, want 1", len(c.Plugins))
	}
}

// TestPlugin_EnvelopeRejectsAnUnknownAnnotationScope covers the other half of
// the enforcement split, and it is a different gate from the one above: the
// value space is closed by the published schema, so a scope outside the four is
// refused structurally before any Go check reads it. A test asserting only the
// Go check would leave the schema half unproven.
func TestPlugin_EnvelopeRejectsAnUnknownAnnotationScope(t *testing.T) {
	t.Parallel()

	if _, err := envelope.ValidateBytes(envelope.KindPlugin, []byte(providerEntity("          - sourced\n")), "plugin.yaml"); err != nil {
		t.Fatalf("positive control: a declared scope was rejected by the envelope: %v", err)
	}
	_, err := envelope.ValidateBytes(envelope.KindPlugin, []byte(providerEntity("          - whenever\n")), "plugin.yaml")
	if err == nil {
		t.Fatal("expected the envelope to refuse a scope outside the declared value space")
	}
}
