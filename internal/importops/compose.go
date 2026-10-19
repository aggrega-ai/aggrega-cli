// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ManifestFileName is the name of the Manifest an import given a target drafts,
// beside the drafted Release — or, where the import drafted none, beside the
// drafted Aggregate.
const ManifestFileName = "manifest.yaml"

// manifestKindToken is the schema kind token the Manifest's starter document is
// drafted from.
const manifestKindToken = "manifest"

// checkTarget refuses a target that no Recipe in the import's catalog binds to,
// as the occurrence of [ErrImportTargetUnbound] naming the target exactly as
// given and the capabilities the catalog's Recipes do bind to. An empty target
// asks for no Manifest and is never refused.
//
// recorded says the target is the one the record of an earlier import names,
// taken by a sync given none of its own. Leaving the target out does not leave
// such a target out, so the refusal names where it came from and offers only
// naming another.
//
// [Import] asks it once the catalog is loaded and before the source is recognised
// or the importer runs, so a refused target runs no parser and no importer and
// writes nothing — on every surface alike, because every surface reaches it only
// through [Import].
func checkTarget(target string, recorded bool, recipes RecipeIndex) error {
	if target == "" {
		return nil
	}
	capabilities := recipeCapabilities(recipes)
	if slices.Contains(capabilities, target) {
		return nil
	}
	values := []string{"target " + strconv.Quote(target)}
	if recorded {
		values = append(values, "recorded by the earlier import in "+RecordFileName)
	}
	advice := "Name one of those capabilities as the target, or leave the target out and no Manifest is drafted; " +
		"nothing was drafted or written"
	if recorded {
		advice = "Name one of those capabilities as the target; nothing was drafted or written"
	}
	if len(capabilities) == 0 {
		values = append(values, "the catalog holds no Recipe")
		advice = "Add a Recipe of the provider to the workspace's catalog, or leave the target out and no Manifest " +
			"is drafted; nothing was drafted or written"
		if recorded {
			advice = "Add a Recipe of the provider to the workspace's catalog; nothing was drafted or written"
		}
	} else {
		values = append(values, "the catalog's Recipes bind to "+quotedList(capabilities))
	}
	return cerrors.Raise(errImportTargetUnbound, "", cerrors.SourceLocation{},
		cerrors.ComposeMessage(errImportTargetUnbound, values, advice), nil)
}

// recipeCapabilities reads the capabilities the catalog's Recipes bind to, none
// where the seam carries no Recipe view.
func recipeCapabilities(recipes RecipeIndex) []string {
	if recipes == nil {
		return nil
	}
	return recipes.RecipeCapabilities()
}

