// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ManifestFileName is the name of the Manifest an import given a target drafts,
// beside the drafted Release — or, where the import drafted none, beside the
// drafted Aggregate.
const ManifestFileName = "manifest.yaml"

// manifestKindToken is the schema kind token the Manifest's starter document is
// drafted from.
const manifestKindToken = "manifest"

// manifestCommentPrefix begins the comment line a drafted Release writes where,
// or directly above where, its spec.manifest goes: the line the composition
// replaces with the key, or drops once the key above which it stands is written.
const manifestCommentPrefix = "# manifest:"

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
// and owns. What it maps is every type the drafts need realized (draftNeeds):
// each component type of the drafted Aggregate's instances, at every depth of its
// subsystems, and each feature type its instances, their connections and its
// subsystems attach; the same of every catalog Aggregate it composes as a module,
// read from the catalog the import loaded, and of the modules those compose in
// turn, as deep as a build follows them — a module or an instance whose `active`
// toggle is written false left out, as a build leaves it out; and each feature
// type the drafted Release attaches, on its
// boundaries, their components and those components' connections. Types are
// mapped in the order the drafts first declare each — the Aggregate's own, then
// its modules', then the Release's — a type the import itself drafted as a new
// Component included.
//
// For each type it asks the catalog for the Recipes implementing it — or, for a
// feature type, the FeatureRecipes — through the enumeration the build's
// unmapped-instance refusal lists its pin candidates from, which asks for a type
// naming no version at the version a build resolves it to, and keeps those whose
// bare spec.capability is the target. Several versions of one Recipe are one
// Recipe. Exactly one Recipe maps the type, by its capability-qualified reference
// with no version, which the dependency resolution pins at the version it
// resolves. Recipes of more than one name map the type to the one named after the
// type's own last segment — compose/postgres for core/datastore/postgres — where
// there is one: the catalog's standard realization of that type, beside variants
// that wrap a chart or need a control plane. None, or several with none so named,
// leave the type unmapped and say so in a warning naming the type and why — the
// several each by reference — so a build of the drafts halts naming each
// instance or attachment of it, as it does for any unmapped type. The mapping is
// keyed by the type as the drafts write it, so a key naming no version matches the
// type at any version, as a build matches it. What a Recipe renders, templates or
// means is never read: only its name, its capability and the type it implements.
//
// An importer may draft the Manifest itself, holding what only the importer can
// choose: the Recipe one named instance renders through, or the FeatureRecipe one
// named attachment or a feature type is realized by, where the catalog offers
// several. Each mapping it drafts is kept where a build of the drafts accepts it
// — the reference, resolved by the catalog's own reading of a mapping value, is a
// Recipe or FeatureRecipe of the target that the build's mapping-consistency rule
// accepts for that type, or, for a mapping by name, which a build does not check,
// for the type of every instance or attachment of that name, the composer's own
// condition ([confirms]) — and dropped with a warning saying why where it does
// not; a type is then mapped as above unless a kept mapping already maps it, or every instance or
// attachment of it is mapped by name, which needs no type mapping and draws no
// warning. Each tier's annotation bindings the importer drafted are kept as
// drafted; nothing else of its Manifest is read, and the composed Manifest takes
// its place.
//
// The Manifest is drafted from the Manifest kind's own starter document
// ([scaffoldops.Skeleton]), so its envelope has the one spelling every drafted
// starter document has, and it is named and versioned as the drafted Release is
// — or, where the import drafted none, as the drafted Aggregate is. It is placed
// beside that document as [ManifestFileName] and appended after the importer's
// files, or written where the importer drafted its own.
//
// Where the drafted Release names no Manifest — it declares spec.manifest empty,
// or not at all — its spec.manifest is set to the drafted Manifest's name, the
// bare token a Release resolves by identity in its own directory: an empty key is
// written with that name, and the comment line beginning "# manifest:" directly
// above it dropped; a comment line of that beginning standing in the key's place
// is replaced by the key; where there is neither, the key is added as spec's
// first entry; and every other byte the importer wrote is kept. A Release already
// naming a Manifest is left as written, and an information diagnostic says so; so is a
// target with no drafted Release, where the diagnostic says nothing references
// the Manifest. An import that drafted no Aggregate drafts no Manifest, and says
// so; nor does one whose importer already drafted a file that is not a Manifest
// where the Manifest would go, which is reported as a warning rather than written
// over.
//
// The diagnostics it returns follow the importer's, in the order the drafts
// declare what each concerns, so one source drafts one byte sequence on every
// run.
func composeDraftSet(target string, recipes RecipeIndex, files []abi.ScaffoldFile) ([]abi.ScaffoldFile,
	[]abi.Diagnostic, error) {
	if target == "" {
		return files, nil, nil
	}
	aggregateAt, releaseAt, manifestAt := -1, -1, -1
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
		case entitykind.Manifest:
			if manifestAt < 0 {
				manifestAt = i
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
	if manifestAt >= 0 {
		manifestPath = files[manifestAt].Path
	} else {
		for _, f := range files {
			if f.Path == manifestPath {
				return files, []abi.Diagnostic{warnDiagnostic(fmt.Sprintf("the target %s drafts no Manifest, since the "+
					"importer already drafted %s, which is left as it drafted it", strconv.Quote(target),
					strconv.Quote(manifestPath)))}, nil
			}
		}
	}

	name := scalarAt(namedBy, "metadata", "name")
	version := scalarAt(namedBy, "metadata", "version")
	if version == "" && namedBy != aggregate {
		version = scalarAt(aggregate, "metadata", "version")
	}

	var releaseFile *abi.ScaffoldFile
	if releaseAt >= 0 {
		releaseFile = &files[releaseAt]
	}
	needs, diags, err := readDraftNeeds(files[aggregateAt], releaseFile, recipes)
	if err != nil {
		return nil, nil, err
	}
	var drafted types.ManifestMappings
	if manifestAt >= 0 {
		var m types.Manifest
		if err := yaml.Unmarshal([]byte(files[manifestAt].Content), &m); err != nil {
			return nil, nil, fmt.Errorf("read the Manifest the importer drafted as %q to complete it: %w",
				files[manifestAt].Path, err)
		}
		drafted = m.Spec.Mappings
	}
	components, componentDiags := composeTier(target, componentRealizations(target, recipes), needs.components,
		drafted.Components)
	features, featureDiags := composeTier(target, featureRealizations(target, recipes), needs.features,
		drafted.Features)
	diags = append(append(diags, componentDiags...), featureDiags...)

	manifest, err := draftManifest(name, version, components, features)
	if err != nil {
		return nil, nil, err
	}
	out := append(make([]abi.ScaffoldFile, 0, len(files)+1), files...)

	if releaseAt < 0 {
		diags = append(diags, infoDiagnostic(fmt.Sprintf("the importer drafted no Release, so nothing references "+
			"the drafted Manifest %s; name it as a Release's spec.manifest to build against it",
			strconv.Quote(manifestPath))))
	} else if existing := scalarAt(release, "spec", "manifest"); existing != "" || namesManifestOtherwise(release) {
		declares := "already names the Manifest " + strconv.Quote(existing)
		if existing == "" {
			declares = "declares spec.manifest with no name written in it"
		}
		diags = append(diags, infoDiagnostic(fmt.Sprintf("the drafted Release %s %s, so it is left as the importer "+
			"drafted it; the drafted Manifest %s is not referenced by it", strconv.Quote(files[releaseAt].Path),
			declares, strconv.Quote(manifestPath))))
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
	if manifestAt >= 0 {
		out[manifestAt] = abi.ScaffoldFile{Path: manifestPath, Content: manifest}
		return out, diags, nil
	}
	return append(out, abi.ScaffoldFile{Path: manifestPath, Content: manifest}), diags, nil
}

// realization is the catalog identity of one Recipe or FeatureRecipe: its bare
// capability, its name and its version.
type realization struct {
	capability, name, version string
}

// realizationKind is what the composition reads and says for one kind of type:
// the realizations of the target the catalog offers for a type of it, what a
// Manifest reference resolves to, whether the build accepts that as a type's
// realization, and the words its diagnostics use.
//
// Its catalog readings are unset where the composition has no catalog view, and
// then answer nothing: no offer, no resolution ([realizationKind.offered],
// [realizationKind.resolved]).
type realizationKind struct {
	// offers answers the realizations binding to the target that implement typ.
	offers func(typ string) []realization
	// resolve answers the bare capability and the declared spec.implements of
	// what the catalog resolves reference to, as it resolves a Manifest mapping
	// value, and whether it resolves one.
	resolve func(reference string) (capability, implements string, ok bool)
	// accepts is the build's own mapping-consistency judgement of a realization
	// declaring implements for typ.
	accepts func(implements, typ string) bool
	// typeNoun names a type of the kind, recipeNoun what realizes one,
	// memberNoun what a Manifest's name tier addresses, and haltNoun what a
	// build refusing an unmapped one names.
	typeNoun, recipeNoun, memberNoun, haltNoun string
}

// offered is offers, or nothing where the composition reads no catalog.
func (k realizationKind) offered(typ string) []realization {
	if k.offers == nil {
		return nil
	}
	return k.offers(typ)
}

// resolved is resolve, or no resolution where the composition reads no catalog.
func (k realizationKind) resolved(reference string) (capability, implements string, ok bool) {
	if k.resolve == nil {
		return "", "", false
	}
	return k.resolve(reference)
}

// readCatalog sets kind's catalog readings over one entity kind of the catalog:
// implementing is its enumeration of the entities implementing a type, byURI its
// resolution of a mapping value, identity reads an entity's capability, name,
// version and declared spec.implements, and ancestry is the catalog's extends
// ancestry of a component type, which the build's judgement reads.
func readCatalog[R any](kind *realizationKind, target string, featureTier bool, implementing func(string) []R,
	byURI func(string) (R, bool), identity func(R) (capability, name, version, implements string),
	ancestry func(string) ([]string, error)) {
	kind.offers = func(typ string) []realization {
		var out []realization
		for _, r := range implementing(typ) {
			capability, name, version, _ := identity(r)
			if found := (realization{types.BareCapability(capability), name, version}); found.capability == target {
				out = append(out, found)
			}
		}
		return out
	}
	kind.resolve = func(reference string) (string, string, bool) {
		r, ok := byURI(reference)
		if !ok {
			return "", "", false
		}
		capability, _, _, implements := identity(r)
		return types.BareCapability(capability), implements, true
	}
	kind.accepts = func(implements, typ string) bool {
		return resolver.ManifestMappingAccepts(implements, typ, featureTier, ancestry)
	}
}

// componentRealizations is the realization kind of component types: the Recipes
// of the target implementing one.
func componentRealizations(target string, recipes RecipeIndex) realizationKind {
	kind := realizationKind{typeNoun: "component type", recipeNoun: "Recipe", memberNoun: "component instance",
		haltNoun: "instance"}
	if recipes != nil {
		readCatalog(&kind, target, false, recipes.RecipesImplementing, recipes.RecipeByURI,
			func(r types.Recipe) (string, string, string, string) {
				return r.Spec.Capability, r.Metadata.Name, r.Metadata.Version, r.Spec.Implements
			}, recipes.ComponentAncestry)
	}
	return kind
}

// featureRealizations is the realization kind of feature types: the
// FeatureRecipes of the target implementing one.
func featureRealizations(target string, recipes RecipeIndex) realizationKind {
	kind := realizationKind{typeNoun: "feature type", recipeNoun: "feature recipe", memberNoun: "attachment",
		haltNoun: "attachment"}
	if recipes != nil {
		readCatalog(&kind, target, true, recipes.FeatureRecipesImplementing, recipes.FeatureRecipeByURI,
			func(r types.FeatureRecipe) (string, string, string, string) {
				return r.Spec.Capability, r.Metadata.Name, r.Metadata.Version, r.Spec.Implements
			}, recipes.ComponentAncestry)
	}
	return kind
}

// confirms reports whether reference, an importer's mapping for typ, is one a
// build of the drafts accepts at target: the catalog resolves it, by its own
// reading of a mapping value — capability-qualified or bare, at an exact version,
// a caret range or none — to a realization binding to target, and the build's own
// mapping-consistency rule accepts what that realization declares it implements
// as typ's realization, through typ's extends ancestry for a component type.
//
// The binding to target is the composition's own condition, not the build's: a
// build renders a realization of any capability, but a Manifest drafted for one
// target maps every type to that target.
func confirms(target, reference, typ string, kind realizationKind) bool {
	capability, implements, ok := kind.resolved(reference)
	return ok && capability == target && kind.accepts(implements, typ)
}

// composedTier is one mapping of the drafted Manifest — its component or its
// feature mapping — as the composition settles it: the type tier and the name
// tier, each in order, and the annotation bindings the importer drafted for it.
type composedTier struct {
	byType      types.OrderedMap[string]
	byName      types.OrderedMap[string]
	annotations *types.OrderedMap[types.AnnotationBinding]
}

// composeTier settles one mapping of the drafted Manifest for target from the
// types the drafts need of the kind and the mapping the importer drafted for it,
// nil where it drafted none, and returns it with the warnings it draws, in
// order: the importer's name-tier mappings dropped, its type-tier mappings
// dropped, then each type left unmapped, in the order the drafts declare it.
//
// An importer's name-tier mapping is kept where the drafts hold an instance or
// attachment of that name and [confirms] accepts the reference for the type of
// every one of them; a type-tier mapping where it accepts it for that type. A type a kept mapping maps, or every instance or attachment of
// which a kept name-tier mapping maps, is settled; every other type is mapped by
// [mappingFor]. A type-tier mapping the importer drafted for a type the drafts do
// not declare is kept after the rest, in the importer's order.
func composeTier(target string, kind realizationKind, uses typeUses, drafted *types.MappingTier) (composedTier,
	[]abi.Diagnostic) {
	var tier composedTier
	var diags []abi.Diagnostic
	importerTypes := types.OrderedMap[string]{}
	if drafted != nil {
		tier.annotations = drafted.Annotations
		for member, reference := range drafted.Name.All() {
			typs := uses.typesOf(member)
			if len(typs) == 0 {
				diags = append(diags, warnDiagnostic(fmt.Sprintf("the drafted Manifest drops the importer's mapping "+
					"of the %s %s to %s: the drafts declare no %s of that name", kind.memberNoun,
					strconv.Quote(member), strconv.Quote(reference), kind.memberNoun)))
				continue
			}
			kept := true
			for _, typ := range typs {
				if !confirms(target, reference, typ, kind) {
					diags = append(diags, warnDiagnostic(fmt.Sprintf("the drafted Manifest drops the importer's "+
						"mapping of the %s %s to %s: the workspace's catalog holds no %s of that name binding to the "+
						"target %s that implements its type %s", kind.memberNoun, strconv.Quote(member),
						strconv.Quote(reference), kind.recipeNoun, strconv.Quote(target), strconv.Quote(typ))))
					kept = false
					break
				}
			}
			if kept {
				tier.byName.Set(member, reference)
			}
		}
		for typ, reference := range drafted.Type.All() {
			if !confirms(target, reference, typ, kind) {
				diags = append(diags, warnDiagnostic(fmt.Sprintf("the drafted Manifest drops the importer's mapping "+
					"of the %s %s to %s: the workspace's catalog holds no %s of that name binding to the target %s "+
					"that implements it", kind.typeNoun, strconv.Quote(typ), strconv.Quote(reference), kind.recipeNoun,
					strconv.Quote(target))))
				continue
			}
			importerTypes.Set(typ, reference)
		}
	}
	for _, use := range uses {
		if reference, ok := importerTypes.Get(use.typ); ok {
			tier.byType.Set(use.typ, reference)
			continue
		}
		if allNamed(use.names, &tier.byName) {
			continue
		}
		reference, unmapped := mappingFor(target, kind, use.typ)
		if unmapped != "" {
			diags = append(diags, warnDiagnostic(unmapped))
			continue
		}
		tier.byType.Set(use.typ, reference)
	}
	for typ, reference := range importerTypes.All() {
		if _, ok := tier.byType.Get(typ); !ok {
			tier.byType.Set(typ, reference)
		}
	}
	return tier, diags
}

// allNamed reports whether byName maps every one of names.
func allNamed(names []string, byName *types.OrderedMap[string]) bool {
	for _, name := range names {
		if _, ok := byName.Get(name); !ok {
			return false
		}
	}
	return len(names) > 0
}

// mappingFor answers what the drafted Manifest maps typ to for target: the
// capability-qualified, unversioned reference of the one realization of that
// capability the catalog offers for it — or, where it offers several, of the one
// named after the type's own last segment — or, where there is none, or several
// with none so named, the warning saying so and nothing to map.
//
// typ is handed to the catalog's enumeration exactly as the drafts write it: the
// enumeration resolves a type naming no version to the version a build resolves
// it to, so no version is chosen here. A type the catalog holds no entity of —
// one the import drafted new — finds a realization only where one implements that
// unversioned type itself.
func mappingFor(target string, kind realizationKind, typ string) (reference, unmapped string) {
	var references []string
	for _, r := range kind.offered(typ) {
		ref := target + "/" + r.name
		if !slices.Contains(references, ref) {
			references = append(references, ref)
		}
	}
	switch len(references) {
	case 1:
		return references[0], ""
	case 0:
		return "", fmt.Sprintf("the drafted Manifest leaves the %s %s unmapped: no %s in the workspace's catalog "+
			"binds to the target %s and implements it, so a build of the drafts halts naming each %s of it until a "+
			"mapping is added", kind.typeNoun, strconv.Quote(typ), kind.recipeNoun, strconv.Quote(target),
			kind.haltNoun)
	}
	named := path.Base(types.ParseVersionPin(typ).Base())
	if standard := target + "/" + named; slices.Contains(references, standard) {
		return standard, ""
	}
	return "", fmt.Sprintf("the drafted Manifest leaves the %s %s unmapped: the workspace's catalog holds more than "+
		"one %s binding to the target %s that implements it (%s), none of them named %s after the type, so the "+
		"import does not choose between them, and a build of the drafts halts naming each %s of it until the one "+
		"wanted is mapped", kind.typeNoun, strconv.Quote(typ), kind.recipeNoun, strconv.Quote(target),
		quotedList(references), strconv.Quote(named), kind.haltNoun)
}

// draftManifest renders the drafted Manifest: the Manifest kind's starter
// document with its name and version filled and its mappings set — the
// component mapping's type tier always, and every other tier where it holds
// something.
func draftManifest(name, version string, components, features composedTier) (string, error) {
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
	componentsNode, err := tierNode(components, true)
	if err != nil {
		return "", err
	}
	*mappingsNode = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map",
		Content: []*yaml.Node{plainScalar("components"), componentsNode}}
	featuresNode, err := tierNode(features, false)
	if err != nil {
		return "", err
	}
	if len(featuresNode.Content) > 0 {
		mappingsNode.Content = append(mappingsNode.Content, plainScalar("features"), featuresNode)
	}

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

// tierNode renders one composed mapping: its type tier — written even when
// empty where withType says so — its name tier where it holds any entry, and
// the importer's annotation bindings where it drafted any.
func tierNode(tier composedTier, withType bool) (*yaml.Node, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	entries := func(m *types.OrderedMap[string]) *yaml.Node {
		out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for key, value := range m.All() {
			out.Content = append(out.Content, plainScalar(key), plainScalar(value))
		}
		return out
	}
	if withType || tier.byType.Len() > 0 {
		node.Content = append(node.Content, plainScalar("type"), entries(&tier.byType))
	}
	if tier.byName.Len() > 0 {
		node.Content = append(node.Content, plainScalar("name"), entries(&tier.byName))
	}
	if tier.annotations != nil {
		annotations := &yaml.Node{}
		if err := annotations.Encode(*tier.annotations); err != nil {
			return nil, fmt.Errorf("encode the annotation bindings the importer drafted: %w", err)
		}
		node.Content = append(node.Content, plainScalar("annotations"), annotations)
	}
	return node, nil
}

// namesManifestOtherwise reports whether the drafted Release declares
// spec.manifest with a value that is neither a name nor declared empty — null, or
// a value that is not a scalar — which the composition leaves as written.
func namesManifestOtherwise(release *yaml.Node) bool {
	_, value := mappingEntry(mappingValue(release, "spec"), "manifest")
	return value != nil && !manifestDeclaredEmpty(value)
}

// manifestDeclaredEmpty reports whether value, the value of a drafted Release's
// spec.manifest, is the empty string, which names no Manifest: it is the key a
// drafted Release declares where the import was given no target, and the
// composition writes the name into it. Null is not: it is a value the author
// wrote, which the composition leaves as written.
func manifestDeclaredEmpty(value *yaml.Node) bool {
	return value != nil && value.Kind == yaml.ScalarNode && value.Tag == "!!str" && value.Value == ""
}

// setReleaseManifest returns the drafted Release's text with spec.manifest set
// to name, and whether it could be set: false where the Release carries no block
// spec mapping to write the key into.
//
// The text is edited by line rather than decoded and re-encoded, so every byte
// the importer wrote outside the lines it replaces is kept: a spec.manifest key
// declared empty is written with the name, and the comment line beginning
// [manifestCommentPrefix] directly above it, at spec's own indentation, is
// dropped; else such a comment line standing in the key's place is replaced by
// the key; and where there is neither the key is added directly beneath spec.
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
	manifestComment := func(line string) bool {
		return commentAt(line, col) && strings.HasPrefix(lineText(line)[col:], manifestCommentPrefix)
	}
	// The lines spec's block holds run from the line after its key to the line
	// before the next entry at the top level.
	end := len(lines)
	if next := nextTopLevelKeyLine(release, specKey); next > 0 {
		end = next - 1
	}
	if key, value := mappingEntry(spec, "manifest"); key != nil && manifestDeclaredEmpty(value) &&
		key.Line > specKey.Line && key.Line <= len(lines) {
		at := key.Line - 1
		lines[at] = entry
		if above := at - 1; above >= specKey.Line && manifestComment(lines[above]) {
			lines = append(lines[:above], lines[at:]...)
		}
		return strings.Join(lines, ""), true
	}
	for i := specKey.Line; i < end; i++ {
		if manifestComment(lines[i]) {
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
