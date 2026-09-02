// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/serve/completion"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// handleCompletion returns the candidate list for the cursor at uri.
// Returns a 1001 error envelope when the source file is not parse-
// cached; otherwise returns a CompletionItem slice (possibly empty).
//
// Dispatch flow:
//
//  1. Classify the cursor via yamlpos.Classify (navigation context).
//  2. If the navigation Kind is non-Unknown, route to a parallel
//     completion handler (extends-value cursor → workspace URIs of
//     the host file's kind).
//  3. If the navigation Kind is Unknown, invoke completion's
//     secondary classifier (completion-only contexts: kind:,
//     apiVersion:, label-values, target:, schema-keywords).
//  4. Unrecognized cursor → empty candidate list (LSP convention for
//     "no completion available here").
func (s *Server) handleCompletion(params completionParams) ([]CompletionItem, *jsonRPCError) {
	entry, ok := s.parseCache.Get(s.canonicalFile(params.URI))
	if !ok {
		return nil, &jsonRPCError{
			Code:    errFileNotValidated,
			Message: fmt.Sprintf("file not validated: %s", params.URI),
		}
	}
	idx := yamlpos.BuildLineIndexWithEncoding(entry.content, s.positionEncoding())
	ctx, err := yamlpos.Classify(entry.node, idx, params.Position)
	if err != nil {
		return []CompletionItem{}, nil
	}

	// Determine the trigger-character prefix (for "/" trigger prefix
	// narrowing). The prefix is the portion of the cursor's current
	// scalar that precedes the trigger character — today the
	// daemon implements only the simplest prefix narrowing: when
	// triggerCharacter is "/", filter candidates whose label starts
	// with the cursor's scalar value (which would be the partial
	// "core/" the operator already typed).
	prefix := ""
	if params.Context != nil && params.Context.TriggerCharacter == "/" {
		prefix = ctx.Value
	}

	// Read the host file's kind: field for kind-aware dispatch
	// (extends, implements, provider).
	hostKind := scalarAtPathOnDoc(entry.node, []string{"kind"})

	// Branch 1: navigation-shape cursor (yamlpos recognized it).
	if items, handled := s.completionForNavigationContext(ctx, hostKind, entry.node, prefix); handled {
		return adaptItems(items), nil
	}

	// Branch 2: completion-only context via the secondary classifier.
	compKind := completion.ClassifyForCompletion(ctx.Path, ctx.Side)
	items := s.completionForCompletionContext(compKind, ctx, prefix)
	return adaptItems(items), nil
}

// completionForNavigationContext handles cursors whose yamlpos Kind
// is non-Unknown. Returns (items, true) when the Kind has a
// registered completion handler; (_, false) when the Kind has no
// completion equivalent (MetadataName, SchemaProperty — no candidates
// at those positions). sourceDoc is the cursor's source file used by
// the intra-document UsesTarget branch.
func (s *Server) completionForNavigationContext(ctx yamlpos.Context, hostKind string, sourceDoc *yaml.Node, prefix string) ([]completion.CompletionItem, bool) {
	switch ctx.Kind {
	case yamlpos.ContextKindExtendsValue:
		uris := s.enumerateWorkspaceURIs(hostKind)
		return completion.Extends(uris, prefix), true
	case yamlpos.ContextKindComponentsType:
		uris := s.enumerateWorkspaceURIs("Component")
		return completion.ComponentsType(uris, prefix), true
	case yamlpos.ContextKindFeaturesType:
		uris := s.enumerateWorkspaceURIs("Feature")
		return completion.FeaturesType(uris, prefix), true
	case yamlpos.ContextKindRecipeImplements:
		targetKind := "Component"
		if hostKind == "FeatureRecipe" {
			targetKind = "Feature"
		}
		uris := s.enumerateWorkspaceURIs(targetKind)
		return completion.Implements(uris, prefix), true
	case yamlpos.ContextKindManifestRecipeReference:
		// Implements-aware ranking: a Manifest mapping value binds a component
		// (or feature) TYPE to a recipe URI. Recipes whose spec.implements is
		// able to implement that type rank first — a suggestion, never a
		// constraint (the author may map any recipe). The type is the mapping
		// key: the last path segment of a `...type.<TYPE>` cursor; a `features`
		// segment selects FeatureRecipes. A `name` mapping carries an instance
		// name (not a type), so ranking is skipped (mappingType stays empty).
		recipeKind := "Recipe"
		isTypeMapping := false
		for _, seg := range ctx.Path {
			switch seg {
			case "features":
				recipeKind = "FeatureRecipe"
			case "type":
				isTypeMapping = true
			}
		}
		var mappingType string
		if isTypeMapping && len(ctx.Path) > 0 {
			mappingType = ctx.Path[len(ctx.Path)-1]
		}
		uris := s.enumerateWorkspaceURIs(recipeKind)
		return completion.ManifestRecipeReference(uris, prefix, mappingType, s.enumerateRecipeImplements(recipeKind)), true
	case yamlpos.ContextKindReleaseAggregatePath:
		paths := s.enumerateWorkspaceRelativePaths("Aggregate")
		return completion.ReleaseAggregatePath(paths, prefix), true
	case yamlpos.ContextKindReleaseManifestPath:
		paths := s.enumerateWorkspaceRelativePaths("Manifest")
		return completion.ReleaseManifestPath(paths, prefix), true
	case yamlpos.ContextKindUsesTarget:
		names := completion.EnumerateLocalComponentKeys(sourceDoc)
		return completion.UsesTarget(names, prefix), true
	case yamlpos.ContextKindMetadataName,
		yamlpos.ContextKindSchemaProperty,
		yamlpos.ContextKindUnknown:
		// No completion at these positions for navigation Kind —
		// fall through to the completion-only classifier (Unknown
		// branch returns empty; others may classify into completion-
		// only contexts the same cursor lands at — e.g., the same
		// cursor can be SchemaProperty for navigation AND a
		// schema-keyword context for completion).
		return nil, false
	}
	return nil, false
}