// composeDraftSet composes the draft set an import hands back from the files the
// importer drafted, for the target the import was given.
//
// 🔴 It is the one place the draft set is composed, and [Import] is its only
// caller: every surface hands its target to [Import] unchanged, so the command
// line, the editor's session and the assistant's tool draft the same files from
// the same source. A surface that composed on its own, or branched on the target,
// would draft a second answer to one request.
//
// Without a target the importer's files are returned exactly as drafted.
//
// With one, it drafts the Manifest the drafted Release needs, and adds no
// Component, no Feature and no instance: only a mapping file the operator reviews
// and owns. For each distinct component type the drafted Aggregate declares — on
// its top-level components or on any subsystem's, in the order the Aggregate
// first declares each, a type the import itself drafted as a new Component
// included — it asks the catalog for the Recipes implementing the type (the
// enumeration the no-mapping diagnostic lists its pin candidates from, which asks
// for a type naming no version at the version a build resolves it to, the
// highest of that Component the catalog holds) and keeps those whose bare
// spec.capability is the target. The mapping is keyed by the type as the
// Aggregate writes it, so a key naming no version matches the type at any
// version, as a build matches it. Several versions of one Recipe
// are one Recipe. Exactly one Recipe maps the type, by its capability-qualified
// reference with no version, which the dependency resolution pins at the
// version it resolves; none, or Recipes of more than one name, leave the type
// unmapped and say so in a warning naming the type and why — the several each by
// reference — so a build of the drafts halts naming each instance of it, as it
// does for any unmapped type. Feature attachments are not mapped. What a Recipe
// renders, templates or means is never read: only its capability and the type it
// implements.
//
// The Manifest is drafted from the Manifest kind's own starter document
// ([scaffoldops.Skeleton]), so its envelope has the one spelling every drafted
// starter document has, and it is named and versioned as the drafted Release is
// — or, where the import drafted none, as the drafted Aggregate is. It is placed
// beside that document as [ManifestFileName] and appended after the importer's
// files.
//
// Where the drafted Release names no Manifest, its spec.manifest is set to the
// drafted Manifest's name, the bare token a Release resolves by identity in its
// own directory: the comment line the importer wrote in its place is replaced by
// that one key, or, where there is none, the key is added as spec's first entry,
// and every other byte the importer wrote is kept. A Release already naming a
// Manifest is left as written, and an information diagnostic says so; so is a
// target with no drafted Release, where the diagnostic says nothing references
// the Manifest. An import that drafted no Aggregate drafts no Manifest, and says
// so; nor does one whose importer already drafted a file where the Manifest
// would go, which is reported as a warning rather than written over.
//
// The diagnostics it returns follow the importer's, in the order the types are
// declared, so one source drafts one byte sequence on every run.
func composeDraftSet(target string, recipes RecipeIndex, files []abi.ScaffoldFile) ([]abi.ScaffoldFile,
	[]abi.Diagnostic, error) {
	if target == "" {
		return files, nil, nil
	}
	aggregateAt, releaseAt := -1, -1
	for i, f := range files {
		switch draftKind(f.Content) {
		case entitykind.Aggregate:
			if aggregateAt < 0 {
				aggregateAt = i
			}
		case entitykind.Release:
			if releaseAt < 0 {
				releaseAt = i
			}
		}
	}
	if aggregateAt < 0 {
		return files, []abi.Diagnostic{infoDiagnostic(fmt.Sprintf("the target %s drafts no Manifest, since the "+
			"importer drafted no Aggregate whose component types it could map", strconv.Quote(target)))}, nil
	}
	aggregate, err := readDraft(files[aggregateAt].Content)
	if err != nil {
		return nil, nil, fmt.Errorf("read the drafted Aggregate %q to draft its Manifest: %w", files[aggregateAt].Path, err)
	}
	namedBy, namedByPath := aggregate, files[aggregateAt].Path
	var release *yaml.Node
	if releaseAt >= 0 {
		if release, err = readDraft(files[releaseAt].Content); err != nil {
			return nil, nil, fmt.Errorf("read the drafted Release %q to draft its Manifest: %w", files[releaseAt].Path, err)
		}
		namedBy, namedByPath = release, files[releaseAt].Path
	}

	manifestPath := path.Join(path.Dir(namedByPath), ManifestFileName)
	for _, f := range files {
		if f.Path == manifestPath {
			return files, []abi.Diagnostic{warnDiagnostic(fmt.Sprintf("the target %s drafts no Manifest, since the "+
				"importer already drafted %s, which is left as it drafted it", strconv.Quote(target),
				strconv.Quote(manifestPath)))}, nil
		}
	}

	name := scalarAt(namedBy, "metadata", "name")
	version := scalarAt(namedBy, "metadata", "version")
	if version == "" && namedBy != aggregate {
		version = scalarAt(aggregate, "metadata", "version")
	}

	var mappings []manifestMapping
	var diags []abi.Diagnostic
	for _, componentType := range declaredComponentTypes(aggregate) {
		reference, unmapped := mappingFor(target, componentType, recipes)
		if unmapped != "" {
			diags = append(diags, warnDiagnostic(unmapped))
			continue
		}
		mappings = append(mappings, manifestMapping{componentType: componentType, reference: reference})
	}

	manifest, err := draftManifest(name, version, mappings)
	if err != nil {
		return nil, nil, err
	}
	out := append(make([]abi.ScaffoldFile, 0, len(files)+1), files...)

	if releaseAt < 0 {
		diags = append(diags, infoDiagnostic(fmt.Sprintf("the importer drafted no Release, so nothing references "+
			"the drafted Manifest %s; name it as a Release's spec.manifest to build against it",
			strconv.Quote(manifestPath))))
	} else if existing := scalarAt(release, "spec", "manifest"); existing != "" || hasKey(release, "spec", "manifest") {
		diags = append(diags, infoDiagnostic(fmt.Sprintf("the drafted Release %s already names the Manifest %s, so "+
			"it is left as the importer drafted it; the drafted Manifest %s is not referenced by it",
			strconv.Quote(files[releaseAt].Path), strconv.Quote(existing), strconv.Quote(manifestPath))))
	} else {
		edited, ok := setReleaseManifest(files[releaseAt].Content, release, name)
		if ok {
			out[releaseAt] = abi.ScaffoldFile{Path: files[releaseAt].Path, Content: edited}
		} else {
			diags = append(diags, warnDiagnostic(fmt.Sprintf("the drafted Release %s carries no spec block the "+
				"Manifest reference could be written into; set its spec.manifest to %s by hand",
				strconv.Quote(files[releaseAt].Path), strconv.Quote(name))))
		}
	}
	return append(out, abi.ScaffoldFile{Path: manifestPath, Content: manifest}), diags, nil
}

