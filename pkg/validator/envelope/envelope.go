// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package envelope validates a raw YAML document against the embedded
// JSON schema for its entity kind — the document-envelope layer that
// runs BEFORE typed unmarshal at every entity parse boundary. It is
// the single implementation of that layer: the workspaceconfig,
// lockfile, and workload loaders consume it rather than carrying
// private copies.
//
// Its reach is the entity-kind envelope, not every validate-then-unmarshal
// site in the tree. A reader of a document that has no entity kind — a
// cache slot's own metadata sidecar, say — validates against its own
// schema and cannot dispatch through this package. Such a reader shares
// the steps it genuinely can share, which are exported for exactly that:
// ParseDecodable, so a document carrying an unquoted template expression
// has one definition of what makes it decodable, and NormaliseYAML, so the
// shape the schema engine consumes has one definition — each holding even
// where the dispatch does not. A reader that decodes into its own typed
// entity takes ParseDecodable and decodes the tree itself; the step that is
// shared is the parse, never the decode, because a generic shape and a
// typed one are different destinations for one document.
//
// The envelope layer is distinct from the content-validation pipeline
// (compact form → resolved JSON Schema → instance validation): the
// envelope checks the document's own structure against its kind's
// published schema; the content pipeline checks pillar payloads
// against schemas the entities themselves declare.
package envelope

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"go.yaml.in/yaml/v3"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/anchorcycle"
	aggerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	clischemas "github.com/aggrega-ai/aggrega-cli/pkg/schemas/jsonschema"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// ErrEntityEnvelope is the sentinel for a document failing its
// kind's envelope schema. errors.Is-recoverable at the pipeline
// boundary.
func ErrEntityEnvelope() error { return errEntityEnvelope }

// The sentence states the condition and stops there. Three paths raise this
// one condition — a document that will not parse, an engine that could not
// run, and a document the schema rejected — and only the third is a document
// the author can correct against a schema. Advice that fits the third is wrong
// on the other two, where the document may be perfectly well-formed, so it is
// supplied by the path where it is true rather than carried here.
var errEntityEnvelope = aggerrors.Register("validator-envelope/entity-envelope", aggerrors.Meta{
	ShortCode: "E0215",
	Message:   "document envelope validation failed",
})

// adviceCorrectDocument is the advice for the schema-violation path, the one
// path on which it is true.
const adviceCorrectDocument = "correct the document to match its kind's schema"

// Kind names a document family with an embedded envelope schema.
type Kind string

const (
	KindComponent        Kind = "Component"
	KindFeature          Kind = "Feature"
	KindRecipe           Kind = "Recipe"
	KindFeatureRecipe    Kind = "FeatureRecipe"
	KindPlugin           Kind = "Plugin"
	KindPluginsTrust     Kind = "PluginsTrust"
	KindSchemaLibrary    Kind = "SchemaLibrary"
	KindMappingLibrary   Kind = "MappingLibrary"
	KindWorkloadLibrary  Kind = "WorkloadLibrary"
	KindArtifactsLibrary Kind = "ArtifactsLibrary"
	KindAggregate        Kind = "Aggregate"
	KindManifest         Kind = "Manifest"
	KindRelease          Kind = "Release"
	KindAggregaConfig    Kind = "AggregaConfig"
	KindLockfile         Kind = "Lockfile"
	KindWorkload         Kind = "Workload"
	// KindIR is the emitted intermediate representation. It is an
	// OUTPUT-side, TEST-ONLY kind: no loader calls ValidateBytes/
	// ValidateInstance with it, so it creates no runtime gate (a gate is
	// a call site, never map membership). It exists so that emission-side
	// conformance checks can validate a REAL emitted IR instance against
	// ir-v1alpha1 through the one validation component. The bidirectional
	// schema↔struct alignment suite deliberately excludes this kind and
	// cannot cover it: the IR types are json-tagged, and that suite's
	// schema-to-Go direction reads yaml tags. Do not wire it into a parse
	// path — runtime emit-validation is deliberately not part of the
	// pipeline.
	KindIR Kind = "IR"
)

