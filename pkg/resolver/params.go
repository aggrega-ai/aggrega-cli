// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// InterpolateParameters renders Go template expressions in raw Aggregate
// YAML bytes against a parameter map exposed under the .parameters key
// (e.g. {{ .parameters.db_size }}). Sprig template functions are available.
//
// A reference to a parameter the Release never filled renders as nothing. The
// template runs with missingkey=zero, whose own answer for an absent key is the
// zero value of a map of any — a nil interface, which the printer writes as the
// text "<no value>" and printf "%s" writes as "%!s(<nil>)". Neither is anything
// the author wrote, so an empty string is supplied at the key the template reads
// instead and the read produces nothing. A `default` pipe or an `if` guard over
// such a read behaves exactly as it would over the unfilled parameter, because an
// empty string is as empty and as falsy.
//
// Two reads are deliberately left alone, both because supplying something for
// them would replace a useful outcome with a worse one. A reference the template
// ITERATES keeps iterating as nothing, which is what an author ranging over an
// optional list wants; an empty string there would fail the execution instead. And
// a reference whose PARENT is absent — .parameters.db.host where no db parameter
// was filled — still halts, because creating the parent would trade a loud halt
// for a silently wrong artifact.
//
// A read anchored at a name other than .parameters reaches nothing to supply and
// resolves through missingkey=zero unchanged.
//
// The parameter map the caller passes is never modified: the empty strings are
// written into a copy this function owns, so a parameter the author did not fill
// stays unfilled for every later reader of that map.
//
// Both template-parse and template-execute errors are wrapped as
// ResolverError with ErrInvalidParameterRef.
//
// The Aggregate a build starts from is refused ahead of this function when its
// text is not a template at all: that fault is a rule over what the author wrote
// and is asked where the file is in hand, so the finding names the file. The
// parse below still reports it for a document that rule is never presented with
// — a sub-Aggregate the composition imports, read out of the catalog and
// interpolated one level in.
func InterpolateParameters(raw []byte, params map[string]any) ([]byte, error) {
	tmpl, err := parseAggregateTemplate(raw)
	if err != nil {
		// Retained rather than removed with the rule that now refuses the same
		// fault in the Aggregate a build starts from. That rule is asked over one
		// authored document, and a sub-Aggregate's text is not one of them: it is
		// read from the catalog while the composition is assembled, so nothing
		// presents it and only this raise covers it. The two never report one
		// input twice — the rule halts the root build before this function is
		// reached, and the text it is never shown reaches only here.
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Message: fmt.Sprintf("template parse error: %v", err),
			Err:     ErrInvalidParameterRef(),
		}
	}

	// The tree walked is the tree that runs, so the references seeded for are
	// exactly the references the execution will make.
	data := map[string]any{"parameters": copyParameterTree(params)}
	gotmpl.SeedAbsentLeafKeys(data, gotmpl.CollectRootRefs(tmpl.Tree.Root))

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Message: fmt.Sprintf("template execution error: %v", err),
			Err:     ErrInvalidParameterRef(),
		}
	}

	return buf.Bytes(), nil
}

// parseAggregateTemplate parses an Aggregate's authored text exactly as
// InterpolateParameters renders it: the same delimiters, the same absent-key
// option and the same function set.
//
// It has one home because two surfaces ask one question of one document — the
// rule that refuses an Aggregate whose text is not a template, and the
// interpolation that renders it — and a second configuration would answer that
// question differently rather than identically. The difference is not
// hypothetical, which is why this is a shared function rather than a convention:
// the template helper the rest of the compiler parses through layers Aggrega's
// own helpers over the set below, and Go's parser fails on a function name it
// does not hold. A rule asking through that helper would accept text this
// function refuses, and the build would then halt here instead, with a finding
// that names no file.
//
// The two function sets themselves are deliberately left as they are. Which
// helpers an Aggregate's own text may call is a question about what an author is
// offered, and it is not settled by making one rule agree with one renderer.
func parseAggregateTemplate(raw []byte) (*template.Template, error) {
	return template.New("aggregate").
		Option("missingkey=zero").
		Funcs(sprig.FuncMap()).
		Parse(string(raw))
}

