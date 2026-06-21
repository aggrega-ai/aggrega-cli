// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"errors"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// entityKindByName maps an authored aggrega entity kind string to its
// envelope.Kind. Only the ten editor-authored entity kinds appear here;
// AggregaConfig / Lockfile / Workload / IR are deliberately absent — the
// first two carry no apiVersion+kind envelope to classify on (they are
// flat-top-level documents, so this apiVersion+kind mechanism
// structurally cannot reach them — those two are classified by filename
// instead, see classifyByFilename), and Workload/IR are not
// editor-authored standalone documents.
var entityKindByName = map[string]envelope.Kind{
	"Component":      envelope.KindComponent,
	"Feature":        envelope.KindFeature,
	"Recipe":         envelope.KindRecipe,
	"FeatureRecipe":  envelope.KindFeatureRecipe,
	"Provider":       envelope.KindProvider,
	"SchemaLibrary":  envelope.KindSchemaLibrary,
	"MappingLibrary": envelope.KindMappingLibrary,
	"Aggregate":      envelope.KindAggregate,
	"Manifest":       envelope.KindManifest,
	"Release":        envelope.KindRelease,
}

// classifyAggregaDocument peeks apiVersion + kind from the parsed buffer
// node and returns the envelope.Kind for an authored aggrega entity, or
// ok=false for anything else — the non-aggrega guard. A document without
// an aggrega.ai apiVersion, without a known entity kind, or that is not a
// single mapping document classifies as not-ours, so the per-file
// envelope leg never touches foreign YAML (chart templates, compose
// files, Kubernetes manifests, arbitrary YAML).
func classifyAggregaDocument(doc *yaml.Node) (envelope.Kind, bool) {
	if doc == nil {
		return "", false
	}
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return "", false
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return "", false
	}
	var apiVersion, kind string
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]
		if key.Kind != yaml.ScalarNode || val.Kind != yaml.ScalarNode {
			continue
		}
		switch key.Value {
		case "apiVersion":
			apiVersion = val.Value
		case "kind":
			kind = val.Value
		}
	}
	if !strings.HasPrefix(apiVersion, "aggrega.ai/") {
		return "", false
	}
	k, ok := entityKindByName[kind]
	return k, ok
}

// classifyByFilename classifies the two flat-top-level aggrega documents
// that carry no apiVersion+kind envelope and so cannot be reached by
// classifyAggregaDocument: the workspace config and the active Release's
// lockfile. The workspace config is the fixed, unique name
// ".aggrega-config.yaml". The lockfile is matched by correlation to the
// active Release — only the basename that lockfile.PathFor derives from
// activeReleasePath classifies as a lockfile; with no active Release,
// nothing does. The bare ".lock" extension is deliberately NOT used: it
// belongs to many foreign ecosystems (Cargo.lock, flake.lock,
// poetry.lock, yarn.lock, Gemfile.lock, …), none of which is an aggrega
// lockfile, and validating them against the lockfile schema would
// lint-bomb foreign files. Correlation keeps the editor's single-anchor
// model — only the active Release's documents are validated — and never
// touches another ecosystem's lockfile.
func (s *Server) classifyByFilename(file string) (envelope.Kind, bool) {
	base := filepath.Base(file)
	if base == workspaceconfig.ConfigFilename {
		return envelope.KindAggregaConfig, true
	}
	if s.activeReleasePath != "" && base == filepath.Base(lockfile.PathFor(s.activeReleasePath)) {
		return envelope.KindLockfile, true
	}
	return "", false
}

// envelopeDiagnosticsForBuffer runs the document-envelope gate over the
// editor BUFFER content for a classifiable aggrega document and returns
// its findings as per-file diagnostics. Classification is by filename
// first (the workspace config and the active Release's lockfile, which
// carry no apiVersion+kind envelope) and then by the document's
// apiVersion+kind (the ten authored entity kinds). The second return is
// true only when the document classifies AND the envelope rejected it —
// the signal the caller uses to early-return (which short-circuits Tier-4
// for the active Aggregate and is the coverage for every other file).
// Foreign YAML and envelope-clean documents return (nil, false), so the
// caller falls through to its existing flow unchanged.
func (s *Server) envelopeDiagnosticsForBuffer(file string, content []byte, doc *yaml.Node) ([]Diagnostic, bool) {
	kind, ok := s.classifyByFilename(file)
	if !ok {
		kind, ok = classifyAggregaDocument(doc)
	}
	if !ok {
		return nil, false
	}
	_, err := envelope.ValidateBytes(kind, content, "")
	var envErr *envelope.Error
	if !errors.As(err, &envErr) {
		// Clean (err == nil) or a non-envelope engine error — either way
		// this leg produces no findings; fall through.
		return nil, false
	}
	idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding)
	var diags []Diagnostic
	for _, f := range leafmostFindings(envErr.Findings) {
		line, col := 1, 1
		var endLine, endCol int
		if node := walkYAMLToField(doc, "."+f.FieldPath); node != nil {
			line, col, endLine, endCol = diagRangeForNode(idx, node)
		}
		// Carry the same envelope framing the CLI renders ("…document
		// violates its envelope schema") plus the path-pointed finding, so
		// the editor diagnostic reads as one coherent verdict with the CLI.
		diags = append(diags, Diagnostic{
			Line:      line,
			Column:    col,
			EndLine:   endLine,
			EndColumn: endCol,
			Severity:  "error",
			Message:   string(kind) + " document violates its envelope schema: " + f.Message,
		})
	}
	return diags, true
}

// leafmostFindings keeps only findings whose FieldPath is not a strict
// ancestor of another finding's FieldPath. The envelope engine reports a
// single violation at each level of the path to it (e.g. spec →
// spec.boundaries → spec.boundaries.main); keeping the leaf-most yields
// one diagnostic at the deepest walkable key rather than a squiggle at
// every ancestor.
func leafmostFindings(fs []jsonvalidator.Finding) []jsonvalidator.Finding {
	var out []jsonvalidator.Finding
	for i, f := range fs {
		ancestor := false
		for j, g := range fs {
			if i != j && f.FieldPath != g.FieldPath &&
				(f.FieldPath == "" || strings.HasPrefix(g.FieldPath, f.FieldPath+".")) {
				ancestor = true
				break
			}
		}
		if !ancestor {
			out = append(out, f)
		}
	}
	return out
}
