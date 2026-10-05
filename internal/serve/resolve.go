// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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
// not target a referenceable URI, or when neither a workspace document
// nor the catalog lookup in terms declares the target URI.
//
// terms are the terms the request named, or the workspace's own: an entity
// reference is looked up in them as findDefinitionByURI states.
func (s *Server) resolveDefinitionSite(reqCtx context.Context, uri string, pos yamlpos.Position, terms releaseTerms) (*yamlpos.Location, *jsonRPCError) {
	entry, ok := s.parseCache.Get(s.canonicalFile(uri))
	if !ok {
		return nil, &jsonRPCError{
			Code:    errFileNotValidated,
			Message: fmt.Sprintf("file not validated: %s", uri),
		}
	}
	idx := yamlpos.BuildLineIndexWithEncoding(entry.content, s.positionEncoding())
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
		yamlpos.ContextKindRecipeImplements,
		yamlpos.ContextKindManifestRecipeReference:
		if ctx.Value == "" {
			return nil, nil
		}
		return s.findDefinitionByURI(reqCtx, ctx.Value, terms)
	case yamlpos.ContextKindUsesTarget:
		// Intra-document local-component reference. Walk the source
		// document's spec.components (and recursively-nested
		// spec.subsystems) for a sibling whose key matches the cursor
		// value.
		if ctx.Value == "" {
			return nil, nil
		}
		return s.resolveUsesTarget(uri, entry.node, idx, ctx.Value), nil
	case yamlpos.ContextKindReleaseAggregatePath,
		yamlpos.ContextKindReleaseManifestPath:
		// Workspace-relative path reference. Resolve against workspaceRoot,
		// append .yaml, return Location at the target file's metadata.name.
		if ctx.Value == "" {
			return nil, nil
		}
		return s.resolveReleasePath(ctx.Value), nil
	default:
		return nil, nil
	}
}

