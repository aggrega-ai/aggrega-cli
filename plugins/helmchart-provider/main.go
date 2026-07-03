// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command helmchart-provider is the helm-chart Provider Wasm guest. It implements
// the sourced-render path of the helm-chart engine behind the provider render
// ABI's render_node export, and the umbrella-assembly path behind its
// assemble_boundary export, reusing the same filesystem-free render and assemble
// cores the in-tree helm-chart engine runs so the two produce byte-identical
// output for identical input.
//
// The helm-chart provider is sourced-only: it copies a user's local Helm chart
// verbatim into a sub-chart file set (umbrella + sub-chart composition). It
// synthesizes nothing from a Workload — workload synthesis is the helm provider's
// concern — so the guest handles a source projection alone and rejects a payload
// that carries none.
//
// The render_node guest decodes a RenderNodeInput, reconstructs the sub-chart's
// file set in memory from the payload's SourceProjection (the host read the
// chart's files at compile time and packed them into the payload — the guest
// touches no filesystem), and returns each file's bytes verbatim as a
// RenderedFile. The assemble_boundary guest decodes an AssembleBoundaryInput,
// reconstructs each sub-chart's pure-data view from the payload (identity,
// per-instance values, and the pristine bytes of the vendored chart's Chart.yaml
// the host read at compile time), and assembles the umbrella Chart.yaml +
// values.yaml through the same core, signalling that per-node files nest under a
// per-node sub-directory. The host writes all returned bytes verbatim and runs no
// serializer, so the on-disk output matches the in-tree engine's bytes.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o helmchart-provider.wasm ./plugins/helmchart-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	pdk "github.com/extism/go-pdk"
)

