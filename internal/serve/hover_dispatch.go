// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/serve/hover"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// handleHover returns the LSP MarkupContent for the cursor position
// at uri. Returns a 1001 error envelope when the source file is not
// parse-cached; nil Contents (no envelope) when the cursor doesn't
// classify as a hover-eligible reference shape; otherwise the
// rendered markdown for the resolved target entity.
//
// Dispatch covers the hover-eligible reference kinds — metadata.name
// self-hover; extends, component-type, feature-type, recipe-implements,
// and manifest-recipe URI references; the two Release path lookups; and
// uses-target, which renders the referenced sibling Component instance.
// SchemaProperty and unclassified positions return null Contents.
func (s *Server) handleHover(params hoverParams) (*MarkupContent, *jsonRPCError) {
	entry, ok := s.parseCache.Get(params.URI)
	if !ok {
		return nil, &jsonRPCError{
			Code:    errFileNotValidated,
			Message: fmt.Sprintf("file not validated: %s", params.URI),
		}
	}
	idx := yamlpos.BuildLineIndexWithEncoding(entry.content, s.positionEncoding())
	ctx, err := yamlpos.Classify(entry.node, idx, params.Position)
	if err != nil {
		return nil, nil
	}

	// UsesTarget — intra-document sibling Component instance.
	if ctx.Kind == yamlpos.ContextKindUsesTarget {
		return s.hoverUsesTarget(entry.node, ctx.Value), nil
	}

	// Release-path lookups — workspace-relative filesystem path + kind validation.
	if ctx.Kind == yamlpos.ContextKindReleaseAggregatePath {
		return s.hoverReleasePath(ctx.Value, "Aggregate"), nil
	}
	if ctx.Kind == yamlpos.ContextKindReleaseManifestPath {
		return s.hoverReleasePath(ctx.Value, "Manifest"), nil
	}

	// URI-based references (parse-cache + workspace walk; version-stripped).
	switch ctx.Kind {
	case yamlpos.ContextKindMetadataName:
		// Self-hover — render the current file's own entity.
		return s.renderEntityFromCache(entry.node, entry.content), nil
	case yamlpos.ContextKindExtendsValue,
		yamlpos.ContextKindComponentsType,
		yamlpos.ContextKindFeaturesType,
		yamlpos.ContextKindRecipeImplements,
		yamlpos.ContextKindManifestRecipeReference:
		if ctx.Value == "" {
			return nil, nil
		}
		name, _ := splitVersionedURI(ctx.Value)
		return s.hoverByURI(name), nil
	}

	// All other ContextKinds (SchemaProperty, Unknown, etc.) — null Contents.
	return nil, nil
}

// hoverByURI locates the target entity in parse cache or workspace
// walk, reads its kind, and dispatches to the corresponding
// per-kind renderer. Returns nil when no target file matches.
func (s *Server) hoverByURI(targetURI string) *MarkupContent {
	doc, content := s.findEntityForHover(targetURI)
	if doc == nil {
		return nil
	}
	return s.renderEntityFromCache(doc, content)
}

