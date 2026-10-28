// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
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

	// PlacedChartYAML is the manifest of the chart as PLACED under the umbrella,
	// which is a different document from ChartYAML above: the render rewrote its
	// name to the identity the placed chart is addressed by, while ChartYAML holds
	// the pristine bytes the author shipped. The dependency's name is read from
	// this one, because a dependency binds to a sub-chart by the name that
	// sub-chart declares — so reading it here is what keeps the entry and the chart
	// from being two derivations that merely agree.
	PlacedChartYAML []byte

	// OutputDirectory is where the assembly's own output sits relative to itself,
	// as composed by the placement that actually wrote the files. It is read rather
	// than rebuilt from the identity: a contributor placed beside another rather
	// than beneath the position its identity names is at a directory no identity
	// spells.
	OutputDirectory string

	// NameBasis is the identity this contributor is addressed by inside the
	// umbrella where that is not InstanceID itself — empty for an ordinary
	// component. It is read only for a contributor whose render placed no manifest:
	// one that placed a manifest is addressed by the name that manifest declares.
	// For a contributor reaching the umbrella under a dispatch identity the
	// compiler built, which no chart-name alphabet accepts, it carries the identity
	// that contributor's own resources are labelled with, so the chart written for
	// it is named exactly as a chart that contributor vendored would be.
	NameBasis string

	// RenderedFiles is the path of each file this contributor's render placed,
	// relative to its own directory, in the order the render emitted them. It is
	// read only for a contributor whose render placed no manifest: those files are
	// finished manifests another provider rendered, sitting where Helm renders
	// nothing, and the chart written for them emits each one by this name.
	RenderedFiles []string
}

// generatedTemplateFileName is the one template a chart written for a
// contributor whose render placed no manifest carries, relative to that chart.
const generatedTemplateFileName = "templates/rendered.yaml"

// contributorChart writes the chart for a contributor whose render placed no
// manifest of its own: a minimal manifest, and one template emitting each file the
// render placed. It returns the files, keyed by their path relative to the
// umbrella, and the manifest, which the dependency entry is then read from exactly
// as it is from a manifest a render placed — so the entry and the chart it points
// at are one statement of the name rather than two derivations.
//
// Such a contributor is a Feature attachment whose FeatureRecipe rendered through
// another provider: its files are finished Kubernetes manifests, placed at the
// root of its own directory under charts/. Helm loads nothing there without a
// manifest, and renders only what sits under templates/, so without this chart the
// umbrella refuses to load — and with a manifest alone it loads and installs none
// of those resources.
//
// The template emits each file through .Files.Get rather than the files being
// moved under templates/, because they are rendered output and not template
// source: a manifest carrying a literal "{{" — an alerting rule's label reference,
// a dashboard's query — would be evaluated a second time by Helm's template engine
// and either fail or change. .Files.Get returns a file's bytes as they are, so what
// installs is exactly what the other provider rendered.
//
// The manifest is named for the contributor's own identity, through the one
// encoding every placed chart's name goes through, and carries the version the
// sub-chart projection names, or the version the umbrella's own manifest carries
// where it names none.
func contributorChart(sc CoreSubChart, indent int) (map[string][]byte, []byte, error) {
	version := sc.Version
	if version == "" {
		version = defaultChartVersion
	}
	manifest, err := rendercore.MarshalYAMLIndent(&umbrellaChart{
		APIVersion: helmChartAPIVersion,
		Name:       helmNameEncoding.Segment(rendercore.ContributorNameBasis(sc.InstanceID, sc.NameBasis)),
		Version:    version,
	}, indent)
	if err != nil {
		return nil, nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: sc.OutputDirectory},
			fmt.Sprintf("helmchart umbrella: marshal the manifest for sub-chart %q", sc.InstanceID))
	}
	files := map[string][]byte{path.Join(sc.OutputDirectory, chartMetadataFileName): manifest}
	if tmpl := renderedFilesTemplate(sc.RenderedFiles); tmpl != nil {
		files[path.Join(sc.OutputDirectory, generatedTemplateFileName)] = tmpl
	}
	return files, manifest, nil
}

