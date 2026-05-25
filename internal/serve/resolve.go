// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// resolveDefinitionSite returns the Location of the entity referenced
// at the cursor in uri. The cursor is classified via yamlpos.Classify;
// the resolved target is the value of the scalar at the cursor, which
// IS the referenced entity URI (or, for ContextKindMetadataName, the
// entity's own URI — a self-reference returning the cursor's own
// position).
//
// The source file must be in the parse cache (validated prior).
// Returns the file-not-validated error envelope when not. Returns
// a nil Location (no envelope) when the cursor's classification does
// not target a referenceable URI, when the target URI does not appear
// in any workspace YAML's metadata.name, or when the workspace walk
// has nothing to scan.
func (s *Server) resolveDefinitionSite(uri string, pos yamlpos.Position) (*yamlpos.Location, *jsonRPCError) {
	entry, ok := s.parseCache.Get(uri)
	if !ok {
		return nil, &jsonRPCError{
			Code:    errFileNotValidated,
			Message: fmt.Sprintf("file not validated: %s", uri),
		}
	}
	idx := yamlpos.BuildLineIndex(entry.content)
	ctx, err := yamlpos.Classify(entry.node, idx, pos)
	if err != nil {
		return nil, nil
	}
	switch ctx.Kind {
	case yamlpos.ContextKindMetadataName:
		// Cursor sits on the entity's own metadata.name — return the
		// same position. Matches LSP's go-to-definition-on-the-
		// definition convention.
		name, nameNode := metadataNameOf(entry.node)
		if nameNode == nil || name == "" {
			return nil, nil
		}
		loc := nodeToLocation(uri, idx, nameNode)
		return &loc, nil
	case yamlpos.ContextKindExtendsValue,
		yamlpos.ContextKindComponentsType,
		yamlpos.ContextKindFeaturesType,
		yamlpos.ContextKindRecipeImplements:
		if ctx.Value == "" {
			return nil, nil
		}
		return s.findDefinitionByURI(ctx.Value)
	default:
		return nil, nil
	}
}

// findDefinitionByURI locates the YAML file in the workspace whose
// metadata.name == targetURI. Scans the parse cache first for a hit
// (other in-flight files in the editor's session), then falls through
// to a workspace walk. First match wins (workspaces with duplicate
// names are user error and not enforced here).
func (s *Server) findDefinitionByURI(targetURI string) (*yamlpos.Location, *jsonRPCError) {
	if loc := s.scanCachedForName(targetURI); loc != nil {
		return loc, nil
	}
	var found *yamlpos.Location
	err := s.walkWorkspaceYAML(func(path string) error {
		if found != nil {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var doc yaml.Node
		if unErr := yaml.Unmarshal(content, &doc); unErr != nil {
			fmt.Fprintf(s.stderr, "  workspace-walk parse: %s: %v\n", path, unErr)
			return nil
		}
		name, nameNode := metadataNameOf(&doc)
		if name != targetURI || nameNode == nil {
			return nil
		}
		idx := yamlpos.BuildLineIndex(content)
		loc := nodeToLocation(path, idx, nameNode)
		found = &loc
		return nil
	})
	if err != nil {
		fmt.Fprintf(s.stderr, "  workspace-walk: %v\n", err)
	}
	return found, nil
}

// scanCachedForName scans parse-cache entries for one whose
// metadata.name equals target. Returns the Location of that node's
// metadata.name on hit; nil on miss.
func (s *Server) scanCachedForName(target string) *yamlpos.Location {
	for path, entry := range s.parseCache.entries {
		name, nameNode := metadataNameOf(entry.node)
		if name != target || nameNode == nil {
			continue
		}
		idx := yamlpos.BuildLineIndex(entry.content)
		loc := nodeToLocation(path, idx, nameNode)
		return &loc
	}
	return nil
}

// findReferencesAcrossWorkspace walks the workspace returning every
// position whose value matches the target URI across the supported
// reference shapes. The cursor must address a metadata.name scalar
// for a non-empty result; the target URI is that node's Value.
//
// Returns the file-not-validated error envelope when the source file
// is not in the parse cache. Returns an empty slice (no envelope) when
// the cursor does not address metadata.name or when the target URI is
// empty.
func (s *Server) findReferencesAcrossWorkspace(uri string, pos yamlpos.Position) ([]yamlpos.Location, *jsonRPCError) {
	target, errEnvelope := s.uriAtMetadataName(uri, pos)
	if errEnvelope != nil {
		return nil, errEnvelope
	}
	if target == "" {
		return []yamlpos.Location{}, nil
	}
	var out []yamlpos.Location
	err := s.walkWorkspaceYAML(func(path string) error {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var doc yaml.Node
		if unErr := yaml.Unmarshal(content, &doc); unErr != nil {
			fmt.Fprintf(s.stderr, "  workspace-walk parse: %s: %v\n", path, unErr)
			return nil
		}
		idx := yamlpos.BuildLineIndex(content)
		root := documentRoot(&doc)
		collectReferences(root, idx, path, target, nil, &out)
		return nil
	})
	if err != nil {
		fmt.Fprintf(s.stderr, "  workspace-walk: %v\n", err)
	}
	return out, nil
}

// findImplementationsAcrossWorkspace returns every workspace position
// that implements or extends the target entity. The result combines:
//
//   - implementations: any Recipe / FeatureRecipe whose spec.implements
//     equals the target URI.
//   - children: any entity whose spec.extends equals the target URI.
//
// Both contribute Locations pointing at the implementing / child
// entity's metadata.name. Cursor must address metadata.name for a
// non-empty result.
func (s *Server) findImplementationsAcrossWorkspace(uri string, pos yamlpos.Position) ([]yamlpos.Location, *jsonRPCError) {
	target, errEnvelope := s.uriAtMetadataName(uri, pos)
	if errEnvelope != nil {
		return nil, errEnvelope
	}
	if target == "" {
		return []yamlpos.Location{}, nil
	}
	var out []yamlpos.Location
	err := s.walkWorkspaceYAML(func(path string) error {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var doc yaml.Node
		if unErr := yaml.Unmarshal(content, &doc); unErr != nil {
			fmt.Fprintf(s.stderr, "  workspace-walk parse: %s: %v\n", path, unErr)
			return nil
		}
		root := documentRoot(&doc)
		implementsVal := scalarAtPath(root, []string{"spec", "implements"})
		extendsVal := scalarAtPath(root, []string{"spec", "extends"})
		var hit bool
		if implementsVal != nil && implementsVal.Value == target {
			hit = true
		}
		if extendsVal != nil && extendsVal.Value == target {
			hit = true
		}
		if !hit {
			return nil
		}
		_, nameNode := metadataNameOf(&doc)
		if nameNode == nil {
			return nil
		}
		idx := yamlpos.BuildLineIndex(content)
		out = append(out, nodeToLocation(path, idx, nameNode))
		return nil
	})
	if err != nil {
		fmt.Fprintf(s.stderr, "  workspace-walk: %v\n", err)
	}
	return out, nil
}

// uriAtMetadataName classifies the cursor in uri's parse-cached doc
// and returns the scalar value at the cursor when the classification
// is ContextKindMetadataName. Returns an empty string when the cursor
// is on any other shape. The file-not-validated envelope surfaces
// when uri is not parse-cached.
func (s *Server) uriAtMetadataName(uri string, pos yamlpos.Position) (string, *jsonRPCError) {
	entry, ok := s.parseCache.Get(uri)
	if !ok {
		return "", &jsonRPCError{
			Code:    errFileNotValidated,
			Message: fmt.Sprintf("file not validated: %s", uri),
		}
	}
	idx := yamlpos.BuildLineIndex(entry.content)
	ctx, err := yamlpos.Classify(entry.node, idx, pos)
	if err != nil {
		return "", nil
	}
	if ctx.Kind != yamlpos.ContextKindMetadataName {
		return "", nil
	}
	return ctx.Value, nil
}

// collectReferences walks the YAML subtree rooted at n looking for
// scalar values equal to target at a path classifying as one of the
// recognized reference shapes. Each match contributes a Location.
//
// The walker tracks the breadcrumb path so the leaf scalar's Path
// classifies through yamlpos.classifyPath logic. The caller's URI is
// passed as the file context for the Location.
func collectReferences(n *yaml.Node, idx yamlpos.LineIndex, fileURI, target string, path []string, out *[]yamlpos.Location) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			keyNode := n.Content[i]
			valNode := n.Content[i+1]
			childPath := append(append([]string(nil), path...), keyNode.Value)
			if valNode.Kind == yaml.ScalarNode {
				if valNode.Value == target && isReferenceShape(childPath) {
					*out = append(*out, nodeToLocation(fileURI, idx, valNode))
				}
			} else {
				collectReferences(valNode, idx, fileURI, target, childPath, out)
			}
		}
	case yaml.SequenceNode:
		for i, child := range n.Content {
			childPath := append(append([]string(nil), path...), fmt.Sprintf("%d", i))
			collectReferences(child, idx, fileURI, target, childPath, out)
		}
	}
}

