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
// boundary. Every refusal this package's gate makes matches it, including
// one reported under a condition of its own, such as
// ErrDuplicatePipelineStepID.
func ErrEntityEnvelope() error { return errEntityEnvelope }

// The sentence states the condition and stops there. Three paths raise this
// one condition — a document that will not parse, an engine that could not
// run, and a document the schema rejected — and only the third is a document
// the author can correct against a schema. Advice that fits the third is wrong
// on the other two, where the document may be perfectly well-formed, so it is
// supplied by the path where it is true rather than carried here.
var errEntityEnvelope = aggerrors.Register("entity-envelope-invalid", aggerrors.Meta{
	ShortCode: "E0215",
	Message:   "document envelope validation failed",
	RulePlace: aggerrors.RuleOverAuthoredInput(),
})

// adviceCorrectDocument is the advice for the schema-violation path, the one
// path on which it is true.
const adviceCorrectDocument = "correct the document to match its kind's schema"

// Kind names a document family with an embedded envelope schema.
type Kind string

const (
	KindComponent       Kind = "Component"
	KindFeature         Kind = "Feature"
	KindRecipe          Kind = "Recipe"
	KindFeatureRecipe   Kind = "FeatureRecipe"
	KindPlugin          Kind = "Plugin"
	KindPluginsTrust    Kind = "PluginsTrust"
	KindSchemaLibrary   Kind = "SchemaLibrary"
	KindMappingLibrary  Kind = "MappingLibrary"
	KindWorkloadLibrary Kind = "WorkloadLibrary"
	KindArtifactLibrary Kind = "ArtifactLibrary"
	KindAggregate       Kind = "Aggregate"
	KindManifest        Kind = "Manifest"
	KindRelease         Kind = "Release"
	KindAggregaConfig   Kind = "AggregaConfig"
	KindLockfile        Kind = "Lockfile"
	KindEntityLock      Kind = "EntityLock"
	KindWorkload        Kind = "Workload"
	// KindIR is the emitted intermediate representation. It is an
	// OUTPUT-side, TEST-ONLY kind: no loader calls ValidateBytes/
	// ValidateInstance with it, so it creates no runtime gate (a gate is
	// a call site, never map membership). It exists so that emission-side
	// conformance checks can validate a REAL emitted IR instance against
	// ir-v1beta1 through the one validation component. The bidirectional
	// schema↔struct alignment suite deliberately excludes this kind and
	// cannot cover it: the IR types are json-tagged, and that suite's
	// schema-to-Go direction reads yaml tags. Do not wire it into a parse
	// path — runtime emit-validation is deliberately not part of the
	// pipeline.
	KindIR Kind = "IR"
)

// schemaSource maps each kind to its embedded schema document.
var schemaSource = map[Kind]string{
	KindComponent:       schemas.ComponentV1beta1,
	KindFeature:         schemas.FeatureV1beta1,
	KindRecipe:          schemas.RecipeV1beta1,
	KindFeatureRecipe:   schemas.FeatureRecipeV1beta1,
	KindPlugin:          schemas.PluginV1beta1,
	KindPluginsTrust:    schemas.PluginsTrustV1beta1,
	KindSchemaLibrary:   schemas.SchemaLibraryV1beta1,
	KindMappingLibrary:  schemas.MappingLibraryV1beta1,
	KindWorkloadLibrary: schemas.WorkloadLibraryV1beta1,
	KindArtifactLibrary: schemas.ArtifactLibraryV1beta1,
	KindAggregate:       schemas.AggregateV1beta1,
	KindManifest:        schemas.ManifestV1beta1,
	KindRelease:         schemas.ReleaseV1beta1,
	KindAggregaConfig:   schemas.AggregaConfigV1beta1,
	KindLockfile:        schemas.LockfileV1beta1,
	KindEntityLock:      schemas.EntityLockV1beta1,
	KindWorkload:        clischemas.WorkloadJSONSchema,
	KindIR:              schemas.IRSchemaV1beta1,
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

// compiled caches one compiled validator per kind and per notice withholding
// every value — the empty notice being the validator that withholds only what the
// schema declares sensitive. Catalog loads validate hundreds of documents; the
// lazy lookup path in particular must not recompile per file.
var (
	compiledMu sync.Mutex
	compiled   = map[compiledKey]jsonvalidator.Validator{}
)

// compiledKey names one compiled validator: the kind whose schema it holds, and
// the notice it withholds every value with, empty for none.
type compiledKey struct {
	kind          Kind
	withholdEvery string
}

func compiledValidator(key compiledKey) (jsonvalidator.Validator, error) {
	compiledMu.Lock()
	defer compiledMu.Unlock()
	if v, ok := compiled[key]; ok {
		return v, nil
	}
	kind := key.kind
	raw, ok := schemaSource[kind]
	if !ok {
		return nil, fmt.Errorf("envelope: no embedded schema for kind %q", kind)
	}
	s := &libschema.Schema{}
	if err := json.Unmarshal([]byte(raw), s); err != nil {
		return nil, fmt.Errorf("envelope: decode embedded %s schema: %w", kind, err)
	}
	// A published document schema that declares a value sensitive means the value
	// is secret — a private key picked by mistake where a public key belongs, say —
	// so a finding here never quotes it. Every caller of this gate renders the
	// findings it returns, the terminal, the editor and the tool protocol alike, so
	// deciding it once here is what keeps each of them from printing the value.
	opts := []jsonvalidator.Option{jsonvalidator.WithholdSensitiveValues()}
	if key.withholdEvery != "" {
		opts = append(opts, jsonvalidator.WithholdEveryValue(key.withholdEvery))
	}
	v, err := jsonvalidator.New(s, opts...)
	if err != nil {
		return nil, fmt.Errorf("envelope: compile embedded %s schema: %w", kind, err)
	}
	compiled[key] = v
	return v, nil
}

// Error is the structured envelope violation: the file, the entity
// kind, the path-pointed findings, and where in the document each of
// those paths sits. Matches ErrEntityEnvelope.
//
// Most violations are the document failing its kind's schema, and their
// findings are the schema engine's. A few rules the gate applies cannot be
// written in a schema at all — a step id repeated within one pipeline is one —
// and a violation of such a rule reports its own registered condition rather
// than the schema one, because the schema accepts the document and advice to
// correct it against the schema would send the author to something that
// confirms nothing. Such a violation still matches ErrEntityEnvelope, so a
// caller gating on the gate's one sentinel keeps catching every refusal the
// gate makes; the condition it names first, and therefore the short code a
// diagnostic prints, is its own.
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

	// condition is the registered condition the findings report when it is
	// not the schema one, and advice the advice true of it; both are empty for
	// a schema violation. They are unexported so that only this package, which
	// applies the rules behind them, can raise one; a copy of the whole value
	// carries them along.
	condition error
	advice    string
}

