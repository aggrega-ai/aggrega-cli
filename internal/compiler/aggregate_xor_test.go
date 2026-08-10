// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// TestAggregateXOR_BothScopesRejected pins the Aggregate components/subsystems
// mutual-exclusivity at the message level (not only the envelope sentinel): an
// Aggregate declaring BOTH a non-empty top-level components map AND named
// subsystems is rejected at the document-envelope gate by the schema's
// not/allOf XOR, through the real build path.
func TestAggregateXOR_BothScopesRejected(t *testing.T) {
	t.Parallel()
	wf := baseWorkspaceFiles()
	wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
  subsystems:
    extra:
      components:
        worker:
          type: core/server
`
	in := writeMinimalWorkspace(t, wf)
	_, err := pipelinetest.BuildE(t, in)
	if err == nil {
		t.Fatal("both components and subsystems non-empty must halt at the envelope XOR")
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
	}
	// Message-level tightening: pin that the spec-root exclusion is what
	// rejected it — not some other envelope rule (additionalProperties / required
	// / type). The exclusion reports generically at `spec`, and it is the only
	// spec-root exclusion in the Aggregate schema, so this substring uniquely
	// identifies the components/subsystems XOR. An author-friendly "components
	// and subsystems are mutually exclusive" wording would require a
	// supplemental compiler-guard; the document-envelope reject is the
	// enforcement of record.
	msg := err.Error()
	if !strings.Contains(msg, "is not allowed here") {
		t.Errorf("diagnostic must identify the spec-level exclusion violation; got %q", msg)
	}
	if !strings.Contains(msg, "spec") {
		t.Errorf("diagnostic must point at spec; got %q", msg)
	}
}