// renderedFilesTemplate is the template emitting each rendered manifest by its
// explicit name, in the order given, or nil where none of the files is one Helm
// hands to a template as a plain file. Each name is written as a quoted string
// literal, so a name carrying a quote or a backslash is read back as written.
func renderedFilesTemplate(paths []string) []byte {
	var b strings.Builder
	for _, p := range paths {
		if !emittedAsPlainFile(p) {
			continue
		}
		b.WriteString("---\n{{ .Files.Get ")
		b.WriteString(strconv.Quote(p))
		b.WriteString(" }}\n")
	}
	if b.Len() == 0 {
		return nil
	}
	return []byte(b.String())
}

// emittedAsPlainFile reports whether a rendered file is one the generated template
// emits: a YAML document Helm exposes to a template through .Files. Helm reads a
// chart's values.yaml and requirements.yaml as values and dependencies, renders
// what sits under templates/ itself, and loads what sits under charts/ as further
// charts, so none of those is a plain file and .Files returns nothing for it.
func emittedAsPlainFile(p string) bool {
	switch {
	case !strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml"):
		return false
	case p == "values.yaml", p == "requirements.yaml", p == chartMetadataFileName:
		return false
	case strings.HasPrefix(p, "templates/"), strings.HasPrefix(p, "charts/"):
		return false
	}
	return true
}

// ReadChartMetaBytes parses a chart's Chart.yaml bytes and returns its `name` and
// `version` fields. label identifies the sub-chart in error strings; it carries
// no filesystem path so the guardrails read identically wherever they run, and a
// provider Wasm guest's payload has no host paths to name. It is exported so that
// guest, which lives in a separate package, can drive this decode-and-guardrail
// logic rather than carrying its own copy of it.
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
		// A field of the wrong shape is refused naming the line, the field and the
		// two shapes, read against the file's own bytes, rather than the type the
		// header decodes into. The sentence is carried in the refusal's own message
		// rather than as a cause beneath it: what crosses to the host renders every
		// cause in the chain, and the reworded sentence keeps the decoder's own
		// report beneath it.
		return "", "", cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: label},
			fmt.Sprintf("helmchart sourced: parse Chart.yaml in %q: %s", label, authoredshape.DescribeYAMLDecodeErrorIn(err, data)),
			nil, nil)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", "", cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: label}, fmt.Sprintf("helmchart sourced: Chart.yaml in %q contains multiple YAML documents; a chart's Chart.yaml must be a single document", label), nil, nil)
	}
	if header.Name == "" {
		return "", "", cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: label}, fmt.Sprintf("helmchart sourced: Chart.yaml in %q has empty name", label), nil, nil)
	}
	if header.Version == "" {
		return "", "", cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: label}, fmt.Sprintf("helmchart sourced: Chart.yaml in %q has empty version", label), nil, nil)
	}
	return header.Name, header.Version, nil
}

