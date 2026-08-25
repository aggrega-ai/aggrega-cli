// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// umbrellaChartDependency mirrors a Helm umbrella Chart.yaml dependency entry.
type umbrellaChartDependency struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	Alias      string `yaml:"alias,omitempty"`
	Repository string `yaml:"repository"`
}

// umbrellaChart is the minimal umbrella Chart.yaml shape.
type umbrellaChart struct {
	APIVersion   string                    `yaml:"apiVersion"`
	Name         string                    `yaml:"name"`
	Version      string                    `yaml:"version"`
	Description  string                    `yaml:"description,omitempty"`
	Dependencies []umbrellaChartDependency `yaml:"dependencies,omitempty"`
}

// CoreSubChart is the filesystem-free view of one sub-chart the umbrella core
// consumes. It carries the sub-chart's identity and per-instance values plus, for
// a sourced sub-chart, the pristine bytes of the vendored chart's Chart.yaml
// (empty for a generative sub-chart). The host reads those bytes from disk before
// calling the core; a provider Wasm guest supplies them from its payload — so the
// umbrella assembly is identical across both surfaces and touches no filesystem.
type CoreSubChart struct {
	InstanceID string
	Version    string
	Values     map[string]any
	ChartYAML  []byte
}

// ReadChartMetaBytes parses a chart's Chart.yaml bytes and returns its `name` and
// `version` fields. label identifies the sub-chart in error strings; it carries
// no filesystem path so the guardrails read identically on the host and in a
// provider Wasm guest, whose payload has no host paths. It is exported so a
// provider Wasm guest, which lives in a separate package, can drive the same
// decode-and-guardrail logic the in-tree engine runs.
//
// A chart's Chart.yaml is a single YAML document. This decodes the first document,
// then requires the stream to be exhausted: a single yaml.Unmarshal would silently
// keep only the first document of a multi-document file, so a malformed
// multi-document Chart.yaml halts loudly. An empty input (io.EOF on the first
// Decode) leaves the header zero-valued and falls through to the empty-name halt.
// An empty name or version halts loudly so a future drift cannot silently drop the
// dependency's alias binding.
func ReadChartMetaBytes(data []byte, label string) (name, version string, err error) {
	var header struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&header); err != nil && !errors.Is(err, io.EOF) {
		return "", "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: label}, fmt.Sprintf("helmchart sourced: parse Chart.yaml in %q", label))
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", "", cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: label}, fmt.Sprintf("helmchart sourced: Chart.yaml in %q contains multiple YAML documents; a chart's Chart.yaml must be a single document", label), nil, nil)
	}
	if header.Name == "" {
		return "", "", cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: label}, fmt.Sprintf("helmchart sourced: Chart.yaml in %q has empty name", label), nil, nil)
	}
	if header.Version == "" {
		return "", "", cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: label}, fmt.Sprintf("helmchart sourced: Chart.yaml in %q has empty version", label), nil, nil)
	}
	return header.Name, header.Version, nil
}