// copyParameterTree returns a copy of a parameter map deep enough that inserting
// into it cannot reach the original: every nested map is rebuilt, and everything
// else is shared because nothing writes through a non-map value.
//
// It is deliberately not copyMap below, and the two must not be merged. copyMap
// is the shallow copy the merge path wants, where a nested map is carried across
// by reference on purpose; this one exists precisely to break that sharing before
// template evaluation writes into the tree. A caller of one that got the other
// would be wrong silently, in opposite directions.
func copyParameterTree(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			out[k] = copyParameterTree(nested)
			continue
		}
		out[k] = v
	}
	return out
}

// LoadParametersImport reads a single parameter YAML file from the
// path supplied at Release.spec.parameters.$import: and returns its
// decoded map. Single-source-only at this site; multi-source layering
// is the province of Release extends: chains where the parent Release
// carries base parameters and the child extends + overrides.
//
// Read or unmarshal errors are wrapped as ResolverError with ErrYAMLLoad and
// the offending path attached. That condition is the one every file a build
// could not turn into a document reports under, and the finding's own locator is
// what says this one was named by a Release's parameters import — see
// ErrYAMLLoad for why the naming key does not earn an identity of its own.
func LoadParametersImport(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Release",
			Field:   fmt.Sprintf(".spec.parameters.$import: %s", path),
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}
	var params map[string]any
	if err := yaml.Unmarshal(data, &params); err != nil {
		return nil, &ResolverError{
			Entity:  "Release",
			Field:   fmt.Sprintf(".spec.parameters.$import: %s", path),
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}
	return params, nil
}

// MergeParameters returns the combined parameter map for template use,
// given the Release.spec.parameters discriminator and any external
// values loaded via LoadParametersImport.
//
// Precedence is Release-wins-per-key when both inline and external
// are present — but post-migration the discriminator enforces
// exactly-one-mode (inline OR import), so the two-side merge is
// vestigial. The function is preserved for the (currently unused)
// future case where extends: chains supply external + inline at the
// same Release.
//
// Author order of inline Release parameters is lost at this boundary
// because Go's text/template data is plain map[string]any, which does
// not preserve insertion order. The lost ordering does not affect the
// rendered output — template references are by key, not by iteration
// order.
func MergeParameters(release types.ParametersBlock, external map[string]any) map[string]any {
	// Flattening here is load-bearing for deepMerge below, whose recursion
	// recognises only plain maps; see the contract note on that function.
	releasePlain := orderedMapToPlain(release.Inline)
	if releasePlain == nil && external == nil {
		return map[string]any{}
	}
	if external == nil {
		return copyMap(releasePlain)
	}
	if releasePlain == nil {
		return copyMap(external)
	}
	return deepMerge(external, releasePlain)
}

// orderedMapToPlain flattens an OrderedMap[any] into a plain map for
// template evaluation, recursively unwrapping nested *OrderedMap[any]
// values so Go templates can walk the tree via field-access syntax
// (.foo.bar.baz). Returns nil when the input is empty.
func orderedMapToPlain(m types.OrderedMap[any]) map[string]any {
	if m.Len() == 0 {
		return nil
	}
	return m.TemplateAlphabeticalMapDeep()
}

// deepMerge recursively merges child into base. Maps recurse; scalars/arrays replace.
//
// Both parameters are plain map[string]any by contract, and the recursion depends
// on it: the assertions below recognise only map[string]any, so a nested
// *types.OrderedMap[any] would fall to the replace branch and discard the base
// subtree rather than merging it — silently, with no error. MergeParameters, the
// sole caller, upholds that contract from both sides: inline parameters are
// flattened through orderedMapToPlain, and imported ones decode into a bare map.
// Widening this signature to any, or dropping that flattening, needs an explicit
// *types.OrderedMap[any] arm here first.
func deepMerge(base, child map[string]any) map[string]any {
	if base == nil && child == nil {
		return map[string]any{}
	}
	if base == nil {
		return copyMap(child)
	}
	if child == nil {
		return copyMap(base)
	}
	result := copyMap(base)
	for k, cv := range child {
		bv, exists := result[k]
		if !exists {
			result[k] = cv
			continue
		}
		bMap, bIsMap := bv.(map[string]any)
		cMap, cIsMap := cv.(map[string]any)
		if bIsMap && cIsMap {
			result[k] = deepMerge(bMap, cMap)
		} else {
			result[k] = cv
		}
	}
	return result
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