// AssembleUmbrellaCore produces the umbrella Chart.yaml + values.yaml for a
// dispatch path from the filesystem-free sub-chart views. It is the umbrella
// assembly core — it reads no disk and formats no host path — which is what lets
// a provider Wasm guest call this one byte-identical-critical function. It is
// exported so that guest, which lives in a separate package, can drive this core
// rather than carrying its own copy of it.
//
// chartNameOverride is the cascaded `aggrega.ai/chart-name` value when present;
// when non-empty it is the chart's name verbatim (the merged-path scenario's name
// source). When empty, the name is derived from the dispatch path.
//
// Per sub-chart: a sourced sub-chart (ChartYAML non-empty) draws its dependency
// VERSION from the vendored chart's own Chart.yaml — Helm binds a dependency to
// a vendored sub-chart by that chart's own name and version rather than by the
// directory it sits in, so a version drawn from the spec/component version would
// leave the alias unbound and the umbrella's per-instance values would never
// reach the sub-chart. Its NAME is the alias, and that is where the two halves
// part company: the render that placed the sub-chart rewrote its manifest name to
// the same encoded identity, so the name on both sides is derived from the
// instance rather than written by the chart's author. That is what keeps two
// instances whose vendored charts declare one name from binding both
// dependencies to one of them — a shared version is harmless, a shared name is
// not. A generative sub-chart (ChartYAML empty) uses version from the sub's
// Version (or the default), so the name is the alias on every path.
//
// The alias is the name the placed chart declares, so Helm scopes the umbrella's
// per-instance values under that key. Two contributors reach one alias only where
// their placed charts declare one name, which the render that placed them derives
// injectively from each contributor's own identity — so no contributor's values
// can arrive at another's sub-chart, however the identities are punctuated.
//
// The dependency's repository is a LOCATION and the alias is a NAME, and they are
// different strings by design rather than by accident: the location is where the
// placement wrote the files, and the name is what the chart there declares. Both
// are read rather than derived, which is what keeps a dependency entry from being
// a second derivation that merely agrees with the chart it points at.
func AssembleUmbrellaCore(boundaryName, aggregateName string, subs []CoreSubChart, chartNameOverride string, indent int) (map[string][]byte, error) {
	if boundaryName == "" {
		return nil, cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{}, "helmchart umbrella: empty boundary name", nil, nil)
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
	written := make(map[string][]byte)
	for _, sc := range subs {
		if sc.OutputDirectory == "" {
			return nil, cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{File: boundaryName},
				fmt.Sprintf("helmchart umbrella: sub-chart %q carries no output directory, so the dependency has no directory to point at", sc.InstanceID), nil, nil)
		}
		// A contributor whose render placed no manifest, and whose source the host
		// read none from, is given a chart here, and from then on is read exactly as
		// a contributor whose render placed one: the manifest written for it is the
		// manifest the dependency entry is read from.
		placed := sc.PlacedChartYAML
		if len(placed) == 0 && len(sc.ChartYAML) == 0 {
			files, manifest, err := contributorChart(sc, indent)
			if err != nil {
				return nil, err
			}
			for p, body := range files {
				written[p] = body
			}
			placed = manifest
		}

		// The alias is the name the PLACED chart declares, read from that chart's
		// own manifest rather than derived a second time from the instance id.
		// A dependency binds to a sub-chart by the name the sub-chart declares, so
		// this is the one value that can be right — and for a contributor whose
		// placed name is not derived from its instance id, a second derivation from
		// the instance id would name a chart that is not there. Only a sub-chart
		// whose pristine manifest was projected without a placed one reaches the
		// instance id below; every other carries a placed manifest by now.
		alias := helmNameEncoding.Segment(sc.InstanceID)
		version := sc.Version
		if version == "" {
			version = defaultChartVersion
		}
		if len(placed) > 0 {
			placedName, placedVersion, err := ReadChartMetaBytes(placed, sc.InstanceID)
			if err != nil {
				return nil, err
			}
			alias = placedName
			// The version too, and only where no vendored manifest is projected
			// below. The two documents carry the same version — the render rewrote
			// the name and nothing else — so this changes nothing for a contributor
			// whose pristine bytes the host read, and it is the only statement of
			// the version for one whose bytes it did not.
			if len(sc.ChartYAML) == 0 {
				version = placedVersion
			}
		}
		if len(sc.ChartYAML) > 0 {
			// The version comes from the vendored chart. Helm binds a dependency to
			// a vendored sub-chart by that chart's own name and version, so a
			// version drawn from the spec or component version would leave the alias
			// unbound and the umbrella's per-instance values would never reach the
			// sub-chart.
			_, metaVersion, err := ReadChartMetaBytes(sc.ChartYAML, sc.InstanceID)
			if err != nil {
				return nil, err
			}
			version = metaVersion
		}
		// The repository names the sub-chart's own directory under the umbrella,
		// read from the position the placement composed rather than rebuilt from
		// the identity. The two are the same string for a contributor placed
		// beneath the position its own identity names, and different for one placed
		// BESIDE another — an attached Feature's chart, whose directory joins the
		// coordinates that identify it. Rebuilding from the identity would name a
		// directory that is not there in exactly that case, which `helm dependency
		// build` and `helm dependency update` resolve and refuse.
		//
		// The alias is never it: the alias is a name and this is a location, and
		// they are different strings by design. Pointing the repository at the
		// alias is accepted by a render and refused by dependency resolution, so
		// the mistake survives the tools an author reaches for first. A sub-chart
		// carrying no position was refused at the top of this loop.
		deps = append(deps, umbrellaChartDependency{
			Name:       alias,
			Version:    version,
			Alias:      alias,
			Repository: "file://" + sc.OutputDirectory,
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

	// The files written for contributors sit under each contributor's own
	// directory, which is never the umbrella's root, so none of them can meet the
	// umbrella's own two files.
	written[chartMetadataFileName] = chartYAML
	written["values.yaml"] = valuesYAML
	return written, nil
}
