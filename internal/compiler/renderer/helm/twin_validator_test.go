// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// decodeValues decodes a JSON object into the plain values map the render
// core consumes, with numerics as json.Number — the value shape the renderer
// hands the engine. A fresh map per call keeps render invocations
// independent.
func decodeValues(t *testing.T, j string) map[string]any {
	t.Helper()
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(j))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode values %s: %v", j, err)
	}
	return out
}

// loadSchemaChart loads a fresh copy of the schema-bearing fixture chart.
func loadSchemaChart(t *testing.T) *chart.Chart {
	t.Helper()
	ch, err := loader.Load("testdata/schema-chart")
	if err != nil {
		t.Fatalf("load schema-chart: %v", err)
	}
	return ch
}

// chartFromFiles builds an in-memory chart the same way the provider guest
// does — through loader.LoadFiles — so validator behaviour over in-memory
// charts is exercised without touching a filesystem chart root.
func chartFromFiles(t *testing.T, files map[string]string) *chart.Chart {
	t.Helper()
	bufs := make([]*loader.BufferedFile, 0, len(files))
	for name, content := range files {
		bufs = append(bufs, &loader.BufferedFile{Name: name, Data: []byte(content)})
	}
	ch, err := loader.LoadFiles(bufs)
	if err != nil {
		t.Fatalf("load chart from files: %v", err)
	}
	return ch
}

// forbidFetch fails the test if the hermetic validator reaches for a remote
// schema document: a chart without remote references must validate without
// fetching anything.
func forbidFetch(t *testing.T) helm.SchemaFetchFunc {
	return func(url string) ([]byte, error) {
		t.Fatalf("unexpected remote schema fetch for %q", url)
		return nil, nil
	}
}

// errorChainText flattens an error's wrap chain into one string for content
// assertions: the compiler's categorised errors keep their cause out of
// Error(), surfacing it through Unwrap for the CLI's error formatter, so a
// content assertion must walk the chain the way that formatter does.
func errorChainText(err error) string {
	msg := err.Error()
	for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
		if s := e.Error(); !strings.Contains(msg, s) {
			msg += ": " + s
		}
	}
	return msg
}

// assertSameFiles asserts two rendered file sets are byte-identical.
func assertSameFiles(t *testing.T, want, got map[string][]byte) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("file sets differ: %d vs %d files", len(want), len(got))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("missing file %q", k)
			continue
		}
		if string(w) != string(g) {
			t.Errorf("bytes differ for %q:\n--- SDK path ---\n%s\n--- hermetic path ---\n%s", k, w, g)
		}
	}
}

// TestValuesSchemaValidationParity proves the hermetic validator and the Helm
// SDK's own validator agree on a schema-bearing chart: valid values are
// accepted by both with byte-identical rendered output, and violating values
// are rejected by both with errors naming the violated constraint.
func TestValuesSchemaValidationParity(t *testing.T) {
	ctx := context.Background()
	nodeCtx := renderer.NodeRenderContext{NodeID: "api"}

	t.Run("valid values accepted by both with byte-identical output", func(t *testing.T) {
		sdkOut, err := helm.RenderLoadedChart(ctx, loadSchemaChart(t),
			decodeValues(t, `{"replicas":3,"message":"hello"}`), nodeCtx, nil)
		if err != nil {
			t.Fatalf("SDK validation path: %v", err)
		}
		twinOut, err := helm.RenderLoadedChart(ctx, loadSchemaChart(t),
			decodeValues(t, `{"replicas":3,"message":"hello"}`), nodeCtx,
			helm.NewHermeticValuesValidator(forbidFetch(t)))
		if err != nil {
			t.Fatalf("hermetic validation path: %v", err)
		}
		if len(sdkOut) == 0 {
			t.Fatal("SDK path rendered no files")
		}
		assertSameFiles(t, sdkOut, twinOut)
	})

	t.Run("violating values rejected by both naming the constraint", func(t *testing.T) {
		_, sdkErr := helm.RenderLoadedChart(ctx, loadSchemaChart(t),
			decodeValues(t, `{"replicas":0}`), nodeCtx, nil)
		_, twinErr := helm.RenderLoadedChart(ctx, loadSchemaChart(t),
			decodeValues(t, `{"replicas":0}`), nodeCtx,
			helm.NewHermeticValuesValidator(forbidFetch(t)))
		for name, err := range map[string]error{"SDK": sdkErr, "hermetic": twinErr} {
			if err == nil {
				t.Fatalf("%s validation accepted values violating the schema", name)
			}
			for _, want := range []string{"replicas", "minimum"} {
				if !strings.Contains(errorChainText(err), want) {
					t.Errorf("%s validation error does not name %q: %v", name, want, errorChainText(err))
				}
			}
		}
	})
}

