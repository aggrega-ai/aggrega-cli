// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

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
//     apiVersion:, label-values, provider:, schema-keywords).
//  4. Unrecognized cursor → empty candidate list (LSP convention for
//     "no completion available here").
func (s *Server) handleCompletion(params completionParams) ([]CompletionItem, *jsonRPCError) {
	entry, ok := s.parseCache.Get(params.URI)
	if !ok {
		return nil, &jsonRPCError{
			Code:    errFileNotValidated,
			Message: fmt.Sprintf("file not validated: %s", params.URI),
		}
	}
	idx := yamlpos.BuildLineIndexWithEncoding(entry.content, s.positionEncoding)
	ctx, err := yamlpos.Classify(entry.node, idx, params.Position)
	if err != nil {
		return []CompletionItem{}, nil
	}

	// Determine the trigger-character prefix (for "/" trigger prefix
	// narrowing). The prefix is the portion of the cursor's current
	// scalar that precedes the trigger character — at this WI, the
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
		uris := s.enumerateWorkspaceURIs("Recipe")
		return completion.ManifestRecipeReference(uris, prefix), true
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
	case completion.ContextKindProvider:
		uris := s.enumerateWorkspaceURIs("Provider")
		return completion.Provider(uris, prefix)
	case completion.ContextKindSchemaKeyword:
		return completion.SchemaKeyword()
	}
	return nil
}

// enumerateWorkspaceURIs walks parse-cache + workspace for files
// whose kind: matches the filter. Returns deduped sorted metadata.name
// values. Empty filter matches every kind (rare).
func (s *Server) enumerateWorkspaceURIs(kindFilter string) []string {
	seen := map[string]bool{}
	// Parse-cache pass first.
	for _, entry := range s.parseCache.entries {
		if kindFilter != "" && scalarAtPathOnDoc(entry.node, []string{"kind"}) != kindFilter {
			continue
		}
		name, _ := metadataNameOf(entry.node)
		if name != "" {
			seen[name] = true
		}
	}
	// Workspace-walk fallback.
	_ = s.walkWorkspaceYAML(func(path string) error {
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
		name, _ := metadataNameOf(&doc)
		if name != "" {
			seen[name] = true
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sortStringSlice(out)
	return out
}

// enumerateWorkspaceRelativePaths walks the workspace for files
// whose kind: matches the filter. Returns deduped sorted workspace-
// relative paths (extension-stripped — e.g., "app" for app.yaml).
// Used by Release-path completion handlers (spec.aggregate:,
// spec.manifest:).
func (s *Server) enumerateWorkspaceRelativePaths(kindFilter string) []string {
	seen := map[string]bool{}
	_ = s.walkWorkspaceYAML(func(path string) error {
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
	if doc == nil {
		return ""
	}
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, key := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return ""
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
				break
			}
		}
		if next == nil {
			return ""
		}
		n = next
	}
	if n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
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