// AssembleUmbrellaCore produces the umbrella Chart.yaml + values.yaml for a
// dispatch path from the filesystem-free sub-chart views. It is the umbrella
// assembly core — it reads no disk and formats no host path — so both the in-tree
// host engine (through the BuildUmbrella adapter, which reads each sourced
// sub-chart's Chart.yaml) and a provider Wasm guest call this one
// byte-identical-critical function. It is exported so a provider Wasm guest, which
// lives in a separate package, can drive the same core the in-tree engine runs.
//
// chartNameOverride is the cascaded `aggrega.ai/chart-name` value when present;
// when non-empty it is the chart's name verbatim (the merged-path scenario's name
// source). When empty, the name is derived from the dispatch path.
//
// Per sub-chart: a sourced sub-chart (ChartYAML non-empty) draws its dependency
// name AND version from the vendored chart's own Chart.yaml — Helm binds a
// dependency to a vendored sub-chart by that chart's own name and version rather
// than by the directory it sits in, so a version drawn from the spec/component
// version would leave the alias unbound and the umbrella's per-instance values
// would never reach the sub-chart. A generative sub-chart (ChartYAML empty) uses
// name == alias and version from the sub's Version (or the default).
//
// The alias is the encoded instance id, so Helm scopes the umbrella's
// per-instance values under that key. Because the encoding is injective, two
// instances reach one alias only where their instance ids are the same string —
// so no instance's values can arrive at another instance's sub-chart, however
// the instance ids are punctuated. The dependency's repository is the one place
// the instance id appears as authored rather than encoded; the comment at the
// dependency says why.
func AssembleUmbrellaCore(boundaryName, aggregateName string, subs []CoreSubChart, chartNameOverride string, indent int) (map[string][]byte, error) {
	if boundaryName == "" {
		return nil, cerrors.New(cerrors.OpsError, cerrors.SourceLocation{}, "helmchart umbrella: empty boundary name", nil, nil)
	}

	// Sorted iteration for determinism
	sort.Slice(subs, func(i, j int) bool {
		return subs[i].InstanceID < subs[j].InstanceID
	})

	// The chart name is one encoded segment when a single identity names the
	// chart, and two joined segments when the aggregate name qualifies the
	// dispatch path. The encoding's own two-hyphen join is what keeps the
	// qualified form injective: no encoded segment can contain that sequence, so
	// an aggregate name ending in a hyphen and a dispatch path beginning with one
	// cannot assemble into the same name as the other way round.
	var name string
	switch {
	case chartNameOverride != "":
		name = helmNameEncoding.Segment(chartNameOverride)
	case aggregateName != "":
		name = helmNameEncoding.Name(aggregateName, boundaryName)
	default:
		name = helmNameEncoding.Segment(boundaryName)
	}

	deps := make([]umbrellaChartDependency, 0, len(subs))
	values := make(map[string]any, len(subs))
	for _, sc := range subs {
		alias := helmNameEncoding.Segment(sc.InstanceID)
		depName := alias
		version := sc.Version
		if version == "" {
			version = defaultChartVersion
		}
		if len(sc.ChartYAML) > 0 {
			metaName, metaVersion, err := ReadChartMetaBytes(sc.ChartYAML, sc.InstanceID)
			if err != nil {
				return nil, err
			}
			depName = metaName
			version = metaVersion
		}
		// The repository names the sub-chart's own directory under the umbrella,
		// and that directory carries the instance id verbatim — the orchestrator
		// places a nesting provider's per-node files at charts/<instance_id>/ and
		// knows nothing of what Helm accepts in a name, so it encodes nothing.
		// The alias is encoded and the directory is not, so the two are the same
		// word only for an instance id the alphabet already admits: pointing the
		// repository at the alias would name a directory that is not there
		// whenever they differ, which `helm dependency build` and
		// `helm dependency update` resolve and refuse.
		deps = append(deps, umbrellaChartDependency{
			Name:       depName,
			Version:    version,
			Alias:      alias,
			Repository: "file://charts/" + sc.InstanceID,
		})
		if sc.Values != nil {
			values[alias] = sc.Values
		} else {
			values[alias] = map[string]any{}
		}
	}

	umbrella := umbrellaChart{
		APIVersion:   helmChartAPIVersion,
		Name:         name,
		Version:      defaultChartVersion,
		Description:  fmt.Sprintf("Aggrega umbrella chart for boundary %q", boundaryName),
		Dependencies: deps,
	}

	chartYAML, err := rendercore.MarshalYAMLIndent(&umbrella, indent)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: boundaryName}, "helmchart umbrella: marshal Chart.yaml")
	}

	valuesYAML, err := rendercore.MarshalYAMLIndent(values, indent)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: boundaryName}, "helmchart umbrella: marshal values.yaml")
	}

	return map[string][]byte{
		"Chart.yaml":  chartYAML,
		"values.yaml": valuesYAML,
	}, nil
}