// Condition returns the registered condition the violation reports: the one
// the rule it broke is registered under, or ErrEntityEnvelope for a document
// failing its kind's schema. A surface composing its own wording for the
// violation reads the sentence through this, so it opens with the same
// condition the error's own rendering and its short code name.
func (e *Error) Condition() error {
	if e.condition != nil {
		return e.condition
	}
	return ErrEntityEnvelope()
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
	b.WriteString(aggerrors.ComposeMessage(e.Condition(), values, ""))
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
	advice := adviceCorrectDocument
	if e.condition != nil {
		advice = e.advice
	}
	if advice != "" {
		b.WriteString(". " + advice)
	}
	return b.String()
}

// Unwrap reports the condition the violation names, so the first registered
// identity in its chain — the one a short code is read from — is that
// condition.
func (e *Error) Unwrap() error { return e.Condition() }

// Is reports a violation of a rule with a condition of its own as
// ErrEntityEnvelope too, so a caller gating on the gate's one sentinel matches
// every refusal the gate makes. A schema violation needs no such report: its
// condition is ErrEntityEnvelope, reached through Unwrap.
func (e *Error) Is(target error) bool {
	return e.condition != nil && target == ErrEntityEnvelope()
}

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
// A document declaring an API version this compiler no longer reads returns
// *Error under ErrRetiredAPIVersion before its schema is consulted, one finding
// positioned at its `apiVersion` line naming the version it declares and the
// version the compiler reads; that error matches ErrEntityEnvelope as well. A
// Plugin declaring a plugin ABI version the compiler no longer speaks is refused
// the same way, under the condition a guest declaring it is refused under at load,
// with its finding at `spec.abiVersion`.
//
// A Recipe or FeatureRecipe the schema accepts is then held to the one rule its
// schema cannot state: no two steps of its pipeline share an id. A document
// breaking it returns *Error under ErrDuplicatePipelineStepID, one finding per
// repeated id positioned at the repeat and naming the step that declared the id
// first, and that error matches ErrEntityEnvelope as well.
//
// A document whose anchor is aliased inside its own content is refused as a
// parse failure, naming that anchor. It describes an infinite tree rather
// than a malformed one, so there is nothing to validate and no position to
// report; depth on its own is never a reason for this refusal.
func ValidateBytes(kind Kind, data []byte, path string) (any, error) {
	return validateBytes(compiledKey{kind: kind}, data, path)
}

// ValidateBytesWithholdingEvery is ValidateBytes for a document any of whose
// values may have been produced from data an author declared sensitive — a
// document a template rendered, say — where which of them carry such data cannot
// be told value by value. Every finding is reported without the value it found,
// its sentence closed by notice; see jsonschema.WithholdEveryValue for which
// sentences are kept because they quote no value. A failure to parse the bytes
// is reported without the parser's own text, which quotes the document.
func ValidateBytesWithholdingEvery(kind Kind, data []byte, path, notice string) (any, error) {
	return validateBytes(compiledKey{kind: kind, withholdEvery: notice}, data, path)
}

