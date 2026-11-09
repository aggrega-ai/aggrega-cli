// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// unloadableChartTemplate is a Helm chart template as a FeatureRecipe's chart
// ships it: Go template source declaring a Kubernetes apiVersion, which does not
// parse as YAML until it is rendered.
const unloadableChartTemplate = `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ .Values.targetName }}-ingress
  {{- with .Values.namespace }}
  namespace: {{ . }}
  {{- end }}
spec:
  rules:
    - host: {{ .Values.host }}
`

// TestUnloadableDocumentAnswersEachShape holds the one answer to whether a file
// a whole-catalog check read is an Aggrega document it cannot load, one row per
// shape: the envelope is read from the text, so a document that does not parse
// is still recognised, and every file that declares no Aggrega apiVersion at
// column 0 is passed over however it parses.
func TestUnloadableDocumentAnswersEachShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		data       string
		readErr    error
		unloadable bool
		reason     string
	}{
		{
			name:       "the envelope with a YAML syntax error",
			data:       "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: core/broken\nspec:\n  schema: values: [\n",
			unloadable: true,
			reason:     "failed to unmarshal YAML:",
		},
		{
			name:       "the envelope quoted, with a YAML syntax error",
			data:       "apiVersion: \"aggrega.ai/v1beta1\"\nkind: Component\nspec: : [\n",
			unloadable: true,
			reason:     "failed to unmarshal YAML:",
		},
		{
			name:       "the envelope single-quoted, with a YAML syntax error",
			data:       "apiVersion: 'aggrega.ai/v1beta1'\nkind: Component\nspec: : [\n",
			unloadable: true,
			reason:     "failed to unmarshal YAML:",
		},
		{
			name:       "the envelope with a header that does not decode",
			data:       "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  - name: core/listed\nspec: {}\n",
			unloadable: true,
			reason:     "failed to decode the envelope: line 4 (metadata): a list is written where a map is expected",
		},
		{
			name:       "a file that could not be read",
			readErr:    fs.ErrPermission,
			unloadable: true,
			reason:     "failed to read file:",
		},
		{
			name: "a well-formed Aggrega document",
			data: "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\nspec: {}\n",
		},
		{
			name: "a Helm chart template",
			data: unloadableChartTemplate,
		},
		{
			name: "a parsable YAML file that is not an Aggrega document",
			data: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  - not-a-mapping\n",
		},
		{
			name: "an envelope line indented inside a block scalar",
			data: "description: |\n  apiVersion: aggrega.ai/v1beta1\n  kind: Component\nvalues: : [\n",
		},
		{
			name: "an empty file",
			data: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const path = "catalog/component/probe.yaml"
			var got error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("UnloadableDocument panicked: %v", r)
					}
				}()
				got = UnloadableDocument(path, []byte(tc.data), tc.readErr, nil)
			}()
			if !tc.unloadable {
				if got != nil {
					t.Fatalf("a file the check passes over was answered as an unloadable Aggrega document: %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("an Aggrega document the check cannot load was passed over")
			}
			if !errors.Is(got, resolver.ErrYAMLLoad()) {
				t.Errorf("the answer does not carry the document-load condition: %v", got)
			}
			var ce *cerrors.CompilerError
			if !errors.As(got, &ce) || ce.Source().File != path {
				t.Errorf("the answer does not name the file %s: %v", path, got)
			}
			if !strings.Contains(got.Error(), tc.reason) {
				t.Errorf("the answer does not say %q: %v", tc.reason, got)
			}
			for _, decoderText := range []string{"struct {", "cannot unmarshal", "!!seq"} {
				if strings.Contains(got.Error(), decoderText) {
					t.Errorf("the answer carries the decoder's own text %q, which names nothing an author wrote: %v", decoderText, got)
				}
			}
		})
	}
}

// TestUnloadableDocumentDecodesIntoTheCallersHeader holds the decode to the
// header the caller reads: a document whose identity decodes but whose spec is
// the wrong shape for a header that reads spec is refused for that header, and
// passed for the envelope's identity alone.
func TestUnloadableDocumentDecodesIntoTheCallersHeader(t *testing.T) {
	t.Parallel()
	data := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: helm/broken\nspec:\n  capability:\n    - a-list\n")
	var header struct {
		Spec struct {
			Capability string `yaml:"capability"`
		} `yaml:"spec"`
	}
	if err := UnloadableDocument("recipe.yaml", data, nil, &header); !errors.Is(err, resolver.ErrYAMLLoad()) {
		t.Errorf("a header reading spec.capability did not refuse a sequence there: %v", err)
	}
	if err := UnloadableDocument("recipe.yaml", data, nil, nil); err != nil {
		t.Errorf("the envelope's identity decodes and the document was refused: %v", err)
	}
}

// TestUnloadableDocumentsHaltNamesEveryFileInPathOrder holds the halt to every
// finding it is given, ordered by path whatever order the walk met them in, and
// to nothing at all for an empty walk.
func TestUnloadableDocumentsHaltNamesEveryFileInPathOrder(t *testing.T) {
	t.Parallel()
	if err := UnloadableDocumentsHalt(nil); err != nil {
		t.Fatalf("a walk finding nothing halted: %v", err)
	}
	bad := []byte("apiVersion: aggrega.ai/v1beta1\nspec: : [\n")
	found := []error{
		UnloadableDocument("catalog/z.yaml", bad, nil, nil),
		UnloadableDocument("catalog/a.yaml", bad, nil, nil),
	}
	halt := UnloadableDocumentsHalt(found)
	if halt == nil {
		t.Fatal("two unloadable documents raised no halt")
	}
	if !errors.Is(halt, resolver.ErrYAMLLoad()) {
		t.Errorf("the halt does not carry the document-load condition: %v", halt)
	}
	chain := cerrors.CauseChain(halt)
	a, z := strings.Index(chain, "catalog/a.yaml"), strings.Index(chain, "catalog/z.yaml")
	if a < 0 || z < 0 || a > z {
		t.Errorf("the halt does not name both files in path order:\n%s", chain)
	}
	if !strings.Contains(halt.Error(), "2 documents under the catalog directories") {
		t.Errorf("the halt does not count the documents: %v", halt)
	}
}