// findDefinitionByURI locates the document declaring the entity reference
// names, in terms. Two sources answer:
//
//   - The Release's lookup: the reference resolved through terms.lookup
//     (catalogDefinitionFor), the answer being the document that lookup resolves
//     it to.
//   - The workspace documents: the YAML files whose metadata.name is the
//     reference's name, read from BOTH the parse cache and the workspace
//     (deduplicated by path), of which only those terms.reach holds are
//     candidates. More than one unique matching path draws a warning on stderr,
//     and the first match is the answer.
//
// A request naming a Release is answered through that Release's lookup first, so
// go-to-definition opens the document the Release's catalog directories resolve
// the name to, in the order the workspace configuration gives them — the one a
// lookup by name in that Release's terms answers — wherever a workspace document
// elsewhere declares the same name; the workspace documents answer only where the
// lookup resolves nothing. A request naming none reads the workspace documents first, so a
// reference a workspace document answers keeps that answer, and the workspace's
// own lookup only where no workspace document matches: that is what answers for
// an entity only a catalog directory holds, which need not sit inside the
// workspace at all. A withdrawn request reads no catalog, whether it was
// withdrawn before a named Release's lookup or during the walk.
//
// A version suffix on the reference drives a secondary check on the workspace
// documents. They are matched by bare name first (versions are not part of
// metadata.name in any entity schema); when a matched entity's
// metadata.version differs from the reference's, the function emits
// a one-line stderr warning naming the version mismatch but still
// returns the Location (navigation succeeds — the user wanted to see
// the entity definition, version-pinning is a compile-time concern).
// The lookup is handed the reference as written, so it selects the version the
// reference names the way it does for a build.
//
// Cost note: wherever the workspace documents are read, the workspace is
// walked whole, to detect cross-cache+disk duplicate definitions. The
// workspace-walk cost is bounded by workspace size (single-digit-ms for typical
// sizes); the duplicate-detection correctness gain is worth the walk cost for a
// user-driven editor operation.
func (s *Server) findDefinitionByURI(reqCtx context.Context, reference string, terms releaseTerms) (*yamlpos.Location, *jsonRPCError) {
	if terms.release != "" {
		if reqCtx.Err() != nil {
			return nil, nil
		}
		if loc, ok := s.catalogDefinitionFor(terms, reference); ok {
			return loc, nil
		}
	}
	name, expectedVersion, _ := resolver.SplitVersionedURI(reference)
	type match struct {
		path     string
		version  string
		location yamlpos.Location
	}
	var matches []match
	seen := map[string]bool{}

	// Parse-cache pass.
	encoding := s.positionEncoding()
	s.parseCache.Range(func(path string, node *yaml.Node, content []byte) bool {
		entityName, nameNode := metadataNameOf(node)
		if entityName != name || nameNode == nil || !terms.reach.Holds(path) {
			return true
		}
		entityVersion := scalarValueAtPath(node, []string{"metadata", "version"})
		idx := yamlpos.BuildLineIndexWithEncoding(content, encoding)
		matches = append(matches, match{
			path:     path,
			version:  entityVersion,
			location: nodeToLocation(path, idx, nameNode),
		})
		seen[path] = true
		return true
	})

	// Workspace-walk pass. Skip paths already captured by the parse-cache
	// pass (a parse-cached file would be re-read from disk and possibly
	// produce a duplicate; dedup by absolute path).
	err := s.walkWorkspaceYAML(reqCtx, func(path, read string) error {
		if seen[path] || !terms.reach.Holds(path) {
			return nil
		}
		content, readErr := os.ReadFile(read)
		if readErr != nil {
			return nil
		}
		var doc yaml.Node
		if unErr := yaml.Unmarshal(content, &doc); unErr != nil {
			fmt.Fprintf(s.stderr, "  workspace-walk parse: %s: %v\n", path, unErr)
			return nil
		}
		entityName, nameNode := metadataNameOf(&doc)
		if entityName != name || nameNode == nil {
			return nil
		}
		entityVersion := scalarValueAtPath(&doc, []string{"metadata", "version"})
		idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding())
		matches = append(matches, match{
			path:     path,
			version:  entityVersion,
			location: nodeToLocation(path, idx, nameNode),
		})
		seen[path] = true
		return nil
	})
	if err != nil {
		fmt.Fprintf(s.stderr, "  workspace-walk: %v\n", err)
	}

	if len(matches) == 0 {
		// A named Release's lookup was read above; a request withdrawn during the
		// walk does not go on to read the catalog.
		if terms.release != "" || reqCtx.Err() != nil {
			return nil, nil
		}
		loc, ok := s.catalogDefinitionFor(terms, reference)
		if !ok {
			return nil, nil
		}
		return loc, nil
	}

	if len(matches) > 1 {
		paths := make([]string, 0, len(matches))
		for _, m := range matches {
			paths = append(paths, m.path)
		}
		fmt.Fprintf(s.stderr, "  duplicate metadata.name %q across %v; using first match\n", name, paths)
	}

	first := matches[0]
	if expectedVersion != "" && first.version != "" && first.version != expectedVersion {
		fmt.Fprintf(s.stderr, "  version mismatch: requested %q but found %q at %s\n",
			name+":"+expectedVersion, name+":"+first.version, first.path)
	}
	return &first.location, nil
}

// catalogDefinitionFor is the definition the catalog lookup the terms read gives
// for reference: the metadata.name of the document catalogDocumentFor answers,
// positioned in the bytes it answers with. ok is false where that document is no
// answer, or declares no metadata.name to point at.
func (s *Server) catalogDefinitionFor(terms releaseTerms, reference string) (*yamlpos.Location, bool) {
	path, doc, content, ok := s.catalogDocumentFor(terms, reference)
	if !ok {
		return nil, false
	}
	_, nameNode := metadataNameOf(doc)
	if nameNode == nil {
		return nil, false
	}
	loc := nodeToLocation(path, yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding()), nameNode)
	return &loc, true
}