// TestValuesSchemaValidationParityDependencyChart proves the hermetic
// validator walks dependency charts' schemas the way the SDK does: the
// dependency's schema validates the value subtree keyed by the dependency's
// name, and both strategies agree on acceptance and rejection.
func TestValuesSchemaValidationParityDependencyChart(t *testing.T) {
	mkChart := func(t *testing.T) *chart.Chart {
		return chartFromFiles(t, map[string]string{
			"Chart.yaml":  "apiVersion: v2\nname: parent\nversion: 0.1.0\n",
			"values.yaml": "sub:\n  replicas: 1\n",
			"templates/configmap.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n" +
				"  name: {{ .Release.Name }}-parent\ndata:\n  subReplicas: {{ .Values.sub.replicas | quote }}\n",
			"charts/sub/Chart.yaml":         "apiVersion: v2\nname: sub\nversion: 0.1.0\n",
			"charts/sub/values.yaml":        "replicas: 1\n",
			"charts/sub/values.schema.json": `{"type":"object","properties":{"replicas":{"type":"integer","minimum":1}}}`,
		})
	}
	ctx := context.Background()
	nodeCtx := renderer.NodeRenderContext{NodeID: "api"}

	t.Run("valid dependency values accepted by both", func(t *testing.T) {
		sdkOut, err := helm.RenderLoadedChart(ctx, mkChart(t),
			decodeValues(t, `{"sub":{"replicas":2}}`), nodeCtx, nil)
		if err != nil {
			t.Fatalf("SDK validation path: %v", err)
		}
		twinOut, err := helm.RenderLoadedChart(ctx, mkChart(t),
			decodeValues(t, `{"sub":{"replicas":2}}`), nodeCtx,
			helm.NewHermeticValuesValidator(forbidFetch(t)))
		if err != nil {
			t.Fatalf("hermetic validation path: %v", err)
		}
		assertSameFiles(t, sdkOut, twinOut)
	})

	t.Run("violating dependency values rejected by both", func(t *testing.T) {
		_, sdkErr := helm.RenderLoadedChart(ctx, mkChart(t),
			decodeValues(t, `{"sub":{"replicas":0}}`), nodeCtx, nil)
		_, twinErr := helm.RenderLoadedChart(ctx, mkChart(t),
			decodeValues(t, `{"sub":{"replicas":0}}`), nodeCtx,
			helm.NewHermeticValuesValidator(forbidFetch(t)))
		for name, err := range map[string]error{"SDK": sdkErr, "hermetic": twinErr} {
			if err == nil {
				t.Fatalf("%s validation accepted dependency values violating the schema", name)
			}
			for _, want := range []string{"sub", "minimum"} {
				if !strings.Contains(errorChainText(err), want) {
					t.Errorf("%s validation error does not name %q: %v", name, want, errorChainText(err))
				}
			}
		}
	})
}

