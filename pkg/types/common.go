// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

// Maintainer identifies an entity owner.
// Shared across all entity metadata blocks.
type Maintainer struct {
	Name  string `json:"name" yaml:"name"`
	Email string `json:"email,omitempty" yaml:"email,omitempty"`
	URL   string `json:"url,omitempty" yaml:"url,omitempty"`
}

// SourceDigest is a content pin for one resolved remote source, recorded in the
// entity.lock file beside the entity that fetched it. Identity is
// {Kind, URL, Chart, ModuleSubdir, Version} — URL is the authored source URL (a
// repo root or a direct .tgz for helm-https), and the two selector terms name
// whatever the URL alone does not: Chart names the chart within a repo root, so
// two charts served from one repo root at the same version pin distinctly, and
// ModuleSubdir names the directory within a git-sourced repository, so two
// directories of one repository at one commit pin distinctly. A single entity's
// lock carries more than one entry when its source knobs are templated and
// resolve differently per consuming instance, which is why both selector terms
// belong to the identity here as well as in the lockfile. Digest is the SHA-256 of
// the fetched archive and ManifestDigest is the OCI manifest digest for an
// OCI-distributed source (empty otherwise). The resolution phase writes these into
// the lock from the fetched bytes; the build reads them back and verifies the
// cached source has not drifted. The entity document itself is never written to.
//
// Defined in entity-lock-v1alpha1.schema.json#/properties/sourceDigests/items.
type SourceDigest struct {
	Kind           string `json:"kind" yaml:"kind"`
	URL            string `json:"url" yaml:"url"`
	Chart          string `json:"chart,omitempty" yaml:"chart,omitempty"`
	ModuleSubdir   string `json:"moduleSubdir,omitempty" yaml:"moduleSubdir,omitempty"`
	Version        string `json:"version" yaml:"version"`
	Digest         string `json:"digest" yaml:"digest"`
	ManifestDigest string `json:"manifestDigest,omitempty" yaml:"manifestDigest,omitempty"`
}

// ArtifactDigest is a content pin for one artifact whose value is a file import,
// recorded in the entity.lock file beside the entity that imports it. Name is the
// artifacts: map key; Digest is the SHA-256 of the imported file's bytes. Inline
// (literal) artifacts are part of the entity and carry no separate pin.
//
// Defined in entity-lock-v1alpha1.schema.json#/properties/artifactDigests/items.
type ArtifactDigest struct {
	Name   string `json:"name" yaml:"name"`
	Digest string `json:"digest" yaml:"digest"`
}

// LocalSourceDigest is a content pin for one file of an entity's local source,
// recorded in the entity.lock file beside the entity. Path is the file's
// slash-separated path relative to the directory holding the entity document —
// the path a publication of the entity carries it under — and Digest is the
// SHA-256 of its bytes. A local source is pinned file by file rather than as one
// digest, so a build that finds the source changed can name the file that moved.
//
// Defined in entity-lock-v1alpha1.schema.json#/properties/localSourceDigests/items.
type LocalSourceDigest struct {
	Path   string `json:"path" yaml:"path"`
	Digest string `json:"digest" yaml:"digest"`
}

// IgnoreRules is what an entity's lock records about which files under its local
// source directory belong to it: the plugin that served the entity's capability
// when it was pinned, with the rules that plugin declared, and the exact composed
// rules every later reading of the directory applies.
//
// It is recorded rather than worked out again at each reading, so a change to the
// author's ignore file, to the workspace's provider binding or to the binary moves
// nothing a publication, a comparison or a render reads until the entity is
// pinned again. A lock recording none keeps every file under the directory.
//
// Defined in entity-lock-v1alpha1.schema.json#/properties/ignore.
type IgnoreRules struct {
	Plugin *IgnorePlugin `json:"plugin,omitempty" yaml:"plugin,omitempty"`
	Rules  []IgnoreRule  `json:"rules,omitempty" yaml:"rules,omitempty"`
}

// IgnorePlugin is the plugin an entity's ignore rules were composed from: its
// entity reference and the digest of its entity document, and the patterns and
// ignore-file names it declared.
//
// Defined in entity-lock-v1alpha1.schema.json#/properties/ignore/properties/plugin.
type IgnorePlugin struct {
	Ref      string   `json:"ref" yaml:"ref"`
	Digest   string   `json:"digest" yaml:"digest"`
	Patterns []string `json:"patterns,omitempty" yaml:"patterns,omitempty"`
	Files    []string `json:"files,omitempty" yaml:"files,omitempty"`
}