// isReferenceShape reports whether path classifies as one of the
// URI-referencing ContextKinds. Single source of truth for the
// classification rules lives in yamlpos.ClassifyPath; this wrapper
// names the subset of Kinds that resolve / findReferences acts on.
//
// When yamlpos grows a new URI-referencing ContextKind, this switch
// must add the corresponding case.
func isReferenceShape(path []string) bool {
	switch yamlpos.ClassifyPath(path) {
	case yamlpos.ContextKindExtendsValue,
		yamlpos.ContextKindComponentsType,
		yamlpos.ContextKindFeaturesType,
		yamlpos.ContextKindRecipeImplements:
		return true
	}
	return false
}

// documentRoot unwraps a yaml.DocumentNode to its single content
// child. Non-document nodes pass through unchanged. Returns nil when
// the document is empty.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	return doc
}

// metadataNameOf reads the entity's metadata.name scalar from a
// parsed YAML document. Returns the value and the underlying scalar
// node (for position reporting). Returns ("", nil) when the document
// is missing metadata.name.
func metadataNameOf(doc *yaml.Node) (string, *yaml.Node) {
	n := scalarAtPath(documentRoot(doc), []string{"metadata", "name"})
	if n == nil {
		return "", nil
	}
	return n.Value, n
}

// scalarAtPath walks root following the supplied mapping keys and
// returns the scalar node at the end of the path. Returns nil if any
// intermediate key is missing or if the terminal node is not a scalar.
func scalarAtPath(root *yaml.Node, path []string) *yaml.Node {
	n := root
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
	if n != nil && n.Kind == yaml.ScalarNode {
		return n
	}
	return nil
}

// nodeToLocation constructs a yamlpos.Location for n in fileURI,
// using idx for UTF-16 conversion of the start and end positions.
func nodeToLocation(fileURI string, idx yamlpos.LineIndex, n *yaml.Node) yamlpos.Location {
	return yamlpos.Location{
		URI: fileURI,
		Range: yamlpos.Range{
			Start: idx.NodeToPosition(n),
			End:   idx.ScalarEnd(n),
		},
	}
}