// catalogDocumentFor resolves reference through the catalog lookup the terms
// read, and returns the document the matched entity was authored in: its path,
// its parsed root and its bytes. ok is false where the lookup resolves nothing,
// or resolves an entity with no authored document to name — one read from the
// artifact cache — or the document cannot be read.
//
// The resolution is the by-name one every surface shares
// (compilerops.ResolveEntity), over a request-scoped view holding no pins, as a
// lookup by name reads it: the reference is resolved the way the catalog
// directories declare it, the version it names selected as a build selects it.
//
// The lookup picks the document from the catalog directories as they stand on
// disk; what is answered for it is the copy the editor holds of that path where
// the parse cache holds one still declaring the name the reference names, so a
// position falls on the line the editor shows and a hover renders what the
// author is reading, unsaved changes included. The cache is asked by the same
// key the request's own document is asked by, and drops a copy the file on disk
// has since moved past. Where no such copy is held — or the one held declares
// some other name, a rename the catalog directories have not seen yet — the bytes
// are read from disk, as the workspace walk reads a document whose cached copy
// does not match.
func (s *Server) catalogDocumentFor(terms releaseTerms, reference string) (string, *yaml.Node, []byte, bool) {
	if terms.lookup == nil {
		return "", nil, nil, false
	}
	match, found := compilerops.ResolveEntity(terms.lookup.ForRequest(nil), reference)
	if !found || match.Source == "" {
		return "", nil, nil, false
	}
	path := match.Source
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.workspaceRoot, path)
	}
	path = s.canonicalFile(path)
	if entry, held := s.parseCache.Get(path); held {
		name, _, _ := resolver.SplitVersionedURI(reference)
		if declared, _ := metadataNameOf(entry.node); declared == name {
			return path, entry.node, entry.content, true
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, false
	}
	var doc yaml.Node
	if uerr := yaml.Unmarshal(content, &doc); uerr != nil {
		return "", nil, nil, false
	}
	return path, &doc, content, true
}

// resolveUsesTarget walks the cursor's source document for a sibling
// Component whose key matches targetName. Returns the Location at the
// matching key scalar; nil when no sibling matches.
//
// The walk visits all spec.components keys at the top level plus any
// recursively-nested spec.subsystems.<*>.components keys. Cross-
// Subsystem references via uses.target are permitted by the Aggregate
// schema (no documented scoping restriction); the walker mirrors that
// permissiveness.
func (s *Server) resolveUsesTarget(uri string, doc *yaml.Node, idx yamlpos.LineIndex, targetName string) *yamlpos.Location {
	root := documentRoot(doc)
	spec := mappingChild(root, "spec")
	if spec == nil {
		return nil
	}
	if keyNode := findComponentKey(spec, targetName); keyNode != nil {
		loc := nodeToLocation(uri, idx, keyNode)
		return &loc
	}
	return nil
}

// findComponentKey recursively walks a spec subtree looking for a
// "components/<targetName>" mapping pair. Returns the key node when
// found; nil otherwise. Recurses into spec.subsystems.<*> at arbitrary
// depth.
func findComponentKey(specMap *yaml.Node, targetName string) *yaml.Node {
	if specMap == nil || specMap.Kind != yaml.MappingNode {
		return nil
	}
	if components := mappingChild(specMap, "components"); components != nil && components.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(components.Content); i += 2 {
			if components.Content[i].Value == targetName {
				return components.Content[i]
			}
		}
	}
	if subsystems := mappingChild(specMap, "subsystems"); subsystems != nil && subsystems.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(subsystems.Content); i += 2 {
			if keyNode := findComponentKey(subsystems.Content[i+1], targetName); keyNode != nil {
				return keyNode
			}
		}
	}
	return nil
}

// resolveReleasePath joins relativePath with workspaceRoot, appends
// ".yaml", reads + parses the file, and returns the Location at the
// file's metadata.name. When metadata.name is absent, falls back to
// the document's first line (line 0, char 0). Returns nil when the
// file is missing or unparseable.
//
// The handler does NOT validate the target file's kind — `spec.aggregate:
// foo` returns a Location at `foo.yaml` regardless of whether that file
// is actually an Aggregate. Misdirected references surface as
// "the operator's mistake" at resolve time, not at classification time.
func (s *Server) resolveReleasePath(relativePath string) *yamlpos.Location {
	full := filepath.Join(s.workspaceRoot, relativePath+".yaml")
	content, err := os.ReadFile(full)
	if err != nil {
		return nil
	}
	var doc yaml.Node
	if unErr := yaml.Unmarshal(content, &doc); unErr != nil {
		fmt.Fprintf(s.stderr, "  release-path parse: %s: %v\n", full, unErr)
		return nil
	}
	idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding())
	if _, nameNode := metadataNameOf(&doc); nameNode != nil {
		loc := nodeToLocation(full, idx, nameNode)
		return &loc
	}
	return &yamlpos.Location{
		URI: full,
		Range: yamlpos.Range{
			Start: yamlpos.Position{Line: 0, Character: 0},
			End:   yamlpos.Position{Line: 0, Character: 0},
		},
	}
}

