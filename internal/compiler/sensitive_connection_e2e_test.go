// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that the attachments written on a
// connection are withheld on the per-pass trace by their own Feature's
// declarations, the same declarations that govern that Feature attached to a node
// directly.
//
// The consuming node declares nothing sensitive and neither of its slots does.
// The connection carries a credential of the same Feature the node also holds
// itself, so the fixture puts a connection's declared data, its undeclared data
// and the node's own attachment side by side in one build.

package compiler_test

import (
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

const connectionArtifactPath = "dist/connection/compose/docker-compose.yaml"

const (
	// The connection attachment's declared values leaf.
	connectionPassword = "conn-password-plaintext"
	// The connection attachment's resolved provides value under a key its Feature
	// declares sensitive.
	connectionProvided = "s3cr3t-from-feature"
	// The connection attachment's undeclared values leaf, published again as its
	// undeclared provides key.
	connectionLocator = "conn-locator-public"
	// The node's own attachment of the same Feature.
	ownPassword = "own-password-plaintext"
	ownLocator  = "own-locator-public"
)

// connectionUsesSlots are the consumer's trace slots that project .context.uses,
// and with it the connection view.
var connectionUsesSlots = []string{
	"04-inject-mapping.yaml",
	"06-source-locals.yaml",
	"08-workload-synthesis.yaml",
	"09b-feature-rendering-boundary.yaml",
	"10-engine-synthesis/app.compose-fragment.yaml",
}

// connectionWorkloadSlots are the consumer's trace slots that project the rendered
// .workload document.
var connectionWorkloadSlots = []string{
	"09b-feature-rendering-boundary.yaml",
	"10-engine-synthesis/app.compose-fragment.yaml",
}

func connectionBuild(t *testing.T) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/sensitive-redaction/connection-release.yaml",
		CatalogPaths: []string{"testdata/sensitive-redaction/catalog/base"},
		IncludeTrace: true,
	})
}

func connectionSlot(t *testing.T, result *pipeline.Result, slot string) string {
	t.Helper()
	return traceEntry(t, result, "boundaries/connection/app/"+slot)
}

// connectionViewLeaf decodes one trace slot and reads one scalar under the
// connection view, by its path below .context.uses.storeEdge.features.credential.
// Decoding rather than searching the text keeps each assertion about the field it
// names: the same value also reaches the workload projection in the same slot.
func connectionViewLeaf(t *testing.T, body string, path ...string) (string, bool) {
	t.Helper()
	var decoded map[string]any
	if err := yaml.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode trace slot: %v\n--- slot ---\n%s", err, body)
	}
	var node any = decoded["context.uses"]
	for _, segment := range append([]string{"storeEdge", "features", "credential"}, path...) {
		m, ok := node.(map[string]any)
		if !ok {
			return "", false
		}
		node, ok = m[segment]
		if !ok {
			return "", false
		}
	}
	text, ok := node.(string)
	return text, ok
}

// TestSensitiveRedaction_TraceWithholdsAConnectionAttachmentsDeclaredData is the
// per-key assertion over every slot that shows the connection view. Each row is
// one field of the connection's attachment and what that slot must show for it.
func TestSensitiveRedaction_TraceWithholdsAConnectionAttachmentsDeclaredData(t *testing.T) {
	t.Parallel()
	result := connectionBuild(t)

	rows := []struct {
		name string
		path []string
		want string
	}{
		{"declared values leaf", []string{"values", "literalPassword"}, redact.Placeholder()},
		{"declared provides key", []string{"provides", "password"}, redact.Placeholder()},
		{"undeclared values leaf", []string{"values", "secretName"}, connectionLocator},
		{"undeclared provides key", []string{"provides", "name"}, connectionLocator},
		{"the attachment's type", []string{"type"}, "feature.test/db-credential"},
	}

	for _, slot := range connectionUsesSlots {
		body := connectionSlot(t, result, slot)
		for _, r := range rows {
			got, ok := connectionViewLeaf(t, body, r.path...)
			if !ok {
				t.Errorf("%s: %s is absent from the connection view; the assertion would be vacuous\n--- slot ---\n%s", slot, r.name, body)
				continue
			}
			if got != r.want {
				t.Errorf("%s: %s = %q, want %q", slot, r.name, got, r.want)
			}
		}
		// The withheld data appears nowhere else in the slot's pillar projection.
		if strings.Contains(body, "literalPassword: "+connectionPassword) || strings.Contains(body, "password: "+connectionProvided) {
			t.Errorf("%s still shows a connection attachment's declared datum\n--- slot ---\n%s", slot, body)
		}
		// The node's own attachment is not part of the per-pass projection: its
		// data is absent rather than withheld, exactly as before a connection could
		// carry an attachment.
		for _, own := range []string{ownPassword, ownLocator} {
			if strings.Contains(body, own) {
				t.Errorf("%s shows the node's own attachment datum %q; the projection gained a surface that needs its own withholding\n--- slot ---\n%s", slot, own, body)
			}
		}
	}
}

// TestSensitiveRedaction_TraceShowsTheWorkloadWithConnectionDataSubstituted is the
// document-scale assertion: a workload template reading the connection's declared
// datum is shown as the announced re-render, with the datum substituted and every
// undeclared datum beside it kept.
func TestSensitiveRedaction_TraceShowsTheWorkloadWithConnectionDataSubstituted(t *testing.T) {
	t.Parallel()
	result := connectionBuild(t)

	rows := []struct {
		path string
		want string
	}{
		{"containers.main.variables.CONN_PASSWORD", redact.Placeholder()},
		{"containers.main.variables.CONN_PROVIDED", redact.Placeholder()},
		{"containers.main.variables.CONN_LOCATOR", connectionLocator},
		{"containers.main.variables.CONN_NAME", connectionLocator},
		{"containers.main.variables.APP_NAME", "conndemo"},
	}

	for _, slot := range connectionWorkloadSlots {
		body := connectionSlot(t, result, slot)
		if !strings.Contains(body, redact.SubstitutedProjection()) {
			t.Errorf("%s must announce that its document is a re-render\n--- slot ---\n%s", slot, body)
		}
		leaves := workloadLeaves(t, body)
		for _, r := range rows {
			if got := leaves[r.path]; got != r.want {
				t.Errorf("%s: %s = %q, want %q", slot, r.path, got, r.want)
			}
		}
		for path, text := range leaves {
			for _, plaintext := range []string{connectionPassword, connectionProvided} {
				if strings.Contains(text, plaintext) {
					t.Errorf("%s: %s leaked %q", slot, path, plaintext)
				}
			}
		}
	}
}

// TestSensitiveRedaction_ConnectionArtifactCarriesPlaintext is the boundary's other
// half: every value the trace withholds reaches the deployable in the clear.
func TestSensitiveRedaction_ConnectionArtifactCarriesPlaintext(t *testing.T) {
	t.Parallel()
	result := connectionBuild(t)

	body, ok := result.Artifacts[connectionArtifactPath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		t.Fatalf("expected the merged compose artifact at %q; got %v", connectionArtifactPath, paths)
	}
	artifact := string(body)
	for _, want := range []string{connectionPassword, connectionProvided, connectionLocator} {
		if !strings.Contains(artifact, want) {
			t.Errorf("the rendered artifact must carry %q verbatim\n--- artifact ---\n%s", want, artifact)
		}
	}
	if strings.Contains(artifact, redact.Placeholder()) {
		t.Errorf("the rendered artifact must never contain the redaction placeholder\n--- artifact ---\n%s", artifact)
	}
}