// completionForCompletionContext handles cursors classified by the
// completion-only classifier. Returns an empty slice when the
// completion Kind is Unknown.
func (s *Server) completionForCompletionContext(kind completion.ContextKind, ctx yamlpos.Context, prefix string) []completion.CompletionItem {
	switch kind {
	case completion.ContextKindKind:
		return completion.Kind()
	case completion.ContextKindAPIVersion:
		return completion.APIVersion()
	case completion.ContextKindLabelValue:
		if len(ctx.Path) < 3 {
			return nil
		}
		labelName := ctx.Path[2]
		return completion.LabelValue(labelName)
	case completion.ContextKindSchemaKeyword:
		return completion.SchemaKeyword()
	case completion.ContextKindSchemaTypeValue:
		return completion.SchemaTypeValue()
	}
	return nil
}

// enumerateWorkspaceURIs projects the workspace document traversal down
// to the metadata.name values of documents whose kind: matches the
// filter. Returns them deduped and sorted. Empty filter matches every
// kind (rare).
//
// Completion is a fast path; cancellation is honored at the dispatcher
// entry check rather than mid-traversal, so a background context
// suffices here.
func (s *Server) enumerateWorkspaceURIs(kindFilter string) []string {
	seen := map[string]bool{}
	s.rangeWorkspaceDocs(context.Background(), func(doc workspaceDoc) bool {
		if kindFilter != "" && scalarAtPathOnDoc(doc.node, []string{"kind"}) != kindFilter {
			return true
		}
		name, _ := metadataNameOf(doc.node)
		if name != "" {
			seen[name] = true
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sortStringSlice(out)
	return out
}

// enumerateRecipeImplements projects the workspace document traversal
// down to a map from each Recipe/FeatureRecipe's metadata.name to its
// spec.implements value, for implements-aware Manifest recipe-reference
// completion ranking. Its keys match enumerateWorkspaceURIs's output
// exactly because both read the same traversal; first document to
// declare a name wins, which the traversal's per-path ordering makes
// deterministic.
func (s *Server) enumerateRecipeImplements(kindFilter string) map[string]string {
	out := map[string]string{}
	s.rangeWorkspaceDocs(context.Background(), func(doc workspaceDoc) bool {
		if scalarAtPathOnDoc(doc.node, []string{"kind"}) != kindFilter {
			return true
		}
		name, _ := metadataNameOf(doc.node)
		if name == "" {
			return true
		}
		if _, ok := out[name]; ok {
			return true
		}
		out[name] = scalarAtPathOnDoc(doc.node, []string{"spec", "implements"})
		return true
	})
	return out
}

// enumerateWorkspaceRelativePaths walks the workspace for files
// whose kind: matches the filter. Returns deduped sorted workspace-
// relative paths (extension-stripped — e.g., "app" for app.yaml).
// Used by Release-path completion handlers (spec.aggregate:,
// spec.manifest:).
func (s *Server) enumerateWorkspaceRelativePaths(kindFilter string) []string {
	seen := map[string]bool{}
	// Background context: completion is a fast path; cancellation is
	// honored at the dispatcher entry check rather than mid-walk.
	_ = s.walkWorkspaceYAML(context.Background(), func(path string) error {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var doc yaml.Node
		if uerr := yaml.Unmarshal(content, &doc); uerr != nil {
			return nil
		}
		if kindFilter != "" && scalarAtPathOnDoc(&doc, []string{"kind"}) != kindFilter {
			return nil
		}
		rel, err := filepath.Rel(s.workspaceRoot, path)
		if err != nil {
			return nil
		}
		rel = strings.TrimSuffix(rel, ".yaml")
		rel = strings.TrimSuffix(rel, ".yml")
		seen[rel] = true
		return nil
	})
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sortStringSlice(out)
	return out
}

// scalarAtPathOnDoc walks doc following the supplied mapping keys and
// returns the scalar text. Mirrors completion/common.go's helper —
// duplicated here to keep the package boundary clean (no
// cross-package leak of internal helpers).
func scalarAtPathOnDoc(doc *yaml.Node, path []string) string {
	n := nodeAtPathOnDoc(doc, path)
	if n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

// nodeAtPathOnDoc walks doc following the supplied mapping keys and
// returns the node the last key addresses, or nil when any key along
// the way is absent or its parent is not a mapping. It is the single
// home for the key-descent itself, so a reader that wants a scalar and
// a reader that wants a mapping descend identically.
func nodeAtPathOnDoc(doc *yaml.Node, path []string) *yaml.Node {
	if doc == nil {
		return nil
	}
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, key := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

// sortStringSlice sorts s ascending in place. Avoids importing sort.
func sortStringSlice(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// adaptItems converts the completion package's CompletionItem shape
// to internal/serve's wire shape (defined in handlers.go).
func adaptItems(items []completion.CompletionItem) []CompletionItem {
	if items == nil {
		return []CompletionItem{}
	}
	out := make([]CompletionItem, len(items))
	for i, it := range items {
		out[i] = CompletionItem{
			Label:         it.Label,
			Kind:          it.Kind,
			Detail:        it.Detail,
			Documentation: it.Documentation,
			SortText:      it.SortText,
		}
	}
	return out
}