// manifestMapping is one entry of the drafted Manifest's component type mapping.
type manifestMapping struct {
	componentType string
	reference     string
}

// mappingFor answers what the drafted Manifest maps componentType to for target:
// the capability-qualified, unversioned reference of the one Recipe of that
// capability implementing it, or, where there is none or more than one, the
// warning saying so and nothing to map.
//
// componentType is handed to the Recipe index exactly as the drafted Aggregate
// writes it: the index resolves a type naming no version to the version a build
// resolves it to, so no version is chosen here. A type the catalog holds no
// Component of — one the import drafted new — finds a Recipe only where one
// implements that unversioned type itself.
func mappingFor(target, componentType string, recipes RecipeIndex) (reference, unmapped string) {
	var references []string
	if recipes != nil {
		for _, r := range recipes.RecipesImplementing(componentType) {
			if types.ParseVersionPin(r.Spec.Capability).Base() != target {
				continue
			}
			ref := target + "/" + r.Metadata.Name
			if !slices.Contains(references, ref) {
				references = append(references, ref)
			}
		}
	}
	switch len(references) {
	case 1:
		return references[0], ""
	case 0:
		return "", fmt.Sprintf("the drafted Manifest leaves the component type %s unmapped: no Recipe in the "+
			"workspace's catalog binds to the target %s and implements it, so a build of the drafts halts naming "+
			"each instance of it until a mapping is added", strconv.Quote(componentType), strconv.Quote(target))
	default:
		return "", fmt.Sprintf("the drafted Manifest leaves the component type %s unmapped: the workspace's catalog "+
			"holds more than one Recipe binding to the target %s that implements it (%s), and the import does not "+
			"choose between them, so a build of the drafts halts naming each instance of it until the one wanted is "+
			"mapped", strconv.Quote(componentType), strconv.Quote(target), quotedList(references))
	}
}

// draftManifest renders the drafted Manifest: the Manifest kind's starter
// document with its name and version filled and its component type mapping set,
// in the order given.
func draftManifest(name, version string, mappings []manifestMapping) (string, error) {
	skeleton, err := scaffoldops.Skeleton(manifestKindToken, name)
	if err != nil {
		return "", fmt.Errorf("draft the Manifest's starter document: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(skeleton), &doc); err != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return "", fmt.Errorf("read the Manifest's starter document back: %v", err)
	}
	root := doc.Content[0]
	if version != "" {
		if node := mappingValue(mappingValue(root, "metadata"), "version"); node != nil {
			*node = *plainScalar(version)
		}
	}
	spec := mappingValue(root, "spec")
	mappingsNode := mappingValue(spec, "mappings")
	if mappingsNode == nil {
		return "", fmt.Errorf("the Manifest's starter document carries no spec.mappings to fill")
	}
	byType := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, m := range mappings {
		byType.Content = append(byType.Content, plainScalar(m.componentType), plainScalar(m.reference))
	}
	components := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map",
		Content: []*yaml.Node{plainScalar("type"), byType}}
	*mappingsNode = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map",
		Content: []*yaml.Node{plainScalar("components"), components}}

	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		_ = enc.Close()
		return "", fmt.Errorf("encode the drafted Manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("encode the drafted Manifest: %w", err)
	}
	return b.String(), nil
}