// schemaSource maps each kind to its embedded schema document.
var schemaSource = map[Kind]string{
	KindComponent:        schemas.ComponentV1alpha1,
	KindFeature:          schemas.FeatureV1alpha1,
	KindRecipe:           schemas.RecipeV1alpha1,
	KindFeatureRecipe:    schemas.FeatureRecipeV1alpha1,
	KindPlugin:           schemas.PluginV1alpha1,
	KindPluginsTrust:     schemas.PluginsTrustV1alpha1,
	KindSchemaLibrary:    schemas.SchemaLibraryV1alpha1,
	KindMappingLibrary:   schemas.MappingLibraryV1alpha1,
	KindWorkloadLibrary:  schemas.WorkloadLibraryV1alpha1,
	KindArtifactsLibrary: schemas.ArtifactsLibraryV1alpha1,
	KindAggregate:        schemas.AggregateV1alpha1,
	KindManifest:         schemas.ManifestV1alpha1,
	KindRelease:          schemas.ReleaseV1alpha1,
	KindAggregaConfig:    schemas.AggregaConfigV1alpha1,
	KindLockfile:         schemas.LockfileV1alpha1,
	KindWorkload:         clischemas.WorkloadJSONSchema,
	KindIR:               schemas.IRSchemaV1alpha1,
}

