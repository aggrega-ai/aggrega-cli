// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compiler_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// helmTargetFactsCatalog is the catalog both builds below read: one component type
// rendered by the helm provider as a chart for web and as a synthesized Workload
// otherwise, and a Feature bound either to a FeatureRecipe that declares it reads its
// targets' render facts or to one that writes the same template without declaring it.
const helmTargetFactsCatalog = "testdata/helm-target-render-facts/catalog"

// TestHelmTargetRenderFacts_AFeatureRecipeReadsWhatItsTargetsEmitted builds the
// fixture through the real pipeline and the embedded helm provider and reads what a
// FeatureRecipe template was handed on each connection its host makes, at
// .context.uses.<alias>.render_facts.
//
// The host, client, is wired to web — a chart — and to worker — a synthesized
// Workload. Both ids sort after client's, so both render after client in the build:
// in client's own turn neither has rendered at all, and the facts are there only
// because the FeatureRecipe declares the read, which renders its attachment once
// every component has. The chart's lines are read exactly: its Deployment's pod
// template labels, its controller's selector and its container's ports, as the
// chart's own templates write them. The synthesized Workload's are read by what the
// build itself stamps, which is all of it a reader can predict.
func TestHelmTargetRenderFacts_AFeatureRecipeReadsWhatItsTargetsEmitted(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/helm-target-render-facts/release.yaml",
		CatalogPaths: []string{helmTargetFactsCatalog},
	})

	var read []string
	for _, d := range emittedDocuments(t, result.Artifacts) {
		body, printed := d.Data["read"]
		if d.Kind != "ConfigMap" || !printed {
			continue
		}
		if read != nil {
			t.Fatalf("two attachments printed a read; the fixture attaches one")
		}
		read = strings.Split(strings.TrimRight(body, "\n"), "\n")
	}
	if len(read) == 0 {
		t.Fatal("no attachment printed a read, so nothing below reads what it was handed")
	}
	t.Logf("the Feature on client read:\n  %s", strings.Join(read, "\n  "))
	if read[0] != "host client" {
		t.Fatalf("the read opens with %q, want it to name its host, client", read[0])
	}

	var web, worker []string
	for _, line := range read[1:] {
		switch {
		case strings.HasPrefix(line, "target web "):
			if worker != nil {
				t.Errorf("a line for web follows the lines for worker; a range meets the connections by alias: %q", line)
			}
			web = append(web, line)
		case strings.HasPrefix(line, "target worker "):
			worker = append(worker, line)
		default:
			t.Errorf("a line names no target the host is wired to: %q", line)
		}
	}

	wantWeb := []string{
		"target web pod_template_labels Deployment/web-server app=web-server tier=front",
		"target web controller_selectors Deployment/web-server app=web-server",
		"target web container_ports Deployment/web-server server http:8080 metrics:9090",
	}
	if !reflect.DeepEqual(web, wantWeb) {
		t.Errorf("the Feature read the chart target web as\n  %s\nwant\n  %s",
			strings.Join(web, "\n  "), strings.Join(wantWeb, "\n  "))
	}

	for _, want := range []struct{ prefix, contains string }{
		{"target worker pod_template_labels Deployment/worker", " aggrega.ai/component-id=worker"},
		{"target worker controller_selectors Deployment/worker", ""},
		{"target worker container_ports Deployment/worker main", ":8080"},
	} {
		found := false
		for _, line := range worker {
			if strings.HasPrefix(line, want.prefix) && strings.Contains(line, want.contains) {
				found = true
			}
		}
		if !found {
			t.Errorf("no line for the synthesized target worker begins %q and carries %q; read\n  %s",
				want.prefix, want.contains, strings.Join(worker, "\n  "))
		}
	}
}

// TestHelmTargetRenderFacts_ReadingThemUndeclaredHalts builds the same fixture with
// its attachment bound to a FeatureRecipe that writes the same template without
// declaring that it reads its targets' render facts.
//
// The build halts before anything renders, with the undeclared condition and with
// no other condition over a connection's facts, and the finding names the reference
// and the field it was written in. The build above, which differs from this one by
// the declaration alone, is the control: it renders.
func TestHelmTargetRenderFacts_ReadingThemUndeclaredHalts(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/helm-target-render-facts/release-undeclared.yaml",
		CatalogPaths: []string{helmTargetFactsCatalog},
	})
	if err == nil {
		t.Fatal("the build rendered a template reading its targets' render facts without the declaration")
	}
	findings := pipelinetest.Findings(err, renderer.ErrTargetRenderFactsNotDeclared())
	if len(findings) != 1 {
		t.Fatalf("the build carries %d finding(s) of the undeclared condition, want 1: %v", len(findings), err)
	}
	for _, other := range []error{
		renderer.ErrPipelineStepReadsTargetRenderFacts(),
		renderer.ErrCallerRenderFactsNotDeclared(),
		renderer.ErrPipelineStepReadsCallerRenderFacts(),
	} {
		if got := pipelinetest.Findings(err, other); len(got) != 0 {
			t.Errorf("the build also carries %v: %v", other, err)
		}
	}
	message := findings[0].Error()
	for _, want := range []string{".render_facts", "artifacts[facts.yaml]", "readsTargetRenderFacts"} {
		if !strings.Contains(message, want) {
			t.Errorf("the finding does not name %q: %s", want, message)
		}
	}
	t.Logf("REFUSED -> %s", message)
}
