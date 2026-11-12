// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compiler_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// assertSyncWaveIsAString reads every document of a rendered artifact and asserts
// that each sync-wave annotation it carries is the string want — the YAML string
// `"<want>"`, as the chart and synthesized output write it, never a bare number
// YAML reads as an integer. It fails when the file carries no sync-wave annotation
// at all, so a render that stopped stamping the wave cannot pass.
func assertSyncWaveIsAString(t *testing.T, path string, body []byte, want string) {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(body))
	stamped := 0
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("%s does not parse: %v\n%s", path, err, body)
		}
		ann := mappingChild(mappingChild(documentRoot(&doc), "metadata"), "annotations")
		for _, key := range []string{"aggrega.ai/sync-wave", "argocd.argoproj.io/sync-wave"} {
			v := mappingChild(ann, key)
			if v == nil {
				t.Errorf("%s: a document carries no %s\n%s", path, key, body)
				continue
			}
			stamped++
			if v.Kind != yaml.ScalarNode || v.ShortTag() != "!!str" || v.Value != want {
				t.Errorf("%s: %s is %s %q, want the string %q\n%s", path, key, v.ShortTag(), v.Value, want, body)
			}
		}
	}
	if stamped == 0 {
		t.Fatalf("%s carries no sync-wave annotation\n%s", path, body)
	}
	if line := `argocd.argoproj.io/sync-wave: "` + want + `"`; !strings.Contains(string(body), line) {
		t.Errorf("%s does not carry %q\n%s", path, line, body)
	}
}

// documentRoot returns a decoded document's root node.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return doc
}

// mappingChild returns the value written for key in mapping, or nil.
func mappingChild(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// TestArtifactSyncWave_HelmArtifactDocumentCarriesTheWaveAsAString builds a
// Release whose boundary declares `syncWave: 2` over a helm Recipe with an inline
// `artifacts:` document, through the real pipeline and the embedded helm guest.
// The artifact document carries both sync-wave annotations as the string "2".
func TestArtifactSyncWave_HelmArtifactDocumentCarriesTheWaveAsAString(t *testing.T) {
	t.Parallel()
	res := runFeatureMatrixVariant(t, featureMatrixVariant{
		name:        "helm-sourced-with-artifacts",
		releaseFile: "helm-sourced-with-artifacts.yaml",
	})
	body, path := findFile(res.Files, "servicemonitor.yaml")
	if body == "" {
		t.Fatalf("no rendered servicemonitor.yaml; files: %v", sortedKeys(res.Files))
	}
	assertSyncWaveIsAString(t, path, []byte(body), "2")
}

// TestArtifactSyncWave_HelmChartImportedArtifactCarriesTheWaveAsAString builds the
// helm-chart fixture with `syncWave: 3` added to one boundary, through the real
// pipeline and the embedded helm-chart guest. The `$import:`-loaded artifact that
// boundary's component places in its sub-chart's templates/ carries both sync-wave
// annotations as the string "3".
func TestArtifactSyncWave_HelmChartImportedArtifactCarriesTheWaveAsAString(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	copyDirTree(t, "testdata/fullstack-app", root)
	src, err := os.ReadFile(filepath.Join(root, "releases", "helm-chart-with-artifacts.yaml"))
	if err != nil {
		t.Fatalf("read the fixture Release: %v", err)
	}
	const anchor = "      directory: ./dist/backend\n"
	if n := strings.Count(string(src), anchor); n != 1 {
		t.Fatalf("the fixture Release carries %q %d time(s), want 1", anchor, n)
	}
	waved := strings.Replace(string(src), anchor, anchor+"      syncWave: 3\n", 1)
	releasePath := filepath.Join(root, "releases", "helm-chart-with-artifacts-wave.yaml")
	if err := os.WriteFile(releasePath, []byte(waved), 0o644); err != nil {
		t.Fatalf("write the Release: %v", err)
	}
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath: releasePath,
		CatalogPaths: []string{
			filepath.Join(root, "catalog", "base"),
			filepath.Join(root, "catalog", "recipes", "helm-chart-with-artifacts"),
		},
	})
	body, path := findFile(result.Artifacts, "dist/backend/", "/templates/", "policy.yaml")
	if body == "" {
		t.Fatalf("no imported artifact under dist/backend/…/templates/; files: %v", sortedKeys(result.Artifacts))
	}
	assertSyncWaveIsAString(t, path, []byte(body), "3")
}