// Kinds returns every document family this package validates, in lexical
// order. The returned slice is freshly allocated, so a caller may not
// change the family set through it.
//
// It is the enumeration counterpart to the per-kind validation calls: a
// caller that must reason about the whole family set — checking that its
// own handling accounts for all of them, say — reads that set from here
// rather than restating it. A restated copy agrees on the day it is
// written and disagrees silently the day a family is added.
//
// The set is authoritative for envelope validation and nothing more. It
// is wider than the set of kinds a user authors, because generated and
// configuration documents carry envelope schemas too, so a caller that
// needs only authored kinds must narrow rather than take this whole.
func Kinds() []Kind {
	out := make([]Kind, 0, len(schemaSource))
	for kind := range schemaSource {
		out = append(out, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// compiled caches one compiled validator per kind. Catalog loads
// validate hundreds of documents; the lazy lookup path in particular
// must not recompile per file.
var (
	compiledMu sync.Mutex
	compiled   = map[Kind]jsonvalidator.Validator{}
)

func validatorFor(kind Kind) (jsonvalidator.Validator, error) {
	compiledMu.Lock()
	defer compiledMu.Unlock()
	if v, ok := compiled[kind]; ok {
		return v, nil
	}
	raw, ok := schemaSource[kind]
	if !ok {
		return nil, fmt.Errorf("envelope: no embedded schema for kind %q", kind)
	}
	s := &libschema.Schema{}
	if err := json.Unmarshal([]byte(raw), s); err != nil {
		return nil, fmt.Errorf("envelope: decode embedded %s schema: %w", kind, err)
	}
	v, err := jsonvalidator.New(s)
	if err != nil {
		return nil, fmt.Errorf("envelope: compile embedded %s schema: %w", kind, err)
	}
	compiled[kind] = v
	return v, nil
}

// Error is the structured envelope violation: the file, the entity
// kind, the engine's path-pointed findings, and where in the document
// each of those paths sits. Unwraps to ErrEntityEnvelope.
type Error struct {
	Kind     Kind
	Path     string
	Findings []jsonvalidator.Finding

	// Positions maps a finding's FieldPath to the source position of the
	// key that path names. It is keyed by path rather than correlated to
	// Findings by index because a position is a property of the path: the
	// engine reports several findings along one path, and they share it.
	//
	// Each entry carries the file as well as the line and column, so a
	// caller converting a finding into a categorised compiler error can
	// hand the location straight to that error's source pointer.
	//
	// A path absent from the map is one the document does not address —
	// see yamlpos.FieldNode for which shapes those are. Such a finding
	// renders without a position; no position is substituted for it,
	// because a coordinate a reader has to distrust is worse than none.
	//
	// The map is only ever looked up, never iterated, so it introduces no
	// ordering into the rendered message.
	Positions map[string]aggerrors.SourceLocation
}

// Error renders the violation as one grouped report: the condition's
// registered sentence, the kind and file it was met on, then each finding with
// its position, key path and message, and the advice for this path last.
//
// The sentence leads and is read off the condition rather than written here,
// so the words a reader sees have one home. The header states no framing of its
// own: that the document failed its envelope validation is exactly what the
// registered sentence says, and a header repeating it would state the condition
// twice in one line.
//
// The header establishes the file, so a finding carries only its line and
// column rather than repeating the path — the same grouping the compiler's
// other multi-finding schema report uses, and the difference between a
// readable diagnostic and one that restates an absolute path five times.
func (e *Error) Error() string {
	values := make([]string, 0, 2)
	if e.Kind != "" {
		values = append(values, string(e.Kind))
	}
	if e.Path != "" {
		values = append(values, "at "+e.Path)
	}
	var b strings.Builder
	b.WriteString(aggerrors.ComposeMessage(ErrEntityEnvelope(), values, ""))
	for i, f := range e.Findings {
		if i >= 4 {
			fmt.Fprintf(&b, "; (+%d more)", len(e.Findings)-4)
			break
		}
		fp := f.FieldPath
		if fp == "" {
			fp = "(root)"
		}
		b.WriteString("; ")
		if loc, ok := e.Positions[f.FieldPath]; ok && loc.Line > 0 {
			fmt.Fprintf(&b, "%d:%d: ", loc.Line, loc.Column)
		}
		fmt.Fprintf(&b, "%s: %s", fp, f.Message)
	}
	b.WriteString(". " + adviceCorrectDocument)
	return b.String()
}

func (e *Error) Unwrap() error { return ErrEntityEnvelope() }

// envelopeFailure attaches ErrEntityEnvelope to a validation failure that has
// no per-field findings to report — a document that will not parse, or an
// engine that could not run.
//
// It reports the condition's registered sentence and then the underlying
// failure, which is the composition every diagnostic naming a condition
// follows. What makes that safe on these paths is that the sentence states the
// condition and nothing more. A sentence carrying advice to correct the
// document could not lead here: that advice is sound for a malformed document
// and wrong for an engine failure, where the document may be perfectly
// well-formed. Keeping the advice with the path it is true of is what lets the
// sentinel be both the matcher these paths are gated on and the sentence they
// open with.
//
// The cause is the detail, and it is the whole detail: a parse failure names
// the document and what would not read, an engine failure names the stage that
// could not run, and neither has advice this side of the failure can add that
// the cause does not already carry.
type envelopeFailure struct {
	cause error
}

func (e *envelopeFailure) Error() string {
	return aggerrors.ComposeMessage(ErrEntityEnvelope(), nil, "") + ": " + e.cause.Error()
}

// Unwrap reports the cause and the sentinel together, so a caller matching
// either the specific cause or ErrEntityEnvelope succeeds.
func (e *envelopeFailure) Unwrap() []error { return []error{e.cause, ErrEntityEnvelope()} }

// ValidateBytes parses raw YAML document bytes, validates the
// document against kind's embedded schema, and returns the parsed,
// normalised generic instance for caller reuse.
//
// Every error it returns wraps ErrEntityEnvelope. A caller may therefore
// gate on that one sentinel and cannot fall past a failure class by
// omission — which matters most where the document is well-formed and the
// engine is what failed, since the caller's own typed decode of those same
// bytes then succeeds and would carry the build forward with this check
// never having run.
//
// Every error it returns opens with that condition's registered sentence, so a
// reader is told which condition was met before being told anything about the
// occurrence. A schema violation returns *Error, carrying one finding per
// violation and the source position of each violated path, resolved against
// the bytes supplied here, and closes with the advice for that path. A parse
// failure and an engine failure have no per-field findings, so the sentence is
// followed by the underlying failure's own message and nothing else.
//
// A document whose anchor is aliased inside its own content is refused as a
// parse failure, naming that anchor. It describes an infinite tree rather
// than a malformed one, so there is nothing to validate and no position to
// report; depth on its own is never a reason for this refusal.
func ValidateBytes(kind Kind, data []byte, path string) (any, error) {
	var instance any
	if err := yaml.Unmarshal(data, &instance); err != nil {
		// A generic map decode cannot hold a mapping-shaped key, which is what
		// an unquoted template expression parses as. ParseDecodable yields a
		// tree that decodes, so such a document still reaches validation. The
		// decode above is an entirely ordinary fast path for the documents that
		// need no repair; the repair itself is reachable on its own.
		node, perr := ParseDecodable(data, path)
		if perr != nil {
			return nil, &envelopeFailure{cause: perr}
		}
		instance = nodeToAny(node)
	}
	if instance == nil {
		return nil, &Error{Kind: kind, Path: path, Findings: []jsonvalidator.Finding{{Message: "document is empty"}}}
	}
	instance = NormaliseYAML(instance)
	findings, err := ValidateInstance(kind, instance)
	if err != nil {
		return instance, &envelopeFailure{cause: err}
	}
	if len(findings) > 0 {
		return instance, newSchemaError(kind, path, data, findings)
	}
	return instance, nil
}

// newSchemaError builds the schema violation and positions each finding
// against the document's own bytes, so a reader is told where to look
// rather than only what is wrong.
//
// The position walk runs here, once, on the failure path only, and it is
// the sole place an envelope finding acquires a position: every surface
// that reports one — the CLI's rendered message and the editor's
// diagnostics alike — resolves the path through yamlpos.FieldNode, so the
// two cannot disagree about where a violation sits.
//
// Positions are UTF-8 columns, matching the byte columns yaml.v3 itself
// records and the source pointers the rest of the compiler's diagnostics
// carry. A consumer reporting positions in another encoding converts them
// through its own LineIndex rather than re-walking the document.
//
// A position is only ever a position in the bytes handed to ValidateBytes,
// which is why it is resolved here and not further out. That makes one rule
// binding on callers: a caller validating bytes the author did not write —
// the output of a rendered template, say, rather than the contents of a file
// — must not surface these coordinates as though they pointed into an
// authoring file. They point into the text that was validated. A line number
// aimed at text the author never typed is worse than no line number, so such
// a caller reports the finding without a position and leaves the reader to
// the key path.
func newSchemaError(kind Kind, path string, data []byte, findings []jsonvalidator.Finding) *Error {
	e := &Error{Kind: kind, Path: path, Findings: findings}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// The document did not re-parse as a node tree, so nothing can be
		// positioned. The findings still stand: they came from an instance
		// the generic decode did produce.
		return e
	}
	idx := yamlpos.BuildLineIndexWithEncoding(data, yamlpos.PositionEncodingUTF8)
	e.Positions = make(map[string]aggerrors.SourceLocation, len(findings))
	for _, f := range findings {
		if _, done := e.Positions[f.FieldPath]; done {
			continue
		}
		span, ok := yamlpos.FieldRange(&doc, idx, f.FieldPath)
		if !ok {
			continue
		}
		e.Positions[f.FieldPath] = aggerrors.SourceLocation{
			File:   path,
			Line:   span.Line,
			Column: span.Column,
		}
	}
	return e
}

// ValidateInstance validates an already-parsed, normalised generic
// instance against kind's embedded schema and returns the engine's
// findings. Engine-level failures (schema decode/compile/run) are
// returned as errors distinct from instance findings.
func ValidateInstance(kind Kind, instance any) ([]jsonvalidator.Finding, error) {
	v, err := validatorFor(kind)
	if err != nil {
		return nil, err
	}
	report, err := v.Validate(instance, nil)
	if err != nil {
		return nil, fmt.Errorf("envelope: validate %s instance: %w", kind, err)
	}
	return report.Findings, nil
}

// ParseDecodable parses raw YAML document bytes into a node tree that decodes,
// whatever the caller decodes it into.
//
// A document may carry an unquoted template expression as a value —
// `{{ .parameters.x }}`. YAML reads the braces as a flow mapping, so the
// expression arrives as a mapping-shaped mapping KEY: well-formed YAML that no
// decode can consume, because neither a generic map nor a string-keyed one
// accepts a mapping as a key. Every such key is replaced by an indexed
// placeholder, which is what makes the tree decodable.
//
// The replacement is lossy in exactly one thing: the text of the key it
// replaces, which for a template-valued entry is the expression itself. Nothing
// else moves — values, scalar keys, tags, anchors and positions all survive. So
// a caller reading the tree for declared structure (a type reference, a name, a
// version) reads it intact, while a caller that needs an expression's own text
// works from the raw bytes, which this does not modify. A template expression
// standing where a scalar is required does not decode into one silently: it is
// still a mapping, and the decode says so.
//
// A document whose anchor is aliased inside its own content is refused here,
// naming that anchor: it describes an infinite tree rather than a malformed one,
// so there is nothing to decode. Parsing a node tree does not expand aliases, so
// such a document parses cleanly and fails only when something walks it. The
// refusal therefore sits on this side of every walk, once, rather than in each
// walker: every consumer of the returned tree inherits it and none has to
// rediscover the hazard. Depth on its own is never a reason for it.
func ParseDecodable(data []byte, path string) (*yaml.Node, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("envelope: parse %s: %w", path, err)
	}
	if anchor, found := anchorcycle.Find(&node); found {
		return nil, fmt.Errorf("envelope: parse %s: %s", path, anchorcycle.Explain(anchor))
	}
	scalariseMappingKeys(&node)
	return &node, nil
}

// scalariseMappingKeys replaces every non-scalar mapping key in the tree with an
// indexed placeholder naming its position among the mapping's pairs. It is the
// one place that replacement is decided, so no consumer of a tree has to know
// the rule or restate it.
//
// It descends into a key it replaces as well as into every value. The replaced
// node stays reachable through any alias naming its anchor, and an alias
// reaching an unrepaired subtree would decode differently from the same subtree
// reached structurally — so repairing it keeps one document from having two
// readings. Alias links themselves are not followed: every anchor is defined
// somewhere in the structural tree and is therefore reached exactly once, which
// also makes this walk terminate on a tree the anchor check has not seen.
func scalariseMappingKeys(n *yaml.Node) {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			scalariseMappingKeys(c)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i]; k.Kind != yaml.ScalarNode {
				scalariseMappingKeys(k)
				n.Content[i] = &yaml.Node{
					Kind:   yaml.ScalarNode,
					Tag:    "!!str",
					Value:  fmt.Sprintf("{{template-key-%d}}", i/2),
					Line:   k.Line,
					Column: k.Column,
				}
			}
			scalariseMappingKeys(n.Content[i+1])
		}
	}
}

