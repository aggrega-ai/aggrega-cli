// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// syncWaveAnnotations is the annotation set a boundary declaring the given wave
// stamps on an artifact, built by the same builder every placer reads.
func syncWaveAnnotations(t *testing.T, wave int) map[string]string {
	t.Helper()
	ann := BuildAggregaProvenanceAnnotations(NodeRenderContext{BoundaryPath: "first", SyncWave: wave}, "svc:1.0.0")
	if len(ann) != 4 {
		t.Fatalf("annotation set = %v, want boundary-path, recipe-uri and the two sync-wave keys", ann)
	}
	return ann
}

// annotationNodes returns, per document of body, the value node written for key
// under metadata.annotations, or nil where the document carries none.
func annotationNodes(t *testing.T, body []byte, key string) []*yaml.Node {
	t.Helper()
	docs, err := decodeAllDocs(string(body))
	if err != nil {
		t.Fatalf("placed artifact does not parse: %v\n%s", err, body)
	}
	var out []*yaml.Node
	for _, d := range docs {
		mapping := DocumentMapping(d)
		meta := UnwrapAlias(MappingValueByKey(mapping, "metadata"))
		ann := UnwrapAlias(MappingValueByKey(meta, "annotations"))
		out = append(out, UnwrapAlias(MappingValueByKey(ann, key)))
	}
	return out
}

// TestSpliceArtifactProvenance_SyncWaveIsWrittenAsAString places an artifact the
// way the helm guest (default delimiters) and the helm-chart guest (alternate
// delimiters) do, through the one placer both call. Each sync-wave annotation is
// written as the string the wave is — `"3"`, the spelling the chart and
// synthesized output use — never as a bare number YAML reads as an integer, in
// every shape the splice handles: an annotations block it creates, one it appends
// to, a negative wave, and every document of a stream.
func TestSpliceArtifactProvenance_SyncWaveIsWrittenAsAString(t *testing.T) {
	t.Parallel()
	const configMap = `apiVersion: v1
kind: ConfigMap
metadata:
  name: art-api
data:
  k: v
`
	const hookJob = `apiVersion: batch/v1
kind: Job
metadata:
  name: arthook-api
  annotations:
    helm.sh/hook: pre-install
spec:
  template:
    spec:
      restartPolicy: Never
`
	for _, tc := range []struct {
		name          string
		body          string
		defaultDelims bool
		wave          int
		wantDocs      int
	}{
		{"annotations block created, default delimiters", configMap, true, 3, 1},
		{"annotations block created, alternate delimiters", configMap, false, 3, 1},
		{"appended to an authored annotations block", hookJob, true, 3, 1},
		{"negative wave", configMap, true, -1, 1},
		{"every document of a stream", configMap + "---\n" + hookJob, false, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ann := syncWaveAnnotations(t, tc.wave)
			labels := map[string]string{"aggrega.ai/component-id": "api"}
			body, warning, err := SpliceArtifactProvenance("art.yaml", []byte(tc.body), labels, ann, tc.defaultDelims, 2, false)
			if err != nil {
				t.Fatalf("SpliceArtifactProvenance: %v", err)
			}
			if warning != "" {
				t.Fatalf("unexpected warning %q", warning)
			}
			want := ann["argocd.argoproj.io/sync-wave"]
			for _, key := range []string{"aggrega.ai/sync-wave", "argocd.argoproj.io/sync-wave"} {
				nodes := annotationNodes(t, body, key)
				if len(nodes) != tc.wantDocs {
					t.Fatalf("%s: %d document(s), want %d\n%s", key, len(nodes), tc.wantDocs, body)
				}
				for i, n := range nodes {
					if n == nil || n.Kind != yaml.ScalarNode || n.ShortTag() != "!!str" || n.Value != want {
						t.Errorf("document %d %s = %+v, want the string %q\n%s", i, key, n, want, body)
					}
				}
				line := key + `: "` + want + `"`
				if got := strings.Count(string(body), line); got != tc.wantDocs {
					t.Errorf("%q written %d time(s), want %d\n%s", line, got, tc.wantDocs, body)
				}
			}
			// The authored lines stay as written: the splice adds lines, it rewrites none.
			for _, authored := range strings.Split(strings.TrimRight(tc.body, "\n"), "\n") {
				if !strings.Contains(string(body), authored+"\n") {
					t.Errorf("authored line %q not carried as written\n%s", authored, body)
				}
			}
		})
	}
}

// TestYAMLScalar_PlainOnlyWhatReadsBackAsTheString pins the one rule the spliced
// provenance lines are written by: a value is written plain only when it reads back
// as the string it is, and quoted otherwise. Each row's output is read back and
// must be the input string; the plain rows are the shapes provenance values take
// and keep their unquoted spelling.
func TestYAMLScalar_PlainOnlyWhatReadsBackAsTheString(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want string
	}{
		{"3", `"3"`},
		{"-1", `"-1"`},
		{"1.0", `"1.0"`},
		{"true", `"true"`},
		{"null", `"null"`},
		{"~", `"~"`},
		{"api", "api"},
		{"probe-env", "probe-env"},
		{"out/first", "out/first"},
		{"0.8.6-105-g20dcf8f2", "0.8.6-105-g20dcf8f2"},
	} {
		got := yamlScalar(tc.in)
		if got != tc.want {
			t.Errorf("yamlScalar(%q) = %s, want %s", tc.in, got, tc.want)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte("k: "+got), &doc); err != nil {
			t.Errorf("yamlScalar(%q) = %s does not parse: %v", tc.in, got, err)
			continue
		}
		v := DocumentMapping(&doc).Content[1]
		if v.ShortTag() != "!!str" || v.Value != tc.in {
			t.Errorf("yamlScalar(%q) = %s reads back as %s %q", tc.in, got, v.ShortTag(), v.Value)
		}
	}
}
