// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
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
// for a silently wrong artifact. A reference the template both prints and iterates,
// unfilled, can be given neither treatment, so it halts with
// gotmpl.ErrAbsentPathReadAndIterated naming the reference.
//
// A read anchored at a name other than .parameters reaches nothing to supply and
// resolves through missingkey=zero unchanged.
//
// The parameter map the caller passes is never modified: the empty strings are
// written into a copy this function owns, so a parameter the author did not fill
// stays unfilled for every later reader of that map.
//
// Both template-parse and template-execute errors are wrapped as
// ResolverError with ErrInvalidParameterRef. The refusal of a reference both
// printed and iterated is wrapped as ResolverError too, under its own condition.
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
	data := map[string]any{parametersRoot: copyParameterTree(params)}
	refs := gotmpl.CollectRootRefs(tmpl.Tree.Root)
	if err := gotmpl.SeedAbsentLeafKeys(data, refs, []string{parametersRoot}); err != nil {
		// The refusal is already composed for its own condition and names the
		// path, so the finding carries it whole under that condition rather than
		// under the interpolation's.
		return nil, &ResolverError{Entity: "Aggregate", Err: err}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Message: undefinedRootReads(refs) + fmt.Sprintf("template execution error: %v", err),
			Err:     ErrInvalidParameterRef(),
		}
	}

	return buf.Bytes(), nil
}

// parametersRoot is the one root an Aggregate's own text is rendered against: the
// key the fulfilled parameters sit under in the data the text is executed with.
const parametersRoot = "parameters"

// undefinedRootReads names, for a failed render, the references the template
// makes through a root an Aggregate's text does not define — .values, say, written
// where it means a value a later stage supplies — and states the one root it does
// define. It returns the empty string when every reference reads .parameters.
//
// It exists because the condition's own sentence speaks of a .parameters
// expression, and a render that failed on a read of another root would otherwise
// send its reader to look for a parameter: the reference that failed is named
// here, as the author spelled it, ahead of the template engine's own report.
//
// A reference through an undefined root does not always fail the render — one
// with a single segment reads as nothing — so this is said only once the render
// has failed, and every such reference is named, since the engine's report
// locates the failing one while the author may have written several.
func undefinedRootReads(refs []gotmpl.RootRef) string {
	var paths []string
	for _, ref := range refs {
		if len(ref.Segs) > 0 && ref.Segs[0] != parametersRoot {
			paths = append(paths, ref.Path)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	return fmt.Sprintf("the template reads %s, and an Aggregate's text defines no root but %s; ",
		strings.Join(paths, ", "), parametersReferenceRoot)
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
//
// What this set does take from the shared template layer is its replacement for
// Go's own `index`, and only that: a read of a map key the map does not hold
// halts naming the key here exactly as it does on every other template surface,
// rather than printing the engine's placeholder text into the document.
func parseAggregateTemplate(raw []byte) (*template.Template, error) {
	return template.New("aggregate").
		Option("missingkey=zero").
		Funcs(aggregateTemplateFuncs()).
		Parse(string(raw))
}

// aggregateTemplateFuncs is the function set an Aggregate's own templates are
// parsed and rendered with — its text, and the validate: predicates its parameter
// contracts declare: Sprig's, with the shared replacement for `index` merged over
// it.
func aggregateTemplateFuncs() template.FuncMap {
	funcs := sprig.FuncMap()
	// Iterating a map is safe here where it usually would not be: the loop only
	// assigns distinct keys into another map, so the merged set is the same
	// whatever order the keys arrive in.
	maps.Copy(funcs, gotmpl.BuiltinReplacements())
	return funcs
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

// LoadParametersImport reads the parameter file a Release's
// spec.parameters.$import names and returns its decoded map. Single-source-only
// at this site; multi-source layering is the province of Release extends: chains
// where the parent Release carries base parameters and the child extends +
// overrides.
//
// It is the one anchor every parameters import is resolved by, whether the
// Release heads no chain or is a link of one: ref, the value as written, is
// resolved beside the Release that names it — relative to the directory holding
// releaseFile, the Release's file as this process opens it — and an absolute ref
// names a place of its own. So which file a Release imports depends on where the
// Release is, never on the directory a command was run from, and every surface
// reads the same file. A Release assembled in memory has no file to stand beside;
// an empty releaseFile resolves a relative ref as the process opens any relative
// path.
//
// The resolved file is then held (readscope.CheckReference) to the root picked
// for anchor, the directory of the Release the resolution started at — the
// Release itself where it heads no chain, the head of the chain where it is a
// link of one — given the workspace at workspaceRoot: the workspace, or anchor
// itself where that Release was named from outside the workspace. An import
// leading out of that root halts with readscope.ErrReferenceLeadsOutOfWorkspace,
// and nothing is read. An empty anchor holds the import to the workspace.
//
// Read or unmarshal errors are wrapped as ResolverError with ErrYAMLLoad and
// the offending path attached. That condition is the one every file a build
// could not turn into a document reports under, and the finding's own locator is
// what says this one was named by a Release's parameters import — see
// ErrYAMLLoad for why the naming key does not earn an identity of its own.
func LoadParametersImport(workspaceRoot, anchor, releaseFile, ref string) (map[string]any, error) {
	path := ref
	if !filepath.IsAbs(ref) {
		path = filepath.Join(filepath.Dir(releaseFile), ref)
	}
	if err := readscope.CheckReference(workspaceRoot, readscope.Reference{
		Document: releaseFile, Anchor: anchor, From: filepath.Dir(releaseFile), Key: ".spec.parameters.$import",
		Value: ref, Target: path,
	}); err != nil {
		return nil, err
	}
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
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", authoredshape.DescribeYAMLDecodeErrorIn(err, data)),
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

// deepMerge recursively merges child into base. Maps recurse; scalars/arrays
// replace; a child key written as null removes what the key held.
//
// Removal is the same concept the annotation cascade publishes, and it is spelled
// the same way here on purpose: a key an author writes as null is absent from the
// result rather than present holding nothing. It is applied whether or not the
// key was inherited, because "written null" means the same thing in both cases
// and a reader cannot be asked to know which one they are in.
//
// Both parameters are plain map[string]any by contract, and the recursion depends
// on it: the assertions below recognise only map[string]any, so a nested
// *types.OrderedMap[any] would fall to the replace branch and discard the base
// subtree rather than merging it — silently, with no error. Its callers uphold
// that contract from both sides: inline parameters are flattened through
// orderedMapToPlain, and imported ones decode into a bare map. Widening this
// signature to any, or dropping that flattening, needs an explicit
// *types.OrderedMap[any] arm here first.
func deepMerge(base, child map[string]any) map[string]any {
	if base == nil && child == nil {
		return map[string]any{}
	}
	if base == nil {
		return copyMapWithoutRemovals(child)
	}
	if child == nil {
		return copyMap(base)
	}
	result := copyMap(base)
	for k, cv := range child {
		if cv == nil {
			delete(result, k)
			continue
		}
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

// copyMapWithoutRemovals copies a map that is being used as the child side with
// nothing to merge onto, dropping the keys written as null.
//
// It exists so that a removal reaches the same outcome whether or not the key
// was inherited from anywhere: a lone link writing a key as null contributes no
// key, rather than contributing one that holds nothing for a later link to find.
func copyMapWithoutRemovals(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if v == nil {
			continue
		}
		out[k] = v
	}
	return out
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