// TestHermeticValidatorRemoteRef exercises the hermetic validator's remote
// reference resolution through an injected fetch function: the contract
// document arrives through fetch, violations against it reject the render,
// and a fetch failure halts the render naming the URL.
func TestHermeticValidatorRemoteRef(t *testing.T) {
	const refURL = "https://schemas.example.invalid/values-contract.json"
	const remoteContract = `{"type":"object","properties":{"replicas":{"type":"integer","minimum":1}},"required":["replicas"]}`

	mkChart := func(t *testing.T) *chart.Chart {
		return chartFromFiles(t, map[string]string{
			"Chart.yaml":         "apiVersion: v2\nname: remoteref\nversion: 0.1.0\n",
			"values.yaml":        "replicas: 1\n",
			"values.schema.json": fmt.Sprintf(`{"$ref": %q}`, refURL),
			"templates/configmap.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n" +
				"  name: {{ .Release.Name }}-remote\ndata:\n  replicas: {{ .Values.replicas | quote }}\n",
		})
	}
	ctx := context.Background()
	nodeCtx := renderer.NodeRenderContext{NodeID: "api"}

	t.Run("valid values pass with the contract served through fetch", func(t *testing.T) {
		var fetched []string
		fetch := func(url string) ([]byte, error) {
			fetched = append(fetched, url)
			return []byte(remoteContract), nil
		}
		out, err := helm.RenderLoadedChart(ctx, mkChart(t),
			decodeValues(t, `{"replicas":2}`), nodeCtx, helm.NewHermeticValuesValidator(fetch))
		if err != nil {
			t.Fatalf("hermetic validation with fetched contract: %v", err)
		}
		if len(out) == 0 {
			t.Fatal("rendered no files")
		}
		if len(fetched) == 0 || fetched[0] != refURL {
			t.Fatalf("fetch calls = %v, want first call for %q", fetched, refURL)
		}
	})

	t.Run("violating values rejected via the fetched contract", func(t *testing.T) {
		fetch := func(string) ([]byte, error) { return []byte(remoteContract), nil }
		_, err := helm.RenderLoadedChart(ctx, mkChart(t),
			decodeValues(t, `{"replicas":0}`), nodeCtx, helm.NewHermeticValuesValidator(fetch))
		if err == nil {
			t.Fatal("hermetic validation accepted values violating the fetched contract")
		}
		for _, want := range []string{"replicas", "minimum"} {
			if !strings.Contains(errorChainText(err), want) {
				t.Errorf("validation error does not name %q: %v", want, errorChainText(err))
			}
		}
	})

	t.Run("fetch failure halts the render naming the URL", func(t *testing.T) {
		fetch := func(url string) ([]byte, error) {
			return nil, fmt.Errorf("%q is not pinned", url)
		}
		_, err := helm.RenderLoadedChart(ctx, mkChart(t),
			decodeValues(t, `{"replicas":2}`), nodeCtx, helm.NewHermeticValuesValidator(fetch))
		if err == nil {
			t.Fatal("render succeeded although the schema reference could not be resolved")
		}
		if !strings.Contains(errorChainText(err), refURL) {
			t.Errorf("fetch-failure error does not name the URL %q: %v", refURL, errorChainText(err))
		}
	})
}

// TestHermeticValidatorChartLocalRef proves chart-local relative references
// resolve from the chart's in-memory files: no fetch happens, and the
// referenced contract still gates the values.
func TestHermeticValidatorChartLocalRef(t *testing.T) {
	mkChart := func(t *testing.T) *chart.Chart {
		return chartFromFiles(t, map[string]string{
			"Chart.yaml":                   "apiVersion: v2\nname: localref\nversion: 0.1.0\n",
			"values.yaml":                  "replicas: 1\n",
			"values.schema.json":           `{"$ref": "schemas/values-contract.json"}`,
			"schemas/values-contract.json": `{"type":"object","properties":{"replicas":{"type":"integer","minimum":1}}}`,
			"templates/configmap.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n" +
				"  name: {{ .Release.Name }}-local\ndata:\n  replicas: {{ .Values.replicas | quote }}\n",
		})
	}
	ctx := context.Background()
	nodeCtx := renderer.NodeRenderContext{NodeID: "api"}

	t.Run("valid values accepted from the in-memory contract", func(t *testing.T) {
		out, err := helm.RenderLoadedChart(ctx, mkChart(t),
			decodeValues(t, `{"replicas":2}`), nodeCtx,
			helm.NewHermeticValuesValidator(forbidFetch(t)))
		if err != nil {
			t.Fatalf("hermetic validation with chart-local contract: %v", err)
		}
		if len(out) == 0 {
			t.Fatal("rendered no files")
		}
	})

	t.Run("violating values rejected from the in-memory contract", func(t *testing.T) {
		_, err := helm.RenderLoadedChart(ctx, mkChart(t),
			decodeValues(t, `{"replicas":0}`), nodeCtx,
			helm.NewHermeticValuesValidator(forbidFetch(t)))
		if err == nil {
			t.Fatal("hermetic validation accepted values violating the chart-local contract")
		}
		for _, want := range []string{"replicas", "minimum"} {
			if !strings.Contains(errorChainText(err), want) {
				t.Errorf("validation error does not name %q: %v", want, errorChainText(err))
			}
		}
	})
}