// IgnoreRule is one composed ignore rule: a pattern in gitignore syntax, the
// directory it is scoped to relative to the entity's own directory ("." for that
// directory), and where it was declared — "plugin", or the path of the ignore file
// it was read from.
//
// Defined in entity-lock-v1alpha1.schema.json#/properties/ignore/properties/rules/items.
type IgnoreRule struct {
	Pattern string `json:"pattern" yaml:"pattern"`
	Within  string `json:"within" yaml:"within"`
	From    string `json:"from" yaml:"from"`
}

// IgnoreRuleFromPlugin is the IgnoreRule.From value of a pattern the plugin
// declared itself, as distinct from a line read out of an ignore file.
const IgnoreRuleFromPlugin = "plugin"

// WellKnownAnnotation is one platform-native annotation key a provider's
// rendering engine reads, with the scope it is read at and the behavior applied
// when it is absent. It is the provider's support claim for that key, and the
// published support matrix is rendered from it.
//
// The compiler reads the claim in one way: a Scope entry naming an input kind
// the provider's own ProviderContract.InputKinds does not claim contradicts an
// adjacent claim in the same entity, and the load halts. Nothing else about a
// build branches on the claim.
type WellKnownAnnotation struct {
	Key             string `json:"key" yaml:"key"`
	Purpose         string `json:"purpose" yaml:"purpose"`
	DefaultBehavior string `json:"defaultBehavior,omitempty" yaml:"defaultBehavior,omitempty"`
	// Scope is where the engine reads this key: the input kinds it is read on
	// (WellKnownAnnotationScopeSourced, ...Synthesized, ...ArtifactsStandalone)
	// and WellKnownAnnotationScopeBoundary for a key resolved once per boundary,
	// for which input kind is not an axis. Empty makes no per-path claim, so the
	// load-time contradiction check has nothing to compare. Entries preserve
	// author order.
	Scope []string `json:"scope,omitempty" yaml:"scope,omitempty"`
}

// ComponentType is the reserved component-type vocabulary declared in
// component-v1alpha1.schema.json. The schema enumerates the values this key
// accepts, so this set mirrors that enumeration and changes with it.
type ComponentType string

const (
	ComponentTypeServer         ComponentType = "server"
	ComponentTypeWorker         ComponentType = "worker"
	ComponentTypeTask           ComponentType = "task"
	ComponentTypeFunction       ComponentType = "function"
	ComponentTypeStatefulServer ComponentType = "stateful-server"
	ComponentTypeResource       ComponentType = "resource"
)

// ValidComponentTypes is the set of valid ComponentType values for quick lookup.
var ValidComponentTypes = map[ComponentType]bool{
	ComponentTypeServer:         true,
	ComponentTypeWorker:         true,
	ComponentTypeTask:           true,
	ComponentTypeFunction:       true,
	ComponentTypeStatefulServer: true,
	ComponentTypeResource:       true,
}

// IsValidComponentType returns true if t is one of the values the reserved
// component-type enumeration declares.
func IsValidComponentType(t ComponentType) bool {
	return ValidComponentTypes[t]
}

// ComponentTypeLabel is the reserved label key carrying the component-type vocabulary.
const ComponentTypeLabel = "component.aggrega.ai/type"

// FeatureKindLabel is the reserved label key for credential feature discrimination.
const FeatureKindLabel = "feature.aggrega.ai/kind"

// FeatureKindSecret is the label value marking a Feature as a credential Feature.
const FeatureKindSecret = "secret"