func validateBytes(key compiledKey, data []byte, path string) (any, error) {
	kind := key.kind
	var instance any
	if err := yaml.Unmarshal(data, &instance); err != nil {
		// A generic map decode cannot hold a mapping-shaped key, which is what
		// an unquoted template expression parses as. ParseDecodable yields a
		// tree that decodes, so such a document still reaches validation. The
		// decode above is an entirely ordinary fast path for the documents that
		// need no repair; the repair itself is reachable on its own.
		node, perr := ParseDecodable(data, path)
		if perr != nil {
			if key.withholdEvery != "" {
				// A parser's own text quotes the document it could not read, and
				// every value in this document may carry declared data.
				perr = fmt.Errorf("parse %s: the parser's text is withheld%s", path, key.withholdEvery)
			}
			return nil, &envelopeFailure{cause: perr}
		}
		instance = nodeToAny(node)
	}
	if instance == nil {
		return nil, &Error{Kind: kind, Path: path, Findings: []jsonvalidator.Finding{{Message: "document is empty"}}}
	}
	instance = NormaliseYAML(instance)
	// A document declaring a version this compiler no longer reads is told so
	// before anything else is said about it: every other finding would be one
	// about a version of the document the compiler does not read.
	if err := refuseRetired(key, path, data, instance); err != nil {
		return instance, err
	}
	findings, err := validateInstance(key, instance)
	if err != nil {
		return instance, &envelopeFailure{cause: err}
	}
	if len(findings) > 0 {
		return instance, newSchemaError(kind, path, data, findings)
	}
	// The rules no schema can state run on a document the schema accepted, so
	// each reads a shape the schema has already confirmed, and a document
	// failing both reports the schema's findings first.
	if err := pipelineStepIDsUnique(key, path, data); err != nil {
		return instance, err
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
// A position's column counts characters (code points), the unit yaml.v3
// records a node's column in, so the column the rendered message prints
// does not grow with the bytes a multi-byte character before the key takes.
// A surface reporting positions in another encoding resolves the same path
// through yamlpos.FieldRange over a LineIndex built in that encoding.
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
	locate, ok := fieldLocator(data, path)
	if !ok {
		// The document did not re-parse as a node tree, so nothing can be
		// positioned. The findings still stand: they came from an instance
		// the generic decode did produce.
		return e
	}
	e.Positions = make(map[string]aggerrors.SourceLocation, len(findings))
	for _, f := range findings {
		if _, done := e.Positions[f.FieldPath]; done {
			continue
		}
		if loc, ok := locate(f.FieldPath); ok {
			e.Positions[f.FieldPath] = loc
		}
	}
	return e
}

// fieldLocator returns a lookup from a field path to its position in data, the
// one way a finding this package reports acquires a position: through the shared
// walk, in characters, as newSchemaError describes. ok is false when data does
// not re-parse as a node tree; the lookup returned then resolves nothing, so its
// findings render without a position rather than with one a reader would have to
// distrust.
func fieldLocator(data []byte, path string) (locate func(string) (aggerrors.SourceLocation, bool), ok bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return func(string) (aggerrors.SourceLocation, bool) { return aggerrors.SourceLocation{}, false }, false
	}
	idx := yamlpos.BuildLineIndexWithEncoding(data, yamlpos.PositionEncodingUTF32)
	return func(fieldPath string) (aggerrors.SourceLocation, bool) {
		span, ok := yamlpos.FieldRange(&doc, idx, fieldPath)
		if !ok {
			return aggerrors.SourceLocation{}, false
		}
		return aggerrors.SourceLocation{File: path, Line: span.Line, Column: span.Column}, true
	}, true
}

// ValidateInstance validates an already-parsed, normalised generic
// instance against kind's embedded schema and returns the engine's
// findings. Engine-level failures (schema decode/compile/run) are
// returned as errors distinct from instance findings.
func ValidateInstance(kind Kind, instance any) ([]jsonvalidator.Finding, error) {
	return validateInstance(compiledKey{kind: kind}, instance)
}

// ValidateInstanceWithholdingEvery is ValidateInstance for an instance any of
// whose values may have been produced from data an author declared sensitive;
// every finding is reported without the value it found, its sentence closed by
// notice. See ValidateBytesWithholdingEvery.
func ValidateInstanceWithholdingEvery(kind Kind, instance any, notice string) ([]jsonvalidator.Finding, error) {
	return validateInstance(compiledKey{kind: kind, withholdEvery: notice}, instance)
}

func validateInstance(key compiledKey, instance any) ([]jsonvalidator.Finding, error) {
	kind := key.kind
	v, err := compiledValidator(key)
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