// pluginDescriptor is the static describe() self-report: a v1alpha1 provider
// plugin declaring the render_node and assemble_boundary exports and
// compatibility with the helm-chart provider engine. The host reads this once at
// load and validates it before invoking either export. helm-chart copies chart
// files verbatim and fetches nothing, so no host functions are declared.
const pluginDescriptor = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["helm-chart"],"exports":[{"name":"render_node","input_pattern":"b","stability":"experimental","description":"copies a sourced Helm chart into a sub-chart file set"},{"name":"assemble_boundary","input_pattern":"b","stability":"experimental","description":"assembles the per-node sub-charts at one dispatch group into an umbrella Chart.yaml + values.yaml"}],"metadata":{"author":"aggrega","license":"Apache-2.0"}}`

//go:wasmexport describe
func describe() int32 {
	pdk.Output([]byte(pluginDescriptor))
	return 0
}

//go:wasmexport render_node
func renderNode() int32 {
	var in renderer.RenderNodeInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	// helm-chart is sourced-only: it copies a local chart verbatim and
	// synthesizes nothing. A payload with no source projection is an orchestrator
	// dispatch error rather than authoring drift, so it halts loudly.
	if in.SourceProjection == nil {
		pdk.SetError(errors.New("helmchart-provider: render_node input carries no source projection"))
		return 1
	}

	rendered := helmchart.RenderSourcedChartCore(in.SourceProjection)

	out := renderer.RenderNodeOutput{Files: make([]renderer.RenderedFile, 0, len(rendered))}
	for _, path := range sortedKeys(rendered) {
		out.Files = append(out.Files, renderer.RenderedFile{Path: path, Content: rendered[path]})
	}

	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

//go:wasmexport assemble_boundary
func assembleBoundary() int32 {
	var in renderer.AssembleBoundaryInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	out, err := assembleBoundaryFromInput(in)
	if err != nil {
		pdk.SetError(errors.New(errorChainMessage(err)))
		return 1
	}

	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// assembleBoundaryFromInput reconstructs the umbrella assemble core's inputs from
// the wire AssembleBoundaryInput and runs the same filesystem-free assembly core
// the in-tree helm-chart engine runs, returning the umbrella Chart.yaml +
// values.yaml as RenderedFiles. Each sub-chart carries its instance identity, its
// per-instance values (flattened from the wire's ordered map), and, for a
// sub-chart vendored from a chart source, the pristine bytes of that chart's
// Chart.yaml the host read at compile time; the core draws the dependency's name
// and version from those bytes. The boundary path is the dispatch path the core
// names the chart from when no chart-name is supplied; the render context carries
// the indentation width. helm-chart nests each contributor's per-node files under
// a per-node sub-directory, so NestPerNode is true.
func assembleBoundaryFromInput(in renderer.AssembleBoundaryInput) (renderer.AssembleBoundaryOutput, error) {
	subs := make([]helmchart.CoreSubChart, 0, len(in.SubCharts))
	for i := range in.SubCharts {
		sc := in.SubCharts[i]
		subs = append(subs, helmchart.CoreSubChart{
			InstanceID: sc.InstanceID,
			Version:    sc.Version,
			Values:     orderedMapToPlain(&sc.Values),
			ChartYAML:  sc.ChartYaml,
		})
	}

	files, err := helmchart.AssembleUmbrellaCore(in.BoundaryPath, in.AggregateName, subs, in.ChartName, in.Context.YAMLIndent)
	if err != nil {
		return renderer.AssembleBoundaryOutput{}, err
	}

	out := renderer.AssembleBoundaryOutput{Files: make([]renderer.RenderedFile, 0, len(files))}
	for _, path := range sortedKeys(files) {
		out.Files = append(out.Files, renderer.RenderedFile{Path: path, Content: files[path]})
	}
	out.NestPerNode = true
	return out, nil
}

// errorChainMessage serializes err's full wrap chain into one message. The
// host-side rich error formatter is not available across the plugin ABI, and
// some wrapper types (the compiler's categorised errors) keep their cause out of
// Error(), surfacing it only through Unwrap; appending each unwrapped cause not
// already present keeps the root diagnostic — an empty or multi-document
// Chart.yaml, an empty name or version — in the message the host receives.
func errorChainMessage(err error) string {
	msg := err.Error()
	for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
		if s := e.Error(); !strings.Contains(msg, s) {
			msg += ": " + s
		}
	}
	return msg
}

// orderedMapToPlain flattens a sub-chart's ordered values into the plain
// map[string]any the umbrella assemble core consumes. Nested ordered maps decode
// into *OrderedMap[any]; they are flattened recursively so the core sees plain Go
// maps rather than the wire container. The core scopes the values under the
// sub-chart's alias by key, so the author order the wire preserves is not
// load-bearing here.
func orderedMapToPlain(m *types.OrderedMap[any]) map[string]any {
	out := make(map[string]any, m.Len())
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = plainValue(v)
	}
	return out
}

// plainValue recursively converts a decoded wire value into plain Go containers:
// nested ordered maps become map[string]any and slices are walked element-wise.
// A json.Number is renormalized to the Go numeric type a YAML scalar parse would
// yield; other scalars pass through unchanged.
func plainValue(v any) any {
	switch t := v.(type) {
	case *types.OrderedMap[any]:
		return orderedMapToPlain(t)
	case types.OrderedMap[any]:
		return orderedMapToPlain(&t)
	case []any:
		for i := range t {
			t[i] = plainValue(t[i])
		}
		return t
	case json.Number:
		// The wire codec decodes JSON numerics to json.Number (high-precision
		// integer preservation via UseNumber). go-yaml serializes a json.Number —
		// a string-kind type — as a quoted scalar, which would diverge from the
		// in-tree engine, whose values carry the Go numeric types a YAML scalar
		// parse yields. Renormalize to those same types so the assembled
		// values.yaml is byte-identical: an integral value to int64, a fractional
		// value to float64, and anything neither leaves the number untouched.
		if i, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(string(t), 64); err == nil {
			return f
		}
		return t
	default:
		return v
	}
}

// sortedKeys returns the map's keys in lexical order so file emission is
// deterministic regardless of Go map iteration order.
func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// insertion sort keeps the guest free of the sort package's
	// reflection surface; the file set per node is small.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func main() {}