// InjectBlock is the agnostic injection routing block from
// aggregate-v1alpha1.schema.json#/$defs/inject_block. Known mechanisms are env,
// file, arg. Custom mechanisms beyond the core three are routed into Additional
// preserving author order; the schema's `additionalProperties` slot accepts any
// mechanism conforming to the inject_delivery shape, and the custom UnmarshalYAML
// / UnmarshalJSON hooks below populate Additional from non-core top-level keys.
//
// Each core mechanism value is one delivery: the array of {in, from?, value?}
// entries the schema's inject_delivery definition describes. The field is typed
// any because the hooks below decode the value generically, leaving each
// consumer to type-switch on what arrived. A mapping of delivery key to value
// is not an alternative an author can write — inject_delivery is an array in
// both the Aggregate and Release schemas, so a mapping-shaped delivery is
// refused by envelope validation. The normalized form is an OrderedMap[string]
// of delivery key to string, which is what IREdge.Inject and the render-time
// InjectPipe hold; being ordered, it carries the order the author wrote into
// the rendered artifacts. Additional values carry the same shape and the same
// downstream normalization.
//
// Duplicate top-level mechanism keys (any combination of env/file/arg/custom) are
// rejected at the unmarshal layer with a wrapped error naming the offending key
// and its source line. The check fires uniformly across all key kinds — yaml.v3's
// parser-layer strictness varies, and encoding/json silently overwrites duplicate
// keys in default decode paths; the hook closes the asymmetry.
//
// The `json:"-" yaml:"-"` tag on Additional keeps standard tag-driven dispatch
// from observing the field; the UnmarshalYAML / UnmarshalJSON / MarshalYAML /
// MarshalJSON methods take responsibility for routing keys into and out of it.
type InjectBlock struct {
	Env        any             `json:"env,omitempty" yaml:"env,omitempty"`
	File       any             `json:"file,omitempty" yaml:"file,omitempty"`
	Arg        any             `json:"arg,omitempty" yaml:"arg,omitempty"`
	Additional OrderedMap[any] `json:"-" yaml:"-"`
}

// UnmarshalYAML decodes a mapping node into the InjectBlock, dispatching env /
// file / arg into the typed fields and routing every other top-level key into
// Additional preserving author order. Duplicate top-level keys at any kind
// (core or custom) return a wrapped error.
func (b *InjectBlock) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("InjectBlock: expected mapping node, got kind %d at line %d", node.Kind, node.Line)
	}
	seen := make(map[string]struct{}, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		var key string
		if err := keyNode.Decode(&key); err != nil {
			return fmt.Errorf("InjectBlock: decoding key at line %d: %w", keyNode.Line, err)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("InjectBlock: duplicate mechanism key %q at line %d", key, keyNode.Line)
		}
		seen[key] = struct{}{}
		switch key {
		case "env":
			if err := valNode.Decode(&b.Env); err != nil {
				return fmt.Errorf("InjectBlock.env at line %d: %w", valNode.Line, err)
			}
		case "file":
			if err := valNode.Decode(&b.File); err != nil {
				return fmt.Errorf("InjectBlock.file at line %d: %w", valNode.Line, err)
			}
		case "arg":
			if err := valNode.Decode(&b.Arg); err != nil {
				return fmt.Errorf("InjectBlock.arg at line %d: %w", valNode.Line, err)
			}
		default:
			var v any
			if err := valNode.Decode(&v); err != nil {
				return fmt.Errorf("InjectBlock.%s at line %d: %w", key, valNode.Line, err)
			}
			b.Additional.Set(key, v)
		}
	}
	return nil
}

// UnmarshalJSON decodes a JSON object into the InjectBlock with the same
// dispatch as UnmarshalYAML. Uses a streaming json.Decoder with Token() to
// observe keys in source order — encoding/json's default object decode collapses
// order, so we parse the object manually one key-value pair at a time.
func (b *InjectBlock) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("InjectBlock: reading JSON opening token: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("InjectBlock: expected JSON object, got token %v", tok)
	}
	seen := make(map[string]struct{})
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("InjectBlock: reading JSON key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("InjectBlock: expected string key, got token %v", keyTok)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("InjectBlock: duplicate mechanism key %q", key)
		}
		seen[key] = struct{}{}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("InjectBlock: reading JSON value for key %q: %w", key, err)
		}
		switch key {
		case "env":
			if err := json.Unmarshal(raw, &b.Env); err != nil {
				return fmt.Errorf("InjectBlock.env: %w", err)
			}
		case "file":
			if err := json.Unmarshal(raw, &b.File); err != nil {
				return fmt.Errorf("InjectBlock.file: %w", err)
			}
		case "arg":
			if err := json.Unmarshal(raw, &b.Arg); err != nil {
				return fmt.Errorf("InjectBlock.arg: %w", err)
			}
		default:
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("InjectBlock.%s: %w", key, err)
			}
			b.Additional.Set(key, v)
		}
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return fmt.Errorf("InjectBlock: reading JSON closing token: %w", err)
	}
	return nil
}