// findEntityForHover scans the parse cache + workspace walk for a
// YAML file whose metadata.name matches targetURI. Returns the
// parsed root + raw bytes on hit; (nil, nil) on miss.
func (s *Server) findEntityForHover(targetURI string) (*yaml.Node, []byte) {
	// Parse-cache pass first (cheap, no filesystem).
	var hitNode *yaml.Node
	var hitContent []byte
	s.parseCache.Range(func(_ string, node *yaml.Node, content []byte) bool {
		name, _ := metadataNameOf(node)
		if name == targetURI {
			hitNode, hitContent = node, content
			return false
		}
		return true
	})
	if hitNode != nil {
		return hitNode, hitContent
	}
	// Workspace-walk fallback. Hover is a fast path; cancellation is
	// honored at the dispatcher entry check rather than mid-walk, so a
	// background context suffices here.
	var foundDoc *yaml.Node
	var foundContent []byte
	_ = s.walkWorkspaceYAML(context.Background(), func(path string) error {
		if foundDoc != nil {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var doc yaml.Node
		if uerr := yaml.Unmarshal(content, &doc); uerr != nil {
			return nil
		}
		name, _ := metadataNameOf(&doc)
		if name == targetURI {
			foundDoc = &doc
			foundContent = content
		}
		return nil
	})
	return foundDoc, foundContent
}

// renderEntityFromCache reads the entity's kind: field and dispatches
// to the corresponding per-kind hover renderer.
func (s *Server) renderEntityFromCache(doc *yaml.Node, content []byte) *MarkupContent {
	kind := scalarValueAtPath(doc, []string{"kind"})
	rendered := s.renderByKind(kind, hover.Entity{Doc: doc, Content: content})
	if rendered == "" {
		return nil
	}
	return &MarkupContent{Kind: "markdown", Value: rendered}
}

// renderByKind dispatches to the per-kind renderer based on the
// supplied kind string. Returns empty string when the kind is not in
// the registered renderer set.
func (s *Server) renderByKind(kind string, e hover.Entity) string {
	switch kind {
	case "Component":
		return hover.Component(e)
	case "Feature":
		return hover.Feature(e)
	case "Recipe":
		return hover.Recipe(e)
	case "FeatureRecipe":
		return hover.FeatureRecipe(e)
	case "Provider":
		return hover.Provider(e)
	case "SchemaLibrary":
		return hover.SchemaLibrary(e)
	case "MappingLibrary":
		return hover.MappingLibrary(e)
	case "Aggregate":
		return hover.Aggregate(e)
	case "Manifest":
		return hover.Manifest(e)
	}
	return ""
}

// hoverUsesTarget resolves a uses.target sibling name to the
// ComponentInstance node in the same Aggregate document and renders
// it via the ComponentInstance renderer.
func (s *Server) hoverUsesTarget(sourceDoc *yaml.Node, targetName string) *MarkupContent {
	root := documentRoot(sourceDoc)
	spec := mappingChild(root, "spec")
	if spec == nil {
		return nil
	}
	keyNode, valueNode := findComponentKeyAndValue(spec, targetName)
	if keyNode == nil || valueNode == nil {
		return nil
	}
	rendered := hover.RenderComponentInstance(hover.ComponentInstance{
		Key:      keyNode.Value,
		Instance: valueNode,
	})
	if rendered == "" {
		return nil
	}
	return &MarkupContent{Kind: "markdown", Value: rendered}
}

// findComponentKeyAndValue walks spec recursively (top-level
// components + nested subsystems' components) looking for an entry
// whose key matches targetName. Returns the matched key node and
// value node together.
func findComponentKeyAndValue(specMap *yaml.Node, targetName string) (*yaml.Node, *yaml.Node) {
	if specMap == nil || specMap.Kind != yaml.MappingNode {
		return nil, nil
	}
	if components := mappingChild(specMap, "components"); components != nil && components.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(components.Content); i += 2 {
			if components.Content[i].Value == targetName {
				return components.Content[i], components.Content[i+1]
			}
		}
	}
	if subsystems := mappingChild(specMap, "subsystems"); subsystems != nil && subsystems.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(subsystems.Content); i += 2 {
			if k, v := findComponentKeyAndValue(subsystems.Content[i+1], targetName); k != nil {
				return k, v
			}
		}
	}
	return nil, nil
}

// hoverReleasePath resolves a workspace-relative path to a YAML file,
// validates the file's kind against the expected kind, and renders
// the actual-kind content. On kind mismatch, prepends a warning
// header and still renders the actual-kind body — surfacing operator
// typos at hover-preview time without hiding the file's real content.
// On unrendered actual kind (a kind not in the renderer set), renders
// the warning header alone.
func (s *Server) hoverReleasePath(relativePath, expectedKind string) *MarkupContent {
	full := filepath.Join(s.workspaceRoot, relativePath+".yaml")
	content, err := os.ReadFile(full)
	if err != nil {
		return nil
	}
	var doc yaml.Node
	if uerr := yaml.Unmarshal(content, &doc); uerr != nil {
		return nil
	}
	actualKind := scalarValueAtPath(&doc, []string{"kind"})
	if actualKind == expectedKind {
		body := s.renderByKind(actualKind, hover.Entity{Doc: &doc, Content: content})
		if body == "" {
			return nil
		}
		return &MarkupContent{Kind: "markdown", Value: body}
	}
	// Mismatch: prepend warning header + try to render the actual-kind body.
	expectedLabel := expectedKind
	if expectedKind == "Aggregate" {
		expectedLabel = "spec.aggregate:"
	} else if expectedKind == "Manifest" {
		expectedLabel = "spec.manifest:"
	}
	warning := fmt.Sprintf("**Kind mismatch**: `%s` expected `kind: %s`; found `kind: %s` at `%s`.",
		expectedLabel, expectedKind, actualKind, full)
	fmt.Fprintf(s.stderr, "  hover: kind mismatch at %s — expected %s, found %s\n", full, expectedKind, actualKind)
	body := s.renderByKind(actualKind, hover.Entity{Doc: &doc, Content: content})
	if body == "" {
		// Unrendered actual kind — warning header alone.
		return &MarkupContent{Kind: "markdown", Value: warning}
	}
	return &MarkupContent{Kind: "markdown", Value: warning + "\n\n---\n\n" + body}
}