// setReleaseManifest returns the drafted Release's text with spec.manifest set
// to name, and whether it could be set: false where the Release carries no block
// spec mapping to write the key into.
//
// The text is edited by line rather than decoded and re-encoded, so every byte
// the importer wrote outside the one line is kept: the comment line naming the
// unset manifest at spec's own indentation is replaced by the key, and where
// there is no such line the key is added directly beneath spec.
func setReleaseManifest(content string, release *yaml.Node, name string) (string, bool) {
	specKey, spec := mappingEntry(release, "spec")
	if spec == nil || spec.Kind != yaml.MappingNode || spec.Style&yaml.FlowStyle != 0 || len(spec.Content) == 0 {
		return "", false
	}
	col := spec.Content[0].Column - 1
	lines := splitLines(content)
	if specKey.Line < 1 || specKey.Line >= len(lines) {
		return "", false
	}
	eol := "\n"
	if strings.HasSuffix(lines[specKey.Line-1], "\r\n") {
		eol = "\r\n"
	}
	entry := strings.Repeat(" ", col) + "manifest: " + yamlScalarText(name) + eol
	// The lines spec's block holds run from the line after its key to the line
	// before the next entry at the top level.
	end := len(lines)
	if next := nextTopLevelKeyLine(release, specKey); next > 0 {
		end = next - 1
	}
	for i := specKey.Line; i < end; i++ {
		if commentAt(lines[i], col) && strings.HasPrefix(lineText(lines[i])[col:], "# manifest:") {
			lines[i] = entry
			return strings.Join(lines, ""), true
		}
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:specKey.Line]...)
	out = append(out, entry)
	out = append(out, lines[specKey.Line:]...)
	return strings.Join(out, ""), true
}

// nextTopLevelKeyLine is the line of the top-level key after key, or 0 where key
// is the last.
func nextTopLevelKeyLine(root, key *yaml.Node) int {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i] == key && i+2 < len(root.Content) {
			return root.Content[i+2].Line
		}
	}
	return 0
}

// declaredComponentTypes returns each distinct component type the drafted
// Aggregate declares, on its top-level components and on every subsystem's, in
// the order the Aggregate first declares it. A type is read as the identity the
// document writes, and never resolved.
func declaredComponentTypes(aggregate *yaml.Node) []string {
	var out []string
	add := func(components *yaml.Node) {
		if components == nil || components.Kind != yaml.MappingNode {
			return
		}
		for i := 1; i < len(components.Content); i += 2 {
			typ := scalarValue(mappingValue(components.Content[i], "type"))
			if typ != "" && !slices.Contains(out, typ) {
				out = append(out, typ)
			}
		}
	}
	spec := mappingValue(aggregate, "spec")
	add(mappingValue(spec, "components"))
	if subsystems := mappingValue(spec, "subsystems"); subsystems != nil && subsystems.Kind == yaml.MappingNode {
		for i := 1; i < len(subsystems.Content); i += 2 {
			add(mappingValue(subsystems.Content[i], "components"))
		}
	}
	return out
}

// draftKind is the top-level kind a drafted file's content declares, or "" for
// content that does not read as a YAML document carrying one.
func draftKind(content string) entitykind.Kind {
	var head struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal([]byte(content), &head); err != nil {
		return ""
	}
	return entitykind.Kind(head.Kind)
}

// readDraft reads a drafted document's top-level mapping.
func readDraft(content string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("its top level is not a mapping")
	}
	return doc.Content[0], nil
}

// mappingEntry returns the key and value nodes for key in a mapping node, or nil
// for both where the node is not a mapping or the key is absent.
func mappingEntry(n *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}

// mappingValue returns the value node for key in a mapping node, or nil.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	_, v := mappingEntry(n, key)
	return v
}

// hasKey reports whether the mapping at the path holds key.
func hasKey(n *yaml.Node, parent, key string) bool {
	k, _ := mappingEntry(mappingValue(n, parent), key)
	return k != nil
}

// scalarAt is the scalar text at parent.key, or "" where there is none.
func scalarAt(n *yaml.Node, parent, key string) string {
	return scalarValue(mappingValue(mappingValue(n, parent), key))
}

// scalarValue is a scalar node's text, or "" for anything else.
func scalarValue(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// plainScalar is a string scalar node the encoder writes in whatever style its
// text needs.
func plainScalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// yamlScalarText is v written as one YAML string scalar, quoted where its text
// needs it, so a name an importer wrote reads back as the same string.
func yamlScalarText(v string) string {
	out, err := yaml.Marshal(plainScalar(v))
	if err != nil {
		return strconv.Quote(v)
	}
	return strings.TrimRight(string(out), "\n")
}

// infoDiagnostic is an information diagnostic the composition reports.
func infoDiagnostic(message string) abi.Diagnostic {
	return abi.Diagnostic{Severity: abi.SeverityInfo, Message: message}
}

// warnDiagnostic is a warning the composition reports.
func warnDiagnostic(message string) abi.Diagnostic {
	return abi.Diagnostic{Severity: abi.SeverityWarn, Message: message}
}