// MarshalYAML emits a mapping node with env / file / arg first (in struct-field
// order, matching the existing serialised emission) followed by Additional
// entries in author order. Value receiver matches OrderedMap.MarshalYAML's
// dispatch rationale — yaml.v3's reflection sees value-receiver methods on the
// type's method set regardless of addressability.
func (b InjectBlock) MarshalYAML() (any, error) {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addPair := func(k string, v any) error {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
		valNode := &yaml.Node{}
		if err := valNode.Encode(v); err != nil {
			return fmt.Errorf("InjectBlock: encoding value for key %q: %w", k, err)
		}
		out.Content = append(out.Content, keyNode, valNode)
		return nil
	}
	if b.Env != nil {
		if err := addPair("env", b.Env); err != nil {
			return nil, err
		}
	}
	if b.File != nil {
		if err := addPair("file", b.File); err != nil {
			return nil, err
		}
	}
	if b.Arg != nil {
		if err := addPair("arg", b.Arg); err != nil {
			return nil, err
		}
	}
	for k, v := range b.Additional.All() {
		if err := addPair(k, v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MarshalJSON emits a JSON object preserving the same emission order as
// MarshalYAML — env / file / arg first if non-nil, then Additional entries in
// author order. Builds the object manually because encoding/json does not expose
// a way to influence its default map-key ordering.
func (b InjectBlock) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	addPair := func(k string, v any) error {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		keyBytes, err := json.Marshal(k)
		if err != nil {
			return fmt.Errorf("InjectBlock: encoding JSON key %q: %w", k, err)
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		valBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("InjectBlock: encoding JSON value for key %q: %w", k, err)
		}
		buf.Write(valBytes)
		return nil
	}
	if b.Env != nil {
		if err := addPair("env", b.Env); err != nil {
			return nil, err
		}
	}
	if b.File != nil {
		if err := addPair("file", b.File); err != nil {
			return nil, err
		}
	}
	if b.Arg != nil {
		if err := addPair("arg", b.Arg); err != nil {
			return nil, err
		}
	}
	for k, v := range b.Additional.All() {
		if err := addPair(k, v); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// DecodeVerboseInjectEntry extracts (in, value, from) from a single schema-
// verbose-form inject entry. The entry is one element of the schema's
// inject_delivery array — shaped as {in: K, from?: P, value?: V} per the
// aggregate-v1alpha1 schema. The inner shape can decode as either
// map[string]any (default YAML/JSON unmarshal) or *OrderedMap[any]
// (round-trip-fidelity decode path); the helper handles both uniformly
// so consumer-site decode arms don't asymmetrically skip valid entries
// that happen to arrive via the ordered path.
//
// Schema validation enforces required "in" plus exactly one of "from" or
// "value" per entry; ok=true only when "in" is present, non-empty, and
// string-typed. The hasValue and hasFrom flags let callers pick a
// preference at decode time (sites that propagate both fields into a
// downstream entry struct can ignore the preference; sites that collapse
// into a single string can prefer one). Non-string in/value/from fields,
// or absent entries, return zero-valued slots; ok=false.
func DecodeVerboseInjectEntry(entry any) (in, value, from string, hasValue, hasFrom, ok bool) {
	var get func(key string) (any, bool)
	switch e := entry.(type) {
	case map[string]any:
		get = func(key string) (any, bool) { v, ok := e[key]; return v, ok }
	case *OrderedMap[any]:
		if e == nil {
			return
		}
		get = e.Get
	default:
		return
	}

	inRaw, hasIn := get("in")
	if !hasIn {
		return
	}
	inStr, isStr := inRaw.(string)
	if !isStr || inStr == "" {
		return
	}
	in = inStr

	if vRaw, hv := get("value"); hv {
		if s, isStr := vRaw.(string); isStr {
			value = s
			hasValue = true
		}
	}
	if fRaw, hf := get("from"); hf {
		if s, isStr := fRaw.(string); isStr {
			from = s
			hasFrom = true
		}
	}
	ok = true
	return
}

// FeatureInstance is one Feature attachment, keyed by the instance name the
// author gave it. Used in Aggregate component.features, subsystem.features, and
// Release boundary features.
//
// This is the one attachment shape, and there is no second one. A credential
// Feature — one carrying the feature.aggrega.ai/kind: secret label — is attached
// here like any other, with the same fields, on the same surface, at any scope
// that accepts an attachment. There is no placement partition between the
// attachment blocks: no block is reserved for credentials and none excludes them.
//
// The label itself steers nothing. It is duck-typing metadata on the catalog
// Feature, carried through uninterpreted; the compiler reads it on no branch, so
// a labelled attachment is resolved, merged and rendered by exactly the paths an
// unlabelled one is. What a given attachment produces is decided by the
// FeatureRecipe the Manifest maps its type to — the operator's explicit
// deployment choice — never by the label. The label's audience is the author and
// the catalog tooling that surfaces it.
//
// For a credential, Values holds pointers into a secret store rather than
// resolved material.
//
// Uses is the topology the attached Feature draws, and its connections carry no
// attachments of their own — see [FeatureUsesSocketValue].
type FeatureInstance struct {
	Type        string                             `json:"type" yaml:"type"`
	Description string                             `json:"description,omitempty" yaml:"description,omitempty"`
	Values      OrderedMap[any]                    `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Uses        OrderedMap[FeatureUsesSocketValue] `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"`
	Inject      *InjectBlock                       `json:"inject,omitempty" yaml:"inject,omitempty"`
}

// PipelineStep is one Wasm plugin invocation in a Recipe or FeatureRecipe pipeline.
// Defined in recipe-v1alpha1.schema.json#/properties/spec/properties/pipeline/items.
type PipelineStep struct {
	ID string `json:"id" yaml:"id"`
	// Plugin is the URI of the Plugin entity this step runs, always scoped by the
	// plugin's own socket type: `<pluginType>/<name>`, optionally versioned, as in
	// `parser/yaml` or `parser/yaml:1.0.0`. The scope is mandatory rather than one
	// form among several, because two sockets may hold a plugin of the same name
	// and the scope is what makes the entity resolvable. The build resolves the
	// entity to its .wasm artifact (derived from the entity's bare identity) and
	// loads the bytes. A bare .wasm or artifact reference is not accepted.
	Plugin   string `json:"plugin" yaml:"plugin"`
	Function string `json:"function" yaml:"function"`
	With     string `json:"with,omitempty" yaml:"with,omitempty"`
	// Optional downgrades a failure of this step from a build-halting
	// error to a loud warning: the pre-step document set is restored, the
	// step's output is discarded, and the pipeline continues. When false,
	// any step failure halts after restoring the pre-step set.
	Optional bool `json:"optional,omitempty" yaml:"optional,omitempty"`
	// When is an inline Go template the host evaluates before running this
	// step. When it renders a falsy value the step is skipped as a
	// pass-through: the document set flows to the next step unchanged and the
	// step contributes no patches, side effects, or preceding-step record. An
	// empty or absent When always runs the step. Truthiness follows the shared
	// convention for boolean-like template output (see the truthy package).
	// When and Optional are orthogonal: a step may declare both.
	When string `json:"when,omitempty" yaml:"when,omitempty"`
}

// OmitEntry is one entry of a Recipe's or FeatureRecipe's omits list: something
// the entity's source renders that is to be left out of the output — a document a
// chart renders, or a service, volume or config a compose file declares — named by
// its kind, its name and, where the entry gives one, its namespace.
// Defined in recipe-v1alpha1.schema.json#/properties/spec/properties/omits/items,
// and identically in the FeatureRecipe schema.
//
// Every field is a template, evaluated against the context the entity's values
// mapping is evaluated against. When is evaluated before the others: a falsy When
// drops the entry, so its other fields are never evaluated, and an empty When keeps
// it. What the resolved Kind, Name and Namespace select is the rendering
// provider's own contract; the compiler hands the resolved strings over and reads
// back how many units each entry left out.
type OmitEntry struct {
	Kind      string `json:"kind" yaml:"kind"`
	Name      string `json:"name" yaml:"name"`
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	When      string `json:"when,omitempty" yaml:"when,omitempty"`
}