// nodeToAny converts a yaml.Node tree into the generic shape the
// validator consumes.
//
// Every mapping key it meets is a scalar, because the tree came from
// ParseDecodable and that is what ParseDecodable guarantees. So a key is read
// here and never decided here, which leaves the rule that decides a
// placeholder's text with one home.
//
// It follows alias links without tracking where it has been, so it
// terminates only on a tree whose anchors are free of recursion.
// ParseDecodable establishes that; the check is not repeated here, because a
// guarantee that has one home cannot drift out of agreement with itself.
func nodeToAny(n *yaml.Node) any {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return nodeToAny(n.Content[0])
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = nodeToAny(n.Content[i+1])
		}
		return out
	case yaml.SequenceNode:
		out := make([]any, len(n.Content))
		for i, c := range n.Content {
			out[i] = nodeToAny(c)
		}
		return out
	case yaml.AliasNode:
		return nodeToAny(n.Alias)
	default:
		var v any
		if err := n.Decode(&v); err != nil {
			return n.Value
		}
		return NormaliseYAML(v)
	}
}

// NormaliseYAML walks the value yaml.Unmarshal produced for an `any`
// destination and converts it into the shape the JSON Schema engine
// consumes: map[any]any becomes map[string]any (non-string keys are
// dropped — they cannot exist on a schema-conforming document and a
// retained mangled key would mask the real diagnostic), and time.Time
// values (yaml.v3 auto-detects ISO-8601 literals) become UTC RFC 3339
// strings, preserving the documented wire format.
func NormaliseYAML(v any) any {
	switch val := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			if ks, ok := k.(string); ok {
				out[ks] = NormaliseYAML(vv)
			}
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = NormaliseYAML(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = NormaliseYAML(item)
		}
		return out
	case time.Time:
		return val.UTC().Format(time.RFC3339)
	default:
		return v
	}
}
