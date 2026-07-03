// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
)

// SchemaFetchFunc fetches the bytes of a remote schema document named by an
// http or https URL. The provider Wasm guest backs it with the host's pinned
// external-input channel, so every remote schema a chart references is
// digest-pinned and served from the content-addressed store; host-side tests
// back it with an in-memory fake.
type SchemaFetchFunc func(url string) ([]byte, error)

// chartSchemaResourceID is the resource identifier a chart's
// values.schema.json compiles under. It matches the identifier the Helm SDK's
// own validator uses for the same document, so reference resolution and
// validation-failure messages locate errors identically across the two
// validation strategies.
const chartSchemaResourceID = "file:///values.schema.json"

// NewHermeticValuesValidator returns a ValuesValidator that performs the same
// values-schema validation the Helm SDK performs — the chart's
// values.schema.json plus dependency charts' schemas, walked recursively over
// the same per-dependency value scoping — with hermetic reference resolution
// in place of the SDK's live network loader:
//
//   - file $refs (chart-local relative references) resolve from the chart's
//     in-memory file set, never the host filesystem;
//   - http and https $refs resolve through fetch;
//   - urn $refs consult chartutil.URNResolver and fall back to the
//     permissive boolean-true schema, matching the SDK's own urn handling.
//
// Validation is a gate, not a transform: the validator never alters values,
// so rendered bytes are identical whether a chart passes through this
// validator or through the SDK's, and only acceptance can differ.
func NewHermeticValuesValidator(fetch SchemaFetchFunc) ValuesValidator {
	return func(ch *chart.Chart, vals chartutil.Values) error {
		if err := validateChartSchemas(ch, vals, fetch); err != nil {
			// Mirrors the message the SDK wraps its own schema-validation
			// failures with, so the two strategies surface alike.
			return fmt.Errorf("values don't meet the specifications of the schema(s) in the following chart(s):\n%s", err.Error())
		}
		return nil
	}
}

// validateChartSchemas walks the chart and its dependencies exactly as the
// SDK's schema validation does: the chart's own schema validates the full
// values table, and each dependency's schema validates the value subtree
// keyed by the dependency's name, recursively. A dependency with no value
// entry (or a nil one) is skipped; a non-map entry is reported as a type
// error. Failures aggregate per chart, each prefixed by the chart's name.
func validateChartSchemas(ch *chart.Chart, values map[string]any, fetch SchemaFetchFunc) error {
	var sb strings.Builder
	if ch.Schema != nil {
		if err := validateSingleSchemaHermetic(ch, values, fetch); err != nil {
			sb.WriteString(fmt.Sprintf("%s:\n", ch.Name()))
			sb.WriteString(err.Error())
		}
	}

	for _, dep := range ch.Dependencies() {
		raw, exists := values[dep.Name()]
		if !exists || raw == nil {
			continue
		}
		depValues, ok := raw.(map[string]any)
		if !ok {
			sb.WriteString(fmt.Sprintf(
				"%s:\ninvalid type for values: expected object (map), got %T\n",
				dep.Name(), raw,
			))
			continue
		}
		if err := validateChartSchemas(dep, depValues, fetch); err != nil {
			sb.WriteString(err.Error())
		}
	}

	if sb.Len() > 0 {
		return errors.New(sb.String())
	}
	return nil
}

// validateSingleSchemaHermetic validates values against one chart's
// values.schema.json, compiling the schema with the hermetic loaders. The
// failure shaping mirrors the SDK's: the schema library's leading resource
// line is stripped so the surfaced text carries the per-violation lines, and
// a compiler panic on a malformed schema document surfaces as an error rather
// than crashing the render.
func validateSingleSchemaHermetic(ch *chart.Chart, values chartutil.Values, fetch SchemaFetchFunc) (reterr error) {
	defer func() {
		if r := recover(); r != nil {
			reterr = fmt.Errorf("unable to validate schema: %s", r)
		}
	}()

	schema, err := jsonschema.UnmarshalJSON(bytes.NewReader(ch.Schema))
	if err != nil {
		return err
	}

	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(jsonschema.SchemeURLLoader{
		"file":  chartFileLoader{chart: ch},
		"http":  fetchSchemaLoader{fetch: fetch},
		"https": fetchSchemaLoader{fetch: fetch},
		"urn":   urnSchemaLoader{},
	})
	if err := compiler.AddResource(chartSchemaResourceID, schema); err != nil {
		return err
	}
	validator, err := compiler.Compile(chartSchemaResourceID)
	if err != nil {
		return err
	}

	if err := validator.Validate(values.AsMap()); err != nil {
		msg := strings.TrimPrefix(err.Error(), "jsonschema validation failed with '"+chartSchemaResourceID+"#'\n")
		return errors.New(msg + "\n")
	}
	return nil
}

// chartFileLoader resolves file-scheme schema references from the chart's
// in-memory file set. The chart's schema compiles under
// chartSchemaResourceID, so a chart-local relative $ref resolves to a
// file:/// URL whose path names a chart-root-relative file; the loader serves
// that file's bytes from the loaded chart. Nothing is read from the host
// filesystem — a sandboxed guest has none to read, and a hermetic render must
// consume only the chart's own projected content. Each chart in the
// dependency walk carries its own loader, so a dependency's local references
// resolve against the dependency's files.
type chartFileLoader struct {
	chart *chart.Chart
}

// Load implements the schema library's URLLoader contract for the file
// scheme.
func (l chartFileLoader) Load(urlStr string) (any, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("parse schema reference %s: %w", urlStr, err)
	}
	rel := strings.TrimPrefix(u.Path, "/")
	for _, f := range l.chart.Raw {
		if f.Name == rel {
			return jsonschema.UnmarshalJSON(bytes.NewReader(f.Data))
		}
	}
	return nil, fmt.Errorf("schema reference %s names no file in chart %q", urlStr, l.chart.Name())
}

// fetchSchemaLoader resolves http and https schema references through the
// injected fetch function. On the provider Wasm guest the function is the
// pinned external-input channel: a remote reference is served from the
// digest-pinned store, or halts loudly when the closure is frozen and the
// URL is unpinned — it is never fetched ambiently.
type fetchSchemaLoader struct {
	fetch SchemaFetchFunc
}

// Load implements the schema library's URLLoader contract for the http and
// https schemes.
func (l fetchSchemaLoader) Load(urlStr string) (any, error) {
	if l.fetch == nil {
		return nil, fmt.Errorf("no schema fetcher is configured to resolve remote schema reference %s", urlStr)
	}
	b, err := l.fetch(urlStr)
	if err != nil {
		return nil, fmt.Errorf("resolve remote schema reference %s: %w", urlStr, err)
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(b))
}

// urnSchemaLoader mirrors the SDK's urn-scheme handling: it consults the
// SDK's pluggable chartutil.URNResolver and falls back to the permissive
// boolean-true schema when the URN stays unresolved, so a chart the SDK
// accepts is not rejected here over an unresolved URN.
type urnSchemaLoader struct{}

// Load implements the schema library's URLLoader contract for the urn
// scheme.
func (urnSchemaLoader) Load(urlStr string) (any, error) {
	if doc, err := chartutil.URNResolver(urlStr); err == nil && doc != nil {
		return doc, nil
	}
	return jsonschema.UnmarshalJSON(strings.NewReader("true"))
}
