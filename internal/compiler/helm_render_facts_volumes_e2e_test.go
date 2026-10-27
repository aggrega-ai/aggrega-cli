// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// TestHelmRenderFacts_PodTemplateVolumesReachAFeatureRecipe builds the fixture
// through the real pipeline and the embedded helm provider, and reads what a
// FeatureRecipe attached to the host printed from the host's pod_template_volumes
// fact at .context.render_facts.
//
// The host is a chart rendering a StatefulSet whose pod template declares two
// volumes — a ConfigMap's and an emptyDir — and one volume claim template, and
// whose one container mounts all three. The Feature prints one line per volume, per
// claim template and per mount, in the order the fact carries them, which is the
// order the emitted document writes them; the emitted StatefulSet is checked first,
// so the rows are known to read a document that carries what they say.
func TestHelmRenderFacts_PodTemplateVolumesReachAFeatureRecipe(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/helm-render-facts-volumes/release.yaml",
		CatalogPaths: []string{"testdata/helm-render-facts-volumes/catalog"},
	})
	docs := emittedDocuments(t, result.Artifacts)
	if !slices.ContainsFunc(docs, func(d emittedDocument) bool { return d.Kind == "StatefulSet" && d.Metadata.Name == "db" }) {
		t.Fatalf("the build emitted no StatefulSet %q, so the read below is of a render the fixture no longer produces", "db")
	}

	var read []string
	for _, d := range docs {
		if body, printed := d.Data["read"]; d.Kind == "ConfigMap" && printed {
			if read != nil {
				t.Fatal("two attachments printed a read")
			}
			read = strings.Split(strings.TrimRight(body, "\n"), "\n")
		}
	}
	want := []string{
		"host db",
		"volume StatefulSet/db config configMap",
		"volume StatefulSet/db scratch emptyDir",
		"claim StatefulSet/db data",
		"mount StatefulSet/db server data /var/lib/store read_only=<absent>",
		"mount StatefulSet/db server config /etc/store read_only=true",
		"mount StatefulSet/db server scratch /tmp/store read_only=<absent>",
	}
	if !slices.Equal(read, want) {
		t.Errorf("the Feature read\n  %s\nwant\n  %s", strings.Join(read, "\n  "), strings.Join(want, "\n  "))
	}
}