// scalarValueAtPath returns the string value at the supplied mapping
// path, or "" when the path doesn't resolve to a scalar.
func scalarValueAtPath(doc *yaml.Node, path []string) string {
	n := scalarAtPath(documentRoot(doc), path)
	if n == nil {
		return ""
	}
	return n.Value
}

// mappingChild returns the value node for key in the supplied mapping,
// or nil when n is not a mapping or key is absent.
func mappingChild(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
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
//
// Only the documents reach holds are read: a request naming a Release reports
// no reference from a document under one of the workspace's catalog directories
// that Release does not read, and one naming none passes the zero reach, which
// holds every document.
func (s *Server) findReferencesAcrossWorkspace(reqCtx context.Context, uri string, pos yamlpos.Position, reach workspaceconfig.ReleaseReach) ([]yamlpos.Location, *jsonRPCError) {
	target, errEnvelope := s.uriAtMetadataName(uri, pos)
	if errEnvelope != nil {
		return nil, errEnvelope
	}
	if target == "" {
		return []yamlpos.Location{}, nil
	}
	var out []yamlpos.Location
	var selfDefinitions []string
	err := s.walkWorkspaceYAML(reqCtx, func(path, read string) error {
		if !reach.Holds(path) {
			return nil
		}
		content, readErr := os.ReadFile(read)
		if readErr != nil {
			return nil
		}
		var doc yaml.Node
		if unErr := yaml.Unmarshal(content, &doc); unErr != nil {
			fmt.Fprintf(s.stderr, "  workspace-walk parse: %s: %v\n", path, unErr)
			return nil
		}
		idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding())
		root := documentRoot(&doc)
		// Track files whose own metadata.name matches the search target. When more than one such file exists,
		// emit a stderr warning at walk-end. This is symmetric to the duplicate-name warning emitted by
		// findDefinitionByURI in the resolve path.
		if entityName, nameNode := metadataNameOf(&doc); entityName == target && nameNode != nil {
			selfDefinitions = append(selfDefinitions, path)
		}
		collectReferences(root, idx, path, target, nil, &out)
		return nil
	})
	if err != nil {
		fmt.Fprintf(s.stderr, "  workspace-walk: %v\n", err)
	}
	if len(selfDefinitions) > 1 {
		fmt.Fprintf(s.stderr, "  duplicate metadata.name %q across %v; references aggregate from all sources\n",
			target, selfDefinitions)
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
//
// Only the documents reach holds are read, as findReferencesAcrossWorkspace
// reads them: a request naming a Release reports only the implementations and
// children that Release's answer holds.
func (s *Server) findImplementationsAcrossWorkspace(reqCtx context.Context, uri string, pos yamlpos.Position, reach workspaceconfig.ReleaseReach) ([]yamlpos.Location, *jsonRPCError) {
	target, errEnvelope := s.uriAtMetadataName(uri, pos)
	if errEnvelope != nil {
		return nil, errEnvelope
	}
	if target == "" {
		return []yamlpos.Location{}, nil
	}
	var out []yamlpos.Location
	err := s.walkWorkspaceYAML(reqCtx, func(path, read string) error {
		if !reach.Holds(path) {
			return nil
		}
		content, readErr := os.ReadFile(read)
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
		if implementsVal != nil {
			implName, _, _ := resolver.SplitVersionedURI(implementsVal.Value)
			if implName == target {
				hit = true
			}
		}
		if extendsVal != nil {
			extName, _, _ := resolver.SplitVersionedURI(extendsVal.Value)
			if extName == target {
				hit = true
			}
		}
		if !hit {
			return nil
		}
		_, nameNode := metadataNameOf(&doc)
		if nameNode == nil {
			return nil
		}
		idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding())
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
	entry, ok := s.parseCache.Get(s.canonicalFile(uri))
	if !ok {
		return "", &jsonRPCError{
			Code:    errFileNotValidated,
			Message: fmt.Sprintf("file not validated: %s", uri),
		}
	}
	idx := yamlpos.BuildLineIndexWithEncoding(entry.content, s.positionEncoding())
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
				// Scanned value may carry a "<name>:<version>" suffix
				// while the target carries the bare name. Split before
				// comparing.
				scannedName, _, _ := resolver.SplitVersionedURI(valNode.Value)
				if scannedName == target && isReferenceShape(childPath) {
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
		yamlpos.ContextKindRecipeImplements,
		yamlpos.ContextKindManifestRecipeReference:
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
