// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// hostLabels and hostAnnotations are the two halves the host computes for a node
// and hands the guest per artifact entry — the Kubernetes-shaped split, which the
// compose placer folds into one service-labels set. They carry one
// charset-unsafe value (the recipe URI's ":") and both Kubernetes-only keys, so
// every arm of the fold is exercised by every row below.
func hostLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":      "db",
		"aggrega.ai/component-id":     "db",
		"aggrega.ai/provider":         "compose",
		"aggrega.ai/compiler-version": "0.1.0",
	}
}

func hostAnnotations() map[string]string {
	return map[string]string{
		"aggrega.ai/boundary-path":     "app",
		"aggrega.ai/recipe-uri":        "compose-db-sourced:1.0.0",
		"aggrega.ai/sync-wave":         "2",
		"argocd.argoproj.io/sync-wave": "2",
	}
}

// serviceLabels decodes a compose fragment and returns the named service's
// labels map. It reads through a decode rather than matching text so an
// assertion is about the emitted document rather than about its formatting.
func serviceLabels(t *testing.T, body []byte, service string) map[string]string {
	t.Helper()
	var doc struct {
		Services map[string]struct {
			Labels map[string]string `yaml:"labels"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode emitted fragment: %v\n%s", err, body)
	}
	svc, ok := doc.Services[service]
	if !ok {
		t.Fatalf("emitted fragment declares no service %q; got %v\n%s", service, doc.Services, body)
	}
	return svc.Labels
}

// assertKeySet compares two key sets by DIFFERENCE, naming what is missing and
// what is extra. A count comparison would pass with both sides short by one,
// which is exactly the partial-splice case a presence check cannot see.
func assertKeySet(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	var missing, extra []string
	for k := range want {
		if _, ok := got[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("%s: missing keys %v", what, missing)
	}
	if len(extra) > 0 {
		t.Errorf("%s: unexpected keys %v", what, extra)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: key %q = %q, want %q", what, k, got[k], v)
		}
	}
}

// TestSpliceArtifactServiceProvenance_StampsEveryServiceWithTheFoldedSet pins the
// placer's positive contract: every service the fragment declares carries the
// folded provenance set — the two host-computed halves unioned, minus the
// Kubernetes-only keys — and no warning is returned. The expected set is computed
// through the same fold the synthesized and source-rendered services use, so the
// assertion cannot drift from what those services carry.
func TestSpliceArtifactServiceProvenance_StampsEveryServiceWithTheFoldedSet(t *testing.T) {
	t.Parallel()
	src := []byte(`services:
  db-metrics:
    image: postgres-exporter:latest
  db-backup:
    image: postgres-backup:latest
`)
	body, warning, err := SpliceArtifactServiceProvenance("db-sidecar.yaml", src, hostLabels(), hostAnnotations(), 2, false)
	if err != nil {
		t.Fatalf("SpliceArtifactServiceProvenance: %v", err)
	}
	if warning != "" {
		t.Fatalf("a fragment that declares services must return no warning, got %q", warning)
	}

	want := foldComposeProvenanceSet(hostLabels(), hostAnnotations())
	if _, present := want["app.kubernetes.io/name"]; present {
		t.Fatal("the fold must drop app.kubernetes.io/name; the expected set is not the compose set")
	}
	for _, svc := range []string{"db-metrics", "db-backup"} {
		assertKeySet(t, "service "+svc, serviceLabels(t, body, svc), want)
	}
}

// TestSpliceArtifactServiceProvenance_ServicelessFragmentWarnsRatherThanPassingSilently
// pins the never-both-silent contract from the placer's side: a fragment
// contributing only a top-level key has no service to stamp, so the bytes come
// back unchanged AND the warning is non-empty. Either half alone would be a
// silent skip.
func TestSpliceArtifactServiceProvenance_ServicelessFragmentWarnsRatherThanPassingSilently(t *testing.T) {
	t.Parallel()
	src := []byte(`volumes:
  db-archive:
    driver: local
`)
	body, warning, err := SpliceArtifactServiceProvenance("db-volume.yaml", src, hostLabels(), hostAnnotations(), 2, false)
	if err != nil {
		t.Fatalf("SpliceArtifactServiceProvenance: %v", err)
	}
	if string(body) != string(src) {
		t.Errorf("an unstampable fragment must be emitted unchanged\n got: %s\nwant: %s", body, src)
	}
	if warning == "" {
		t.Fatal("an unstampable fragment must return a warning; unstamped and unreported is the silence this placer exists to end")
	}
	if !strings.Contains(warning, "db-volume.yaml") {
		t.Errorf("the warning must name the artifact, got %q", warning)
	}
}

// TestSpliceArtifactServiceProvenance_EveryDocumentOfAStreamIsStamped pins that
// the placer walks the whole stream. A placer reading only the first document
// would stamp it, report success, and leave the rest unstamped and unreported —
// a per-document version of the same silence.
func TestSpliceArtifactServiceProvenance_EveryDocumentOfAStreamIsStamped(t *testing.T) {
	t.Parallel()
	src := []byte(`services:
  first:
    image: a:1
---
services:
  second:
    image: b:1
`)
	body, warning, err := SpliceArtifactServiceProvenance("multi.yaml", src, hostLabels(), hostAnnotations(), 2, false)
	if err != nil {
		t.Fatalf("SpliceArtifactServiceProvenance: %v", err)
	}
	if warning != "" {
		t.Fatalf("unexpected warning: %q", warning)
	}
	want := foldComposeProvenanceSet(hostLabels(), hostAnnotations())
	parts := strings.Split(string(body), "---\n")
	if len(parts) != 2 {
		t.Fatalf("expected two documents in the emitted stream, got %d:\n%s", len(parts), body)
	}
	assertKeySet(t, "document 1 service first", serviceLabels(t, []byte(parts[0]), "first"), want)
	assertKeySet(t, "document 2 service second", serviceLabels(t, []byte(parts[1]), "second"), want)
}

// TestSpliceArtifactServiceProvenance_AuthorLabelsSurviveAndProvenanceWins pins
// the merge rule at the service level: an author-set key outside the provenance
// keyspace is preserved, and a key the provenance set also carries takes the
// compiler's value.
func TestSpliceArtifactServiceProvenance_AuthorLabelsSurviveAndProvenanceWins(t *testing.T) {
	t.Parallel()
	src := []byte(`services:
  db-metrics:
    image: postgres-exporter:latest
    labels:
      tier: backend
      aggrega.ai/provider: hand-written
`)
	body, warning, err := SpliceArtifactServiceProvenance("db-sidecar.yaml", src, hostLabels(), hostAnnotations(), 2, false)
	if err != nil {
		t.Fatalf("SpliceArtifactServiceProvenance: %v", err)
	}
	if warning != "" {
		t.Fatalf("unexpected warning: %q", warning)
	}
	got := serviceLabels(t, body, "db-metrics")
	if got["tier"] != "backend" {
		t.Errorf("author-set label outside the provenance keyspace must survive; tier = %q", got["tier"])
	}
	if got["aggrega.ai/provider"] != "compose" {
		t.Errorf("provenance must win on key collision; aggrega.ai/provider = %q", got["aggrega.ai/provider"])
	}
}

// TestSpliceArtifactServiceProvenance_SequenceLabelsAreRefusedNotDiscarded pins
// the shape guardrail: compose accepts a `labels:` sequence, which provenance
// entries cannot merge into. Overwriting it would silently discard what the
// author wrote, so the placer refuses.
func TestSpliceArtifactServiceProvenance_SequenceLabelsAreRefusedNotDiscarded(t *testing.T) {
	t.Parallel()
	src := []byte(`services:
  db-metrics:
    image: postgres-exporter:latest
    labels:
      - tier=backend
`)
	_, _, err := SpliceArtifactServiceProvenance("db-sidecar.yaml", src, hostLabels(), hostAnnotations(), 2, false)
	if err == nil {
		t.Fatal("expected a refusal for a sequence-form labels block rather than a silent overwrite")
	}
	msg := err.Error()
	for _, want := range []string{"CATALOG_ERROR", "db-sidecar.yaml", "services.db-metrics", "expected mapping, got sequence"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal should carry %q, got %q", want, msg)
		}
	}
}

// TestSpliceArtifactServiceProvenance_NonYAMLIsAHardError pins the parse policy:
// a compose artifact reaches the guest already evaluated against Aggrega's own
// delimiter pair, so nothing downstream is waiting to rewrite it and a
// non-parsing body is an authoring fault rather than a shape to pass through.
func TestSpliceArtifactServiceProvenance_NonYAMLIsAHardError(t *testing.T) {
	t.Parallel()
	_, _, err := SpliceArtifactServiceProvenance("broken.yaml", []byte("this: is: malformed: [unclosed"), hostLabels(), nil, 2, false)
	if err == nil {
		t.Fatal("expected a hard error for a fragment that is not valid YAML")
	}
	if !strings.Contains(err.Error(), "not valid YAML") {
		t.Errorf("error should say the fragment is not valid YAML, got %q", err)
	}
}

// TestSpliceArtifactServiceProvenance_EmptyContentIsUnchanged pins the
// short-circuit: an empty body carries nothing to stamp and reports nothing,
// which is not the service-less case — there is no fragment at all.
func TestSpliceArtifactServiceProvenance_EmptyContentIsUnchanged(t *testing.T) {
	t.Parallel()
	body, warning, err := SpliceArtifactServiceProvenance("empty.yaml", []byte("  \n\n "), hostLabels(), hostAnnotations(), 2, false)
	if err != nil {
		t.Fatalf("SpliceArtifactServiceProvenance: %v", err)
	}
	if warning != "" {
		t.Errorf("empty content should report nothing, got %q", warning)
	}
	if string(body) != "  \n\n " {
		t.Errorf("empty content should be returned unchanged, got %q", body)
	}
}

// TestSpliceArtifactServiceProvenance_DoesNotWriteThroughTheHostSets pins that
// the fold allocates: the label and annotation maps belong to the caller's
// projection entry, and a placer mutating them would leak one entry's stamp into
// the next entry of the same node.
func TestSpliceArtifactServiceProvenance_DoesNotWriteThroughTheHostSets(t *testing.T) {
	t.Parallel()
	labels, annotations := hostLabels(), hostAnnotations()
	if _, _, err := SpliceArtifactServiceProvenance("db-sidecar.yaml", []byte("services:\n  db-metrics:\n    image: a:1\n"), labels, annotations, 2, false); err != nil {
		t.Fatalf("SpliceArtifactServiceProvenance: %v", err)
	}
	assertKeySet(t, "caller's label set after the splice", labels, hostLabels())
	assertKeySet(t, "caller's annotation set after the splice", annotations, hostAnnotations())
}
