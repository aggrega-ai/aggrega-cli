// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// This file is the fact registry: the single place a constraint fact is
// declared. Everything the framework knows about placement is derived from it —
// the view stages, the subject-kind set, the placement table — so adding,
// moving or removing a fact is a one-line edit here whose consequences appear
// as a diff in generated output rather than as a judgement buried inside a
// check.
//
// The registry grows. It stays one file while it does, because a second file
// would be a second place to look for the answer to "is this fact already
// declared", which is the question the registry exists to make cheap.

// stages declares the view chain, root first. The order in this slice is not
// the chain — the Extends relation is — and the generator refuses a relation
// that is not a single chain.
//
// Every stage declares its host powers, including the ones exercising none. The
// value describes what that stage's own fact production reaches for and never
// what a check at that stage may do; the comment on each declaration is the
// reason the value is what it is.
var stages = []stageDecl{
	{
		Name:    "document",
		Extends: "",
		Holds: "one authored document as the loader hands it over: parsed and " +
			"envelope-checked, neither normalized nor parameter-interpolated",
		// The facts here are read out of bytes a caller hands over — a loader
		// for a file in the workspace, an editor for a buffer never written —
		// so this production reaches for nothing itself. That is the same
		// property the stage's push traversal rests on.
		HostPowers: noHostPowers,
	},
	{
		Name:    "normalized",
		Extends: "document",
		Holds: "both authoring documents normalized and parameter-interpolated, " +
			"with the catalog reachable",
		// Resolving the catalog entity an instance names is what makes these
		// facts complete, and a catalog entry is reached either as a directory
		// in the workspace or as a published artifact fetched into it.
		HostPowers: []hostPower{powerFilesystem, powerArtifactFetch},
	},
	{
		Name:    "topology",
		Extends: "normalized",
		Holds: "the flattened node set, including sub-aggregate nodes, with the " +
			"prune set, per-node resolved parameters, and the connection set " +
			"every origin has contributed to",
		// Flattening loads every sub-aggregate the Aggregate names, which is a
		// further authored document reached through the same catalog.
		//
		// The connection set is named here rather than at the stage after it
		// because it is complete here: every origin that draws a connection —
		// a component's own dependency block wherever it was authored, an
		// attachment's, and the deployment's edge surface — has contributed by
		// the time this stage holds, and the later graph adds none. A stage
		// naming it later would place a fact reading it after its derivation
		// finished, and so report a rule as askable later than it is.
		HostPowers: []hostPower{powerFilesystem, powerArtifactFetch},
	},
	{
		Name:    "graph",
		Extends: "topology",
		Holds: "the finalised graph after the whole attachment cascade and " +
			"annotation-driven expansion",
		// The cascade and the expansion compute over the flattened node set and
		// the entities already resolved, so nothing further is reached.
		HostPowers: noHostPowers,
	},
	{
		Name:    "described",
		Extends: "graph",
		Holds: "every plugin a node's pipeline steps and its rendering provider " +
			"resolve to, loaded and self-described: the declared capabilities and " +
			"the scope each dispatched export declares, before any step runs",
		// Every fact here is a plugin's own self-report, and producing one
		// resolves the reference against the sources the workspace configures,
		// fetches the guest bytes, and calls describe() across the boundary.
		HostPowers: []hostPower{
			powerPluginResolution,
			powerArtifactFetch,
			powerWorkspaceConfiguration,
			powerGuestCrossing,
		},
	},
	{
		Name:    "hydrated",
		Extends: "described",
		Holds: "per-node resolved locals and provides, and the mid-render products " +
			"standing beside them — the resolved source, the parsed workload, " +
			"each artifact entry's template text before it is evaluated, the " +
			"attributed deliveries each node's injection pipe carries, and the " +
			"mechanisms each attachment's rendered injectMapping output names — " +
			"all of it state that exists before any artifact bytes",
		// The powers named here are the ones this production genuinely exercises,
		// and the filesystem read is the one a reader is most likely to think
		// wrong. An artifact entry and a workload body may each be authored as an
		// import rather than inline, and resolving either to the text it stands
		// for reads that file from the workspace — so a declaration of none here
		// would be a false assertion rather than an absent one, which is worse,
		// because a stage naming no powers is read as asserting it needs none.
		// Do not restore an empty declaration on the grounds that mid-render
		// state ought to be computed rather than reached for: locals and provides
		// alone are computed, as templates over values the graph already carries,
		// and they are no longer all this stage holds.
		HostPowers: []hostPower{powerFilesystem},
	},
	{
		Name:    "emitted",
		Extends: "hydrated",
		Holds: "each render's own emitted file set, complete and attributed: every " +
			"file that render produced, under the name it will be placed by, " +
			"together with the authored input block each came from — held per " +
			"carrier, so a node's own render, the render one attachment carries on " +
			"a node, and the render one boundary-scope attachment carries each " +
			"answer for their own emission — all of it before anything is sorted " +
			"into an assembly group",
		// Producing this stage's facts renders the node, and every input block that
		// can contribute a file goes through a guest: the source render, workload
		// synthesis, an artifact entry's projection, and a pipeline step's own
		// emission. The workspace read is named for the reason the stage before this
		// one names it — an artifact entry or a workload body may be authored as an
		// import, and the walk that turns either into an emitted file reads that
		// file from the workspace — so a declaration of the crossing alone would
		// assert this production needs nothing from the workspace, which is worse
		// than an absent claim because a stage naming one power is read as having
		// named them all.
		HostPowers: []hostPower{powerFilesystem, powerGuestCrossing},
	},
	{
		Name:    "dispatched",
		Extends: "emitted",
		Holds: "every node's own rendered output sorted into the assembly groups " +
			"it will be merged in: the evaluated assembly path and the provider " +
			"each group is keyed by, the boundaries that fed each, what each " +
			"group's elected engine declared about combining and naming that " +
			"group's output, and the names each group's contributors declared they " +
			"derived inside their own output — all of it complete before any group " +
			"assembles",
		// Producing this stage's facts renders every node, which calls the
		// rendering provider's guest across the boundary. The grouping itself
		// reaches for nothing further: it evaluates each boundary's own path
		// declaration over values the graph already carries.
		HostPowers: []hostPower{powerGuestCrossing},
	},
	{
		Name:    "rendered",
		Extends: "dispatched",
		Holds:   "artifact bytes and the emitted file set",
		// The bytes are produced by the rendering provider and by the pipeline
		// steps, each of which is a guest the host calls across the boundary.
		HostPowers: []hostPower{powerGuestCrossing},
	},
}

// records declares the composite records composite facts return.
var records = []recordDecl{
	{
		Name: "LeafProvenance",
		Doc: "LeafProvenance is where one leaf of an attachment's configuration " +
			"came from: whether the author wrote it explicitly or an annotation " +
			"supplied it, which annotation, and — when an explicit value took " +
			"precedence over an annotation-supplied one — which annotation it " +
			"displaced.\n\nIt is one fact rather than several because the " +
			"question a check asks is a single one with several coordinates: an " +
			"explicit value silently displacing an annotation is only visible " +
			"from the displaced side and the displacing side together.",
		Fields: []recordField{
			{Name: "Origin", Type: "string", Doc: "Origin is how the leaf's value arrived: authored explicitly, or supplied by an annotation."},
			{Name: "AnnotationKey", Type: "string", Doc: "AnnotationKey is the annotation that supplied the value, empty when the author wrote it explicitly."},
			{Name: "AnnotationSource", Type: "string", Doc: "AnnotationSource names where that annotation was declared."},
			{Name: "DisplacesAnnotation", Type: "bool", Doc: "DisplacesAnnotation reports whether an explicit value took precedence over an annotation-supplied one."},
			{Name: "DisplacedAnnotationKey", Type: "string", Doc: "DisplacedAnnotationKey is the annotation the explicit value displaced, empty when none was displaced."},
			{Name: "DisplacedAnnotationSource", Type: "string", Doc: "DisplacedAnnotationSource names where the displaced annotation was declared."},
		},
	},
	{
		Name: "AttachmentDeclaration",
		Doc: "AttachmentDeclaration is what one Feature attachment declares: the " +
			"Feature type it attaches, and the socket names its own dependency " +
			"block wires on that Feature.\n\nIt is one fact rather than two " +
			"because neither half answers anything alone. A socket name is only " +
			"checkable against the socket set of the Feature the attachment " +
			"names, and the type says nothing about what was wired — so a rule " +
			"asking whether an attachment wires only sockets its Feature " +
			"declares needs both, and two accessors would each return a value " +
			"with nothing to compare it against.",
		Fields: []recordField{
			{Name: "FeatureType", Type: "string", Doc: "FeatureType is the Feature the attachment names, empty when the author named none."},
			{Name: "AuthoredSocketNames", Type: "[]string", Doc: "AuthoredSocketNames are the socket names the attachment's own dependency block wires, in author order."},
		},
	},
	{
		Name: "DerivedNameDeclaration",
		Doc: "DerivedNameDeclaration is one contributor's declaration that it " +
			"derived one name into one of its own key spaces: the space, the " +
			"name, the contributor and what kind of contributor it is, and " +
			"whether a single one of that contributor's declarations carried " +
			"the name more than once within that space.\n\nIt is one fact " +
			"rather than several because no half of it answers anything alone. " +
			"A name without its space says nothing — one name in two spaces has " +
			"not met itself — and a space without its contributors says nothing " +
			"either, since what makes a name a collision is how many " +
			"contributors arrived at it. The repeat travels with the " +
			"declaration it belongs to for the sharper reason: a contributor " +
			"legitimately restates a name across its several render crossings, " +
			"so the accumulated declarations cannot tell that restatement from " +
			"one declaration carrying the name twice. The distinction is drawn " +
			"while a single declaration is still whole and is carried here " +
			"attached to the declaration it was drawn from, which is what keeps " +
			"a reader from having to pair two answers up and stopping halfway.",
		Fields: []recordField{
			{Name: "KeySpace", Type: "string", Doc: "KeySpace is the space the declaring contributor named, an opaque string of its own choosing."},
			{Name: "DerivedName", Type: "string", Doc: "DerivedName is the name derived into that space, an opaque string the contributor derived."},
			{Name: "Contributor", Type: "string", Doc: "Contributor is the authoring-level name of whatever derived the name: a component instance, or a feature instance attached to a boundary."},
			{Name: "ContributorKind", Type: "string", Doc: "ContributorKind is what that contributor is in authoring terms, either a component or a feature, which is what says where its name is written."},
			{Name: "RepeatedWithinOneDeclaration", Type: "bool", Doc: "RepeatedWithinOneDeclaration reports whether a single one of that contributor's declarations carried this name more than once inside this space, which is a failure of that contributor's own derivation rather than a meeting between two of them."},
		},
	},
	{
		Name: "InjectDelivery",
		Doc: "InjectDelivery is one delivery a node's injection pipe carries or " +
			"was asked to carry: the mechanism and the key it is delivered " +
			"under, whatever attachment asked for it, and whether a value " +
			"actually arrived.\n\nIt is one fact rather than several because no " +
			"part of it answers anything alone. A key without its mechanism has " +
			"not met itself — one key delivered through two mechanisms is two " +
			"deliveries — and neither says who asked for it, which is what " +
			"decides whose authoring a duplicate belongs to. Whether the value " +
			"arrived travels with the delivery it belongs to for the sharper " +
			"reason: a delivery whose reference resolved to nothing puts no " +
			"value under the key, so it leaves the key free for a later " +
			"delivery to take. A reader given the asked-for set alone could not " +
			"tell that case from a delivery that landed, and would read a key " +
			"nobody actually delivered as taken.",
		Fields: []recordField{
			{Name: "Mechanism", Type: "string", Doc: "Mechanism is the injection mechanism the key is delivered through."},
			{Name: "DeliveryKey", Type: "string", Doc: "DeliveryKey is the key the value is delivered under, which is what the consumer reads it by."},
			{Name: "Attachment", Type: "string", Doc: "Attachment is the attachment instance whose own injection block asked for the delivery, empty where the component's own block or one of its connections did."},
			{Name: "Delivered", Type: "bool", Doc: "Delivered reports whether a value arrived under the key, which is false for a delivery whose reference resolved to nothing and for one whose key another delivery already holds."},
		},
	},
	{
		Name: "InjectMappingMechanism",
		Doc: "InjectMappingMechanism is one delivery mechanism the host " +
			"component's own injection block declares, as one attachment's " +
			"rendered injectMapping output names it: the mechanism, the " +
			"FeatureRecipe whose mapping was evaluated, and whether that " +
			"output carries a top-level key for the mechanism.\n\nThe two " +
			"sides are one fact because neither answers anything alone. A " +
			"mechanism the component declares is only interesting beside a " +
			"mapping that had the chance to carry it, and a key a mapping " +
			"carries is only interesting beside a component that asked for " +
			"that mechanism — so a reader given either set alone would have " +
			"to fetch the other and pair them, which is the re-derivation " +
			"declaring the fact exists to close.\n\nThe FeatureRecipe " +
			"travels with the pair because the mapping is authored there " +
			"rather than on the attachment that binds it, and the two names " +
			"are independent: an attachment instance is named by whoever " +
			"attached it and the FeatureRecipe by whoever published it. A " +
			"reader holding only the attachment could not name the document " +
			"the mapping is written in.",
		Fields: []recordField{
			{Name: "Mechanism", Type: "string", Doc: "Mechanism is the delivery mechanism the host component's own injection block declares."},
			{Name: "Recipe", Type: "string", Doc: "Recipe is the FeatureRecipe whose injectMapping was evaluated for the attachment, which is the document a missing key is added to."},
			{Name: "Mapped", Type: "bool", Doc: "Mapped reports whether that rendered output carries a top-level key for the mechanism."},
		},
	},
	{
		Name: "ArtifactTemplateDelimiters",
		Doc: "ArtifactTemplateDelimiters is the delimiter pair an artifact " +
			"template is read with: the opening delimiter and the closing " +
			"one.\n\nIt is one fact rather than two because neither half " +
			"answers anything alone. Reading a template needs both delimiters " +
			"together, so a rule holding one of them could not read a template " +
			"at all, and two accessors would each hand back a value with " +
			"nothing to do.",
		Fields: []recordField{
			{Name: "Left", Type: "string", Doc: "Left is the opening delimiter."},
			{Name: "Right", Type: "string", Doc: "Right is the closing delimiter."},
		},
	},
}

// declarationCompelsImplementation is the paragraph a fact carries when its
// accessor is implemented while no declared check reads it. It accounts for the
// accessor's presence and stops short of endorsing it: a fact nothing reads
// remains a candidate for removal, and carrying this paragraph is not an
// exemption from that.
//
// It is one shared paragraph rather than a copy inside each Doc because it says
// the same thing about the view interface every time, and only the facts it is
// appended to differ. Copies that agree when they are written are how they come
// to disagree later. Which facts carry it is therefore readable from the
// references to this constant rather than from a list kept beside them.
const declarationCompelsImplementation = "\n\nDeclaring a fact puts its " +
	"accessor on its stage's view interface, and every later stage carries " +
	"that interface by embedding — so a view presenting any of those stages " +
	"answers the accessor whether or not a check asks it. The declaration " +
	"compels the implementation; nothing compels the declaration. This " +
	"accessor is implemented for that reason rather than because a check " +
	"reads it, which accounts for its presence without warranting it: a fact " +
	"no check reads remains a candidate for removal."

// facts declares every fact the framework exposes.
//
// The set declared here realizes the full derived chain and the full derived
// subject-kind set: every stage is occupied and every subject kind is keyed, so
// both derived sets are complete and a consumer building one traversal per kind
// has every kind in front of it. Further facts sharing an already-realized stage
// and kind are added by the change that needs them; each is a declaration, not
// a design act, so the registry grows one line at a time.
var facts = []factDecl{
	// ---------------------------------------------------------------- document

	{
		Name:        "raw-aggregate-text",
		Stage:       "document",
		SourceField: "resolver.ResolveInputs.AggregateRawBytes",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "[]byte",
		Doc: "RawAggregateText is the Aggregate's bytes as authored, before " +
			"parameter interpolation.\n\nIt is a separate fact from the " +
			"interpolated document rather than the same fact at an earlier " +
			"moment. A reader asking which parameters a template never " +
			"referenced has to see the references the author wrote, which " +
			"interpolation has already consumed; treating the two as one fact " +
			"would make that question unanswerable and would put two stages " +
			"out of order with each other.",
	},
	{
		Name:        "declared-parameter-keys",
		Stage:       "document",
		SourceField: "types.AggregateSpec.Parameters",
		Subject:     []string{"document", "parameter"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "[]string",
		Doc: "DeclaredParameterKeys are the parameter names the Aggregate's " +
			"contract declares, in author order.",
	},
	{
		Name:        "entity-assertion-name-set",
		Stage:       "document",
		SourceField: "types.ComponentSpec.Assertions",
		Subject:     []string{"document", "assertion"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "[]string",
		Doc: "EntityAssertionNameSet are the assertion names an entity declares " +
			"across its resolved inheritance chain, in author order with the " +
			"chain root's first.",
	},
	{
		Name:        "manifest-annotation-binding-keys",
		Stage:       "document",
		SourceField: "types.MappingTier.Annotations",
		Subject:     []string{"document", "annotation-binding"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "[]string",
		Doc: "ManifestAnnotationBindingKeys are the annotation keys the " +
			"presented Manifest's binding registry declares, in author " +
			"order.\n\nAn annotation binding is what lets an operator write a " +
			"single annotation on a component and have it routed into a " +
			"Feature attachment's pillar fields. The key is the annotation " +
			"name the author writes.\n\nThe order is the author's and is " +
			"carried rather than sorted, because a rule invoked once per " +
			"binding raises in invocation order and that order is the order " +
			"the findings reach the reader.\n\nIt counts the bindings the " +
			"registry consumed by annotation expansion is built from. A " +
			"Manifest declares an annotation binding at one site, " +
			"mappings.features.annotations, so this fact's subject and the " +
			"input annotation expansion reads are the same set, and a rule " +
			"invoked over it is asking about the whole of what the compiler " +
			"acts on.",
	},
	{
		Name:        "manifest-annotation-binding-feature",
		Stage:       "document",
		SourceField: "types.AnnotationBinding.Feature",
		Subject:     []string{"document", "annotation-binding"},
		Params: []paramDecl{
			{Name: "document", Type: "string"},
			{Name: "binding", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "ManifestAnnotationBindingFeature is the Feature an annotation " +
			"binding routes to, and whether the presented Manifest declares a " +
			"binding of that key at all.\n\nThe second result is load-bearing " +
			"for the same reason it is on an instance's component type: a " +
			"binding nobody declared and a declared binding naming the empty " +
			"Feature are different findings, and a rule that cannot tell them " +
			"apart reports the wrong one.",
	},

	// -------------------------------------------------------------- normalized

	{
		Name:        "instance-component-type",
		Stage:       "normalized",
		SourceField: "types.ComponentInstance.Type",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "(string, bool)",
		Doc: "InstanceComponentType is the component type a named instance " +
			"declares, and whether an instance of that name exists at all.\n" +
			"\nThe second result is load-bearing: an absent instance and " +
			"an instance declaring the empty type are different findings, and " +
			"a check that cannot tell them apart reports the wrong one.",
	},
	{
		Name:        "socket-required",
		Stage:       "normalized",
		SourceField: "types.UsesSchemaEntry.Required",
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "bool",
		Doc: "SocketRequired reports whether a dependency socket declared on a " +
			"component type must be populated.",
	},
	{
		Name:        "socket-inject-mechanism-names",
		Stage:       "normalized",
		SourceField: "types.UsesSchemaEntry.Inject",
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "[]string",
		Doc: "SocketInjectMechanismNames are the delivery mechanisms a " +
			"dependency socket allows a connection wired into it to dictate, in " +
			"author order.\n\nThe names are an allowlist and nothing more: they " +
			"constrain which mechanisms a consumer may dictate delivery " +
			"through, never what any of them carries. The shape of one delivery " +
			"entry is the entity contract's to state, and restating it here " +
			"would give one rule two homes.\n\nEmpty is a declaration rather " +
			"than an absence, and a rule reading this fact must not take it for " +
			"\"unconstrained\" — it is the most constrained answer a socket can " +
			"give. A socket naming no mechanism permits no dictation at all, so " +
			"a connection dictating any delivery for it is refused. An omitted " +
			"allowlist and an explicitly empty one are deliberately the same " +
			"answer, because reading them apart would give an empty list a " +
			"meaning no authoring surface documents.\n\nWhether the socket " +
			"carries a declaration at all is a different question, and this " +
			"fact does not answer it: a socket the catalog declares no entry " +
			"for reads the same as one declaring no mechanism. A rule that " +
			"turns on the difference reads the declared socket set first, which " +
			"is the fact that answers which sockets exist to be asked about." +
			declarationCompelsImplementation +
			"\n\nThe rule this fact exists to serve is enforced, and not as a " +
			"declared check. A connection dictating a delivery its socket does " +
			"not permit halts the build while the graph is built, in the same " +
			"resolution that settles whether that connection's socket was " +
			"declared at all. What keeps the rule out of a check is the socket " +
			"side rather than the connection side: this fact is keyed by a " +
			"component type and a socket, and that is the whole of its key, " +
			"while the socket a connection an attachment drew is wired into is " +
			"declared on the attached Feature's own dependency sockets and " +
			"never on the consuming component's. So this fact cannot answer " +
			"for such a connection at all, and a check reading it alone would " +
			"approve every attachment-drawn dictation — silently, for " +
			"exactly the connections it could not see.\n\nWhat is missing is " +
			"one companion fact rather than a stage or a subject kind. The " +
			"connection side is declared already — the delivery a " +
			"connection dictates as authored, keyed by the carrier that drew " +
			"it, over every connection the composition drew — so a " +
			"Feature-side counterpart to this fact, over the socket " +
			"declarations a Feature makes, is the whole of what a faithful " +
			"check still wants. Until that exists this fact has a reader it " +
			"lacks rather than a reader it is not owed, which is why it is not " +
			"the removal candidate a fact nothing reads would otherwise be.",
	},
	{
		Name:        "per-edge-target",
		Stage:       "normalized",
		SourceField: "types.VerboseUsesEntry.Target",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "string",
		Doc: "PerEdgeTarget is the target one connection names, as authored — " +
			"empty when the author named none.\n\nThe emptiness is reported " +
			"rather than resolved. Two rules in the tree read the same empty " +
			"target and mean different things by it, one taking it as naming " +
			"the connection's own alias and the other as declining to create an " +
			"edge; a fact that silently applied either rule would make one of " +
			"them wrong somewhere else.",
	},
	{
		Name:        "per-edge-values",
		Stage:       "normalized",
		SourceField: "types.VerboseUsesEntry.Values",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "types.OrderedMap[any]",
		Doc: "PerEdgeValues is the developer's configuration for one connection " +
			"on one socket as it was written, carried as an opaque ordered " +
			"payload.\n\nIt is the payload rather than a named fact about it, " +
			"on the same reasoning the configuration pillars are: a rule whose " +
			"predicate is \"what was written here conforms to the contract the " +
			"socket declares\" has that content as its input, and the content " +
			"has no other home to be read from.\n\nEmpty is the author writing " +
			"nothing, and it is NOT a case with nothing to check. Once a socket " +
			"declares a contract for this pillar, every connection on that " +
			"socket is measured against it, and a connection that wrote nothing " +
			"is the maximal violation of a key the contract declares required " +
			"rather than an exemption from it. What is opt-in is the socket's " +
			"contract, never the connection's content.\n\nThe operator's " +
			"per-connection configuration is a different fact on a different " +
			"surface rather than this one in another shape. The two are written " +
			"on separate documents by separate hands, each is independently " +
			"absent, and the socket declares a separate contract for each.\n" +
			"\nAuthor order is carried because these entries reach rendered " +
			"output, and an unordered payload would put a byte-identical result " +
			"at the mercy of map iteration.",
	},
	{
		Name:        "authored-per-edge-inject",
		Stage:       "normalized",
		SourceField: "types.VerboseUsesEntry.Inject",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "types.OrderedMap[any]",
		Doc: "AuthoredPerEdgeInject is the delivery a connection dictates as it " +
			"was written, keyed by mechanism name in author order, each entry " +
			"carrying that mechanism's payload as authored.\n\nA mechanism " +
			"counts as dictated when its key is PRESENT, whatever its payload " +
			"turns out to hold. That is the property a rule over this fact " +
			"depends on: the conversions further down the build drop a payload " +
			"they cannot express as one string per delivery key, so a rule " +
			"reading mechanism names off a converted structure would miss a " +
			"dictation whose payload was dropped and read the connection as " +
			"dictating nothing.\n\nThe payload is carried rather than " +
			"decomposed because the shape of one delivery entry is the entity " +
			"contract's to state. A rule needing the keys a mechanism delivers, " +
			"or the provides key an entry references, reads them out of this " +
			"payload with the same reading the authoring surface documents.\n" +
			"\nAn omitted delivery block and an empty one are one answer: the " +
			"connection dictates nothing, which is what a socket declaring no " +
			"allowlist requires of it.\n\nWhat the dictated mechanisms are " +
			"measured against is a separate fact on a separate document — the " +
			"allowlist the socket declares — so a rule reads the two together " +
			"and neither carries the other.",
	},
	{
		Name:        "compact-socket-targets",
		Stage:       "normalized",
		SourceField: "types.UsesSocketValue.Compact",
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "[]string",
		Doc: "CompactSocketTargets are the target names a socket lists in the " +
			"short authoring form, in author order.\n\nThe short form names " +
			"targets directly and gives each connection the target's own name " +
			"as its alias; the long form names an alias per connection and " +
			"carries the target inside it. Exactly one of the two is populated " +
			"for any socket the author wrote a value for, so this fact reads " +
			"empty for a socket authored in the long form and for a socket with " +
			"no value at all — the connections of a long-form socket are read " +
			"through the facts keyed per connection instead.\n\nA name in this " +
			"list is what the author typed and is not resolved against anything " +
			"here. An entry may be the empty string, which names no target and " +
			"populates nothing, so a rule asking whether a socket was populated " +
			"cannot read the list's length as its answer.",
	},
	{
		Name:        "authored-uses-socket-connections",
		Stage:       "normalized",
		SourceField: "types.ComponentInstance.Uses",
		Subject:     []string{"node", "socket"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "types.OrderedMap[int]",
		Doc: "AuthoredUsesSocketConnections are the connections an instance's own " +
			"dependency block carries, one entry per socket the instance wrote, in " +
			"author order, each holding that socket's connection count.\n\nIt is " +
			"what answers whether a socket is populated. A rule over a socket that " +
			"must carry a connection is asked about every socket the component type " +
			"declares, including the ones the instance wrote nothing for — which are " +
			"the ones it exists to catch — so the answer has to be in hand for a " +
			"socket that has no connection to read anything from. A fact keyed per " +
			"connection cannot answer there: keying one takes an alias, and only an " +
			"existing connection supplies an alias.\n\nThe " +
			"count is the fact rather than a yes-or-no answer, and that is what holds " +
			"the two emptinesses apart. A socket absent from this map is one the " +
			"instance named nowhere; a socket present with a count of zero is one it " +
			"named and left carrying nothing. Neither is populated, so a rule that " +
			"refuses an unpopulated socket treats them alike — but a rule telling an " +
			"author what to change names a different correction for each, and an " +
			"answer narrowed to yes-or-no would have collapsed them before either " +
			"rule could ask. The count also answers HOW MANY, which is what a rule " +
			"over a socket admitting a single connection needs and nothing else " +
			"supplies. A reader narrowing this result to a boolean removes both of " +
			"those and can restore neither.\n\nWhat counts as one connection " +
			"follows the authoring form, and the two forms differ in a way a rule " +
			"must not smooth over. The short form lists targets, so the target IS the " +
			"connection and an empty entry names nothing and counts nothing. The long " +
			"form names a connection per alias and carries the target inside it, so " +
			"an entry counts whether or not it names a target: an omitted target " +
			"falls back to the entry's own alias, so the connection is drawn either " +
			"way, and a rule that read the target and took emptiness for absence " +
			"would refuse a socket that IS populated.\n\nThat rule is this " +
			"surface's and does not carry to the deployment's edge surface, whose " +
			"own connection count is a separate fact with the same shape and the " +
			"opposite rule — so a reader unifying the two makes one of them wrong. " +
			"There an entry counts only when it names a target, because an omitted " +
			"target falls back to nothing: on that surface the alias identifies " +
			"which existing connection the entry configures rather than supplying " +
			"a target for a new one, so an entry naming no target draws nothing. " +
			"One authored shape, two meanings, because the alias is doing a " +
			"different job on each surface.\n\nIt reads the instance's own " +
			"dependency block " +
			"and no other surface. An attachment's block declares sockets on the " +
			"Feature it attaches, and a deployment's own edge surface wires sockets " +
			"from a separate document; each is a separate fact because each is " +
			"separately authored and independently absent. A rule asking whether a " +
			"socket is populated across every surface that can populate one reads " +
			"them together, and none of them carries another.\n\nIt counts " +
			"connections and never entries. Which aliases the block wrote an " +
			"entry under is the separate fact declared beside it, and the two " +
			"answer differently for the same authored document.",
	},
	{
		Name:        "authored-uses-socket-entry-aliases",
		Stage:       "normalized",
		SourceField: "types.ComponentInstance.Uses",
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "[]string",
		Doc: "AuthoredUsesSocketEntryAliases are the aliases an instance's own " +
			"dependency block wrote an entry under on one socket, in author " +
			"order.\n\nIt reports authorship and nothing else, and an entry " +
			"appears here whether or not it carries any content. That rule is " +
			"stated once and holds for every per-edge surface, which is the whole " +
			"difference between this fact and the connection count declared " +
			"beside it: the count asks which entries draw a connection, a " +
			"question each surface answers by its own opposite rule, while this " +
			"asks which entries the author wrote at all, which every surface " +
			"answers the same way. So the length of this list is not a connection " +
			"count, and the count is not a record of what was written. An empty " +
			"result says the block wrote no entry under that socket, and does " +
			"not separate a socket it named and left empty from one it named " +
			"nowhere — the count holds that distinction deliberately and is " +
			"where it is read.\n\n" +
			"Authorship is reported positively rather than inferred, and that is " +
			"why the fact exists. Every fact keyed per connection on this surface " +
			"reads its zero value twice over — once for an alias the block wrote " +
			"nothing at, and once for an entry the block wrote and left empty — " +
			"so a rule reading content cannot tell those apart, and they need " +
			"opposite treatment: an entry that writes nothing is the maximal " +
			"violation of a key the socket's contract declares required, while a " +
			"connection this block never wrote is not this surface's to judge. " +
			"Inferring authorship from what no other surface claims is the " +
			"alternative and it is weaker: it is only ever as complete as the set " +
			"of surfaces that can draw a connection, and a connection a Feature " +
			"contributes is reported at a later stage than this one, so a rule " +
			"here cannot see it at all.\n\nIt is keyed per socket and hands back " +
			"the set rather than answering per connection, because the entries an " +
			"author wrote and the connections a build draws are not the same set. " +
			"A connection may be drawn by a surface that wrote nothing here, and " +
			"an entry may name no connection at all. A fact keyed per connection " +
			"is unreadable in the second case: keying one takes an alias, and " +
			"only an existing connection supplies one. The set also enumerates, " +
			"so a rule over one socket visits exactly the entries the author " +
			"wrote. A reader narrowing this to a per-connection answer, or to a " +
			"count, removes both of those and can restore neither.\n\nBoth " +
			"authoring forms are read, because the contract the socket declares " +
			"is the same one whichever the author chose. The long form names an " +
			"alias per connection, and the alias is the key the author wrote; the " +
			"short form lists targets and gives each connection the target's own " +
			"name as its alias, so a short-form socket's entries appear here " +
			"under those names. A short-form item may be the empty string, which " +
			"names no target and populates nothing, and it appears here as an " +
			"empty alias because the author wrote an entry there.\n\nIt reads " +
			"the instance's own dependency block and no other surface. A " +
			"deployment's own edge surface is a separate fact with the same shape " +
			"and the same authorship rule, because it is separately authored and " +
			"independently absent, and neither carries the other.",
	},
	{
		Name:        "authored-attachment-names",
		Stage:       "normalized",
		SourceField: "types.ComponentInstance.Features",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "AuthoredAttachmentNames are the attachment names an instance " +
			"declares, in author order.",
	},
	{
		Name:        "authored-attachment-type",
		Stage:       "normalized",
		SourceField: "types.FeatureInstance.Type",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "AuthoredAttachmentType is the Feature type an application declares " +
			"for one attachment on one component, reported absent when the " +
			"application declares no attachment under that name for it.\n\nIt is " +
			"a separate fact from the type a Release declares for the same " +
			"attachment name, and the pair is what makes the agreement question " +
			"askable at all. Both documents may name one attachment — this one " +
			"carries the application's own configuration, the Release's carries " +
			"the physical sizing — and the two are merged into one attachment " +
			"further down, which only means anything if they name the same " +
			"Feature. A rule reading one surface alone cannot see a " +
			"disagreement.\n\nThe absence is reported rather than resolved, and " +
			"it carries the same distinction its Release-side counterpart does: " +
			"an attachment only one document declares is nothing to reconcile, " +
			"and an attachment naming no type states nothing to disagree with.\n" +
			"\nIt is also a separate fact from the sockets the same attachment " +
			"wires, which is keyed per socket as well: the type names what is " +
			"attached and the socket names are what that Feature is wired on, so " +
			"they are asked a different number of times.",
	},
	{
		Name:        "attachment-authored-uses-socket-names",
		Stage:       "normalized",
		SourceField: "types.FeatureInstance.Uses",
		Subject:     []string{"node", "attachment", "socket"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "[]string",
		Doc: "AttachmentAuthoredUsesSocketNames are the socket names an " +
			"attachment's own dependency block wires, in author order.\n" +
			"\nThese name sockets on the attached Feature, a separate " +
			"declaration surface from the component's own sockets, which is why " +
			"the fact is keyed per attachment as well as per node.",
	},
	{
		Name:        "subsystem-attachment-declaration",
		Stage:       "normalized",
		SourceField: "types.Subsystem.Features",
		Subject:     []string{"subsystem", "attachment"},
		Params: []paramDecl{
			{Name: "subsystem", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "(AttachmentDeclaration, bool)",
		Doc: "SubsystemAttachmentDeclaration is what an attachment declared on a " +
			"subsystem itself states — the Feature it attaches and the sockets it " +
			"wires on that Feature — reported absent when the subsystem declares " +
			"no attachment under that name.\n\nIt is keyed by the subsystem " +
			"rather than by a component, and that is the whole reason it is a " +
			"fact of its own. An attachment written at a subsystem applies to " +
			"every component that scope holds, so it is authored once and " +
			"contributes many times; a fact keyed by a component would be asked " +
			"once per component and never at all for a subsystem holding none — " +
			"which is silence indistinguishable from approval. Keyed here, it is " +
			"asked exactly once per attachment the author wrote, and a rule over " +
			"it can name that site.\n\nThe subsystem is identified by its path " +
			"from the document's own scope set rather than by a bare name, " +
			"because subsystems nest and a name alone does not identify one: two " +
			"scopes at different depths may carry the same name, and a key that " +
			"could not tell them apart would answer for whichever was asked " +
			"last.\n\nThe absence is reported rather than resolved because an " +
			"attachment naming no Feature type is something the authoring " +
			"surface accepts, so an empty type and no attachment at all are " +
			"different answers that a zero value alone would merge.\n\nIt is a " +
			"separate fact from the attachment a boundary declares, which is " +
			"keyed by that boundary: the two are written on separate documents " +
			"by separate hands, either can be absent without the other, and a " +
			"rule over either names a different authored site.\n" +
			"\nWhat the sockets are checked against is a separate fact on a " +
			"separate document: the socket set the attached Feature declares.",
	},
	{
		Name:        "slot-required",
		Stage:       "normalized",
		SourceField: "types.AttachmentSlot.Required",
		Subject:     []string{"node", "slot"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "slot", Type: "string"},
		},
		Returns: "bool",
		Doc: "SlotRequired reports whether an attachment slot declared on a " +
			"component type must be filled.\n\nA slot is a binding point " +
			"and not a dependency socket: the two are separate authored " +
			"surfaces with separate rules over them, so they are separate " +
			"facts and separate subjects.",
	},
	{
		Name:        "authored-inject-block-present",
		Stage:       "normalized",
		SourceField: "types.ComponentInstance.Inject",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "bool",
		Doc: "AuthoredInjectBlockPresent reports whether an instance wrote an " +
			"injection block at all.\n\nIt reports authorship and nothing else, " +
			"and a block appears here whether or not it carries any delivery. " +
			"So it is not a count of what the block dictates and it does not " +
			"become false when the block dictates nothing: it separates a block " +
			"the author wrote from a block the author omitted, which is the one " +
			"question every fact over this surface reads its zero value " +
			"for.\n\nAuthorship is reported positively rather than inferred, and " +
			"that is why the fact exists. The mechanisms an instance declares are " +
			"empty for a block written and left empty exactly as they are for no " +
			"block at all, so a rule reading them cannot tell those apart while " +
			"they need opposite verdicts: writing an empty block on a component " +
			"type that reserves delivery to its Recipe dictates delivery the type " +
			"permits none of, and writing no block dictates nothing and is what " +
			"most components ship. Inferring authorship from emptiness is the " +
			"alternative and it is not available here at all — emptiness is the " +
			"value both cases produce, so the inference has nothing to read.\n\n" +
			"It reads the instance's own injection block and no other surface. " +
			"Delivery dictated on a connection is separately authored and " +
			"independently absent, and neither carries the other.",
	},
	{
		Name:        "authored-inject-mechanism-names",
		Stage:       "normalized",
		SourceField: "types.ComponentInstance.Inject",
		Subject:     []string{"node", "mechanism"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "AuthoredInjectMechanismNames are the delivery mechanisms an " +
			"instance's injection block declares, in author order.",
	},
	{
		Name:        "authored-inject-delivery-keys",
		Stage:       "normalized",
		SourceField: "types.ComponentInstance.Inject",
		Subject:     []string{"node", "mechanism", "delivery-key"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "mechanism", Type: "string"},
		},
		Returns: "[]string",
		Doc: "AuthoredInjectDeliveryKeys are the keys an instance's injection " +
			"block delivers through one mechanism, in author order where the " +
			"authored shape carries one and alphabetically where it does not.",
	},
	{
		Name:        "authored-inject-provides-reference",
		Stage:       "normalized",
		SourceField: "types.ComponentInstance.Inject",
		Subject:     []string{"node", "mechanism", "delivery-key"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "mechanism", Type: "string"},
			{Name: "deliveryKey", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "AuthoredInjectProvidesReference is the published key one delivery " +
			"entry of an instance's injection block reads from, and whether " +
			"that entry reads a published key at all.\n\nThe second answer is " +
			"the fact's point rather than a convenience. An entry may carry a " +
			"literal value instead of a reference, and an entry carrying a " +
			"literal is nothing to resolve — so a rule checking that every " +
			"reference names a key the producing component publishes must be " +
			"able to tell a literal from a reference to the empty key. A single " +
			"string would merge the two, and the rule would report the entry " +
			"that referenced nothing rather than the one that referenced " +
			"wrongly.\n\nThe key is what the author typed and is not resolved " +
			"here. Whether it is published is a separate fact on the producing " +
			"component's own surface, so a rule reads the two together and " +
			"neither carries the other.\n\nThis is the instance's own injection " +
			"block. A reference written on a connection is the same question on " +
			"a different surface, read out of that connection's own authored " +
			"delivery payload, and the two are independently absent.",
	},
	{
		Name:        "authored-values-pillar",
		Stage:       "normalized",
		SourceField: "types.ComponentInstance.Values",
		Subject:     []string{"node", "pillar", "leaf"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "pillar", Type: "string"},
		},
		Returns: "types.OrderedMap[any]",
		Doc: "AuthoredValuesPillar is one configuration pillar as the author " +
			"wrote it, carried as an opaque ordered payload.\n\nThe pillar is a " +
			"fact like any other. A rule whose predicate is \"what the author " +
			"wrote here conforms to this contract\" has the pillar's content as " +
			"its input, and that content has no other home to be read from, so " +
			"receiving a whole pillar is not a rule re-deriving something already " +
			"declared elsewhere.\n\nWhat a reader has to be careful of is the " +
			"stage it reads at. The entries are whatever the author wrote, and one " +
			"of them may be a template expression whose resolved form is a fact of " +
			"a later stage — so interpreting it here, where the text is still as " +
			"authored, can yield an answer the renderer's would differ from. The " +
			"care belongs to what a rule does with the payload rather than to the " +
			"payload's type.",
	},
	{
		Name:        "authored-attachment-block",
		Stage:       "normalized",
		SourceField: "types.ComponentInstance.Features",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "types.OrderedMap[types.FeatureInstance]",
		Doc: "AuthoredAttachmentBlock is an instance's whole authored " +
			"attachment block.\n\nA rule whose predicate is \"this " +
			"authored sub-document conforms to this contract\" reads the " +
			"sub-document rather than a named fact about it, and this is the " +
			"one such rule whose sub-document is typed rather than opaque.",
		ReportedException: "the return shape exposes an authored entity type, " +
			"so a reader of this fact can reach authoring detail the fact does " +
			"not name; the framework reports the fact rather than admitting it " +
			"silently, and the shape is open rather than settled",
	},
	{
		Name:        "socket-selector",
		Stage:       "normalized",
		SourceField: "types.UsesSchemaEntry.Selector",
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "string",
		Doc: "SocketSelector is the label constraint a component type's " +
			"dependency socket declares on what it may be pointed at, empty " +
			"when it constrains nothing.\n\nEmptiness is one answer rather " +
			"than two. A socket declared without a constraint and a socket the " +
			"catalog declares no entry for both leave the target unconstrained, " +
			"and the question a rule over this fact asks is whether there is a " +
			"constraint to evaluate at all. A fact that distinguished the two " +
			"would put a second condition on one question, and the two could " +
			"then disagree.",
	},
	{
		Name:        "attachment-socket-selector",
		Stage:       "normalized",
		SourceField: "types.UsesSchemaEntry.Selector",
		Subject:     []string{"node", "attachment", "socket"},
		Params: []paramDecl{
			{Name: "featureType", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "string",
		Doc: "AttachmentSocketSelector is the label constraint declared on a " +
			"socket of the Feature an attachment resolves to, empty when it " +
			"constrains nothing.\n\nIt is a separate fact from the consuming " +
			"component's own socket constraint, and keyed per attachment, " +
			"because the two read different declaration surfaces: an edge drawn " +
			"from an attachment's own dependency block participates in the " +
			"attached Feature's socket declarations rather than in the " +
			"component's. One fact spanning both would have to pick a surface, " +
			"and would answer for the wrong one wherever the other applied.",
	},
	{
		Name:        "slot-selector",
		Stage:       "normalized",
		SourceField: "types.AttachmentSlot.Selector",
		Subject:     []string{"node", "slot"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "slot", Type: "string"},
		},
		Returns: "string",
		Doc: "SlotSelector is the label constraint an attachment slot declares " +
			"on which Feature may bind it, empty when it accepts any fill.\n" +
			"\nA slot is a binding point and not a dependency socket, so " +
			"this is a separate fact from a socket's constraint even though both " +
			"are written in one grammar: they are declared on separate authored " +
			"surfaces and evaluated against different label sets — a slot's " +
			"against the labels of the Feature filling it, a socket's against " +
			"the labels of the node an edge points at.",
	},
	{
		Name:        "slot-inject-mechanism-names",
		Stage:       "normalized",
		SourceField: "types.AttachmentSlot.Inject",
		Subject:     []string{"node", "slot"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "slot", Type: "string"},
		},
		Returns: "[]string",
		Doc: "SlotInjectMechanismNames are the delivery mechanisms an attachment " +
			"slot allows for the material bound to it, in author order.\n\nThe " +
			"names are an allowlist and nothing more: they constrain which " +
			"mechanisms a fill may dictate delivery through, never what any of " +
			"them carries. The shape of one delivery entry is the entity " +
			"contract's to state, and restating it here would give one rule two " +
			"homes.\n\nEmpty is a declaration rather than an absence, and a rule " +
			"reading this fact must not take it for \"unconstrained\" — it is the " +
			"most constrained answer a slot can give. A slot naming no mechanism " +
			"states that its material is consumed by the Recipe internally rather " +
			"than delivered to the workload, so a fill dictating any delivery for " +
			"it is refused.",
	},
	{
		Name:        "feature-applies-to",
		Stage:       "normalized",
		SourceField: "types.FeatureSpec.AppliesTo",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "featureType", Type: "string"}},
		Returns:     "string",
		Doc: "FeatureAppliesTo is the single label constraint a Feature declares " +
			"on which components it may attach to, empty when it constrains " +
			"nothing.\n\nThe requirements written inside it all hold " +
			"together, so the whole of it is one constraint and one fact.",
	},
	{
		Name:        "feature-applies-to-any-of",
		Stage:       "normalized",
		SourceField: "types.FeatureSpec.AppliesToAnyOf",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "featureType", Type: "string"}},
		Returns:     "[]types.SelectorTerm",
		Doc: "FeatureAppliesToAnyOf are the alternative label constraints a " +
			"Feature declares, of which any one holding is enough, in author " +
			"order.\n\nIt is a separate fact from the single constraint " +
			"rather than the same fact in another shape. The two match by " +
			"different rules — the requirements inside the single one hold " +
			"together, whereas these hold alternatively — and each is " +
			"independently absent, so a rule may legitimately read either " +
			"without the other. Each item carries one of two authored forms, a " +
			"constraint string or a flat set of pairs matched by equality, and " +
			"which form an item carries is part of the answer.",
		ReportedException: "the return shape exposes an authored entity type, " +
			"so a reader of this fact can reach the authored item shape the fact " +
			"does not name, including the method reporting which of the two " +
			"forms an item carries; the framework reports the fact rather than " +
			"admitting it silently, and the shape is open rather than settled",
	},
	{
		Name:        "attachment-feature-labels",
		Stage:       "normalized",
		SourceField: "types.FeatureMetadata.Labels",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "featureType", Type: "string"}},
		Returns:     "types.OrderedMap[string]",
		Doc: "AttachmentFeatureLabels is the label set the catalog entity an " +
			"attachment resolves to declares, in author order.\n\nThese " +
			"are the attached Feature's own labels and not those of the node " +
			"carrying it: a rule asking whether a fill satisfies the constraint " +
			"on the place it binds evaluates that constraint against the fill's " +
			"labels. The labels are complete as soon as the catalog is " +
			"reachable, so the fact is placed there even though the finalised " +
			"graph carries a projection of the same labels later; a fact is " +
			"placed at the earliest stage at which it is complete.",
	},
	{
		Name:        "required-attachment-mandates",
		Stage:       "normalized",
		SourceField: "types.RequiredAttachmentTypes(annotations)",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "componentType", Type: "string"}},
		Returns:     "[]types.RequiredAttachmentType",
		Doc: "RequiredAttachmentMandates are the presence mandates a component " +
			"type declares through its annotations, in author order — each " +
			"carrying the annotation key as written, the label naming the " +
			"requirement, the Feature type at least one attachment must carry, " +
			"and the reason the mandate cannot be evaluated when its " +
			"declaration is broken.\n\nThe broken-declaration reason " +
			"travels with the requirement rather than arriving as a fact of its " +
			"own, and that is why this one is composite: a mandate whose " +
			"declaration cannot be evaluated must never read as a satisfied " +
			"one, so the reason has to be in hand at the moment the requirement " +
			"would otherwise be tested.\n\nA mandate is not a binding " +
			"point. It requires that some attachment of a named type be present " +
			"under any name, which is a different question from a named slot " +
			"being bound, and the two are separate facts because they are " +
			"separate questions.",
	},
	{
		Name:        "declared-socket-names",
		Stage:       "normalized",
		SourceField: "types.ComponentSpec.UsesSchema",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "componentType", Type: "string"}},
		Returns:     "[]string",
		Doc: "DeclaredSocketNames are the dependency socket names a component " +
			"type declares, in author order.\n\nIt answers which sockets exist " +
			"to be asked about, which a rule needs in hand before it can read " +
			"any single socket's own facts — whether that socket must be " +
			"populated, and what it constrains its target to. Neither of those " +
			"names the set, so the set is a fact of its own rather than a shape " +
			"of either.\n\nAuthor order is the fact rather than an incidental " +
			"property of it: a rule reporting one finding per socket emits them " +
			"in this order, so the order is what makes the report the same on " +
			"every run.\n\nThe set is what the type DECLARES and never what a " +
			"deployment populates. A component's own dependency block may name a " +
			"socket the type declares no entry for, so a rule reading this set " +
			"as the permitted names would refuse authoring the product accepts; " +
			"what it answers is which sockets carry a declaration to read.",
	},
	{
		Name:        "declared-inject-mechanism-names",
		Stage:       "normalized",
		SourceField: "types.ComponentSpec.Inject",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "componentType", Type: "string"}},
		Returns:     "[]string",
		Doc: "DeclaredInjectMechanismNames are the delivery mechanisms a " +
			"component type allows an instance of it to dictate for its own " +
			"configuration, in author order.\n\nEmpty is a declaration rather " +
			"than an absence, and a rule reading this fact must not take it for " +
			"\"unconstrained\" — it is the most constrained answer a component " +
			"type can give. A type naming no mechanism states that its Recipe " +
			"wires its material internally rather than delivering it to the " +
			"workload, so an instance dictating any delivery of its own is " +
			"refused outright, before any single mechanism is compared against " +
			"anything.\n\nIt governs the type's own configuration. The " +
			"allowlist a dependency socket declares is the same question on a " +
			"different surface — what a connection into that socket may dictate " +
			"— and the two are declared separately and are independently " +
			"absent, so a rule over either names a different authored site.",
	},
	{
		Name:        "attachment-declared-socket-names",
		Stage:       "normalized",
		SourceField: "types.FeatureSpec.UsesSchema",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "featureType", Type: "string"}},
		Returns:     "[]string",
		Doc: "AttachmentDeclaredSocketNames are the dependency socket names the " +
			"Feature an attachment resolves to declares, in author order.\n" +
			"\nIt is a separate fact from the sockets the consuming component " +
			"type declares, because the two are separate declaration surfaces: " +
			"an edge drawn from an attachment's own dependency block " +
			"participates in the attached Feature's socket declarations rather " +
			"than in the component's. One fact spanning both would have to pick " +
			"a surface, and would answer for the wrong one wherever the other " +
			"applied.\n\nIt is keyed per attachment as well as per node although " +
			"the answer follows from the attachment's type alone. Nothing on the " +
			"attachment surface can widen the set — an attachment carries a " +
			"type, a description, and its own dependency, injection and " +
			"configuration blocks, and no further key is accepted there — so the " +
			"repetition across two attachments of one type is recomputation " +
			"rather than storage. What makes the keying right is the finding: a " +
			"rule refusing a socket name has to name the attachment that wrote " +
			"it, which is what an author can act on, where the type alone would " +
			"leave them looking for which attachment meant it.",
	},
	{
		Name:        "declared-slot-names",
		Stage:       "normalized",
		SourceField: "types.ComponentSpec.FeaturesSchema",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "componentType", Type: "string"}},
		Returns:     "[]string",
		Doc: "DeclaredSlotNames are the attachment slot names a component type " +
			"declares, in author order.\n\nA slot is a binding point and not a " +
			"dependency socket, so this is a separate fact from the declared " +
			"socket names even though both are name sets over one entity: they " +
			"are separate authored surfaces with separate rules over them.\n" +
			"\nDeclaring a slot does not close the attachment surface. A " +
			"deployment may attach a Feature under a name no slot declares, so a " +
			"rule reading this set as the permitted attachment names would " +
			"refuse a shape the product supports; what it answers is which names " +
			"carry a slot declaration for a fill to be checked against.\n\nIt is " +
			"keyed per attachment as well as per node because a rule over it is " +
			"asked of an attachment: the question is whether the name this " +
			"attachment binds is one the type declares, and the answer has to " +
			"name that attachment.",
	},
	{
		Name:        "catalog-component-present",
		Stage:       "normalized",
		SourceField: "resolver.CatalogLookup.Component(componentType)",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "componentType", Type: "string"}},
		Returns:     "bool",
		Doc: "CatalogComponentPresent reports whether the catalog this build " +
			"resolves against holds a Component entry for a component type.\n" +
			"\nIt is the question every rule over what a component type declares " +
			"has to settle first, and no other fact settles it. A name set the type " +
			"declares comes back empty both for a type that declares nothing and " +
			"for a type no catalog entry was found for; a resolved contract comes " +
			"back absent for either of those and for a contract that failed to " +
			"resolve. So a rule taking one of those emptinesses for an answer about " +
			"the catalog reports on a type it never found.\n\nFalse is not on its " +
			"own a finding, and a rule that halts on it alone refuses authoring the " +
			"product accepts. A type addressed as a local path has no catalog entry " +
			"to find, so what a false answer MEANS depends on how the reference " +
			"addresses its entity — which is a separate fact, because the two are " +
			"independent: one is a property of the reference as authored and the " +
			"other of what the catalog holds.\n\nIt answers presence in the catalog " +
			"and nothing about whether there is a catalog to consult. A build that " +
			"resolves against none has no entry set to ask about, which is a " +
			"condition settled where the view is produced rather than reported once " +
			"per node.",
	},
	{
		Name:        "catalog-feature-present",
		Stage:       "normalized",
		SourceField: "resolver.CatalogLookup.Feature(featureType)",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "featureType", Type: "string"}},
		Returns:     "bool",
		Doc: "CatalogFeaturePresent reports whether the catalog this build resolves " +
			"against holds a Feature entry for the type an attachment names.\n" +
			"\nIt is a separate fact from whether a Component entry is held, " +
			"because the two are separate catalog kinds reached through separate " +
			"lookups. One fact spanning both would have to pick a kind, and would " +
			"answer for the wrong one wherever the other applied.\n\nIt is keyed " +
			"per attachment as well as per node although the answer follows from " +
			"the attachment's type alone, for the reason the sockets that type " +
			"declares are: nothing on the attachment surface can change the answer, " +
			"so the repetition across two attachments of one type is recomputation " +
			"rather than storage, and a rule over it is asked of an attachment.\n" +
			"\nIt is what the socket set the attached Feature declares cannot " +
			"answer. That set comes back empty both for a Feature declaring no " +
			"socket and for a type no catalog entry was found for, so a rule that " +
			"turns on the difference settles this one first.\n\nIt answers presence " +
			"in the catalog and nothing about whether there is a catalog to " +
			"consult, which is settled where the view is produced.",
	},
	{
		Name:        "component-type-is-local-path",
		Stage:       "normalized",
		SourceField: "resolver.IsRelativePathRef(componentType)",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "componentType", Type: "string"}},
		Returns:     "bool",
		Doc: "ComponentTypeIsLocalPath reports whether a component type reference " +
			"addresses a file beside the authoring document rather than an entity a " +
			"catalog holds — the explicit relative-path prefix entity resolution " +
			"treats as a local path.\n\nIt is that discriminator and not a test " +
			"over the reference's content. The prefix is what separates a local " +
			"path from an identity, and a rule inspecting the string for anything " +
			"else would put a second, differently-behaving version of the rule " +
			"beside the one resolution applies.\n\nIt is what makes a missing " +
			"catalog entry readable. A type addressed as a local path has no entry " +
			"to find, so its absence is the expected outcome and a rule correctly " +
			"passes over it; a type addressed as an identity that no entry was " +
			"found for is a divergence between what the build resolved and what a " +
			"rule can see, which is a finding. A rule reading presence without this " +
			"fact cannot tell those apart, and reports whichever one it assumed.\n" +
			"\nIt is a property of the reference as authored and says nothing about " +
			"whether the file it addresses exists. Whether an entry was found is " +
			"the separate fact that answers that, and the two are asked together.",
	},
	{
		Name:        "release-per-edge-target",
		Stage:       "normalized",
		SourceField: "types.ReleaseUsesEntry.Target",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "string",
		Doc: "ReleasePerEdgeTarget is the target a Release's per-edge entry names " +
			"for one connection, as authored — empty when the Release " +
			"named none.\n\nIt is a separate fact from the target the Aggregate " +
			"authored, and the pair is what a rule comparing the two needs. A " +
			"deployment document may not redirect a connection the architecture " +
			"already drew, and the conflict is precisely a difference between the " +
			"two targets — neither one alone shows it, so a rule reading either " +
			"surface without the other cannot tell a redirected connection from a " +
			"confirmed one.\n\nWhether a socket carries a connection at all is a " +
			"different question, and the per-socket connection counts each " +
			"document contributes are what answer it. A rule over a socket that " +
			"must carry a connection reads those and not this: this fact is keyed " +
			"by one connection, so it is silent about every socket no entry " +
			"named, which are the sockets such a rule exists for.\n\nThe " +
			"emptiness is reported rather than resolved, and here " +
			"it carries the distinction the operator surface rests on: an entry " +
			"naming a target creates the edge, while an entry naming none " +
			"configures an edge some other document already drew and so binds " +
			"nothing.\n\nThe fact is placed where both documents are in view. It " +
			"is keyed by an edge, one element of whose identity is a node the " +
			"other document declares, and a stage holding one authored document " +
			"at a time cannot answer for a pair." +
			declarationCompelsImplementation,
	},
	{
		Name:        "release-per-edge-infra",
		Stage:       "normalized",
		SourceField: "types.ReleaseUsesEntry.Infra",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "types.OrderedMap[any]",
		Doc: "ReleasePerEdgeInfra is the operator's configuration for one " +
			"connection as it was written, carried as an opaque ordered " +
			"payload.\n\nIt is the payload rather than a named fact about it, on " +
			"the same reasoning the authored configuration pillars are: a rule " +
			"whose predicate is \"what was written here conforms to the contract " +
			"the socket declares\" has that content as its input, and the " +
			"content has no other home to be read from.\n\nThe developer's " +
			"per-edge configuration is a different fact on a different surface " +
			"rather than this one in another shape. The two are written on " +
			"separate documents by separate hands, each is independently absent, " +
			"and the socket declares a separate contract for each — so a rule " +
			"may legitimately read either without the other.\n\nAuthor order is " +
			"carried because these entries reach rendered output, and an " +
			"unordered payload would put a byte-identical result at the mercy of " +
			"map iteration.\n\nIt is placed where both documents are in view, " +
			"for the same reason the target the same entry names is: the edge it " +
			"is keyed by is identified in part by a node the other document " +
			"declares.",
	},
	{
		Name:        "release-authored-uses-socket-connections",
		Stage:       "normalized",
		SourceField: "types.BoundaryComponent.Uses",
		Subject:     []string{"node", "socket"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "types.OrderedMap[int]",
		Doc: "ReleaseAuthoredUsesSocketConnections are the connections a " +
			"Release's own edge surface creates on one component, one entry per " +
			"socket the Release wrote for it, in author order, each holding that " +
			"socket's created-connection count.\n\nIt is the operator-side half " +
			"of the population question. Both documents can bind one socket — " +
			"the component's own dependency block, and this surface, which may " +
			"wire a socket the architecture declared and nothing bound to a " +
			"component that already exists — so a rule reading the component's " +
			"block alone reports a socket as unpopulated that the build wires, " +
			"and refuses a document this surface expressly permits.\n\nIt is " +
			"keyed per socket because that is where the question is asked. A " +
			"rule over a socket that must carry a connection is asked about " +
			"every socket the component type declares, including the ones " +
			"neither document wrote anything for — which are the ones it exists " +
			"to catch — so the answer has to be in hand for a socket that has " +
			"no connection to read anything from. The facts keyed per connection " +
			"on this same surface cannot answer there: keying one takes an " +
			"alias, and only an existing connection supplies an alias.\n\nThe " +
			"count is the fact rather than a yes-or-no answer, and it holds two " +
			"emptinesses apart. A socket absent from this map is one the Release " +
			"named nowhere on that component; a socket present with a count of " +
			"zero is one it named and wrote entries under, none of which creates " +
			"a connection — the operator configuring connections that some other " +
			"document has to have drawn. Neither populates the socket, so a rule " +
			"refusing an unpopulated socket treats them alike, while a rule " +
			"telling an author what to change names a different correction for " +
			"each.\n\nWhat counts as one connection here is the opposite of what " +
			"counts on the component's own dependency block, and the two facts " +
			"have the same shape, so a reader unifying them makes one of them " +
			"wrong. Here an entry counts only when it names a target: naming one " +
			"draws the connection, while an entry naming none carries per-edge " +
			"configuration for a connection some other document already drew and " +
			"so draws nothing itself. On the component's block an entry counts " +
			"whether or not it names a target, because an omitted target there " +
			"falls back to the entry's own alias and the connection is drawn " +
			"regardless. The alias is doing a different job on each surface — " +
			"supplying the absent target on one, identifying which existing " +
			"connection to configure on the other — which is why one authored " +
			"shape means opposite things and why the two counts are separately " +
			"defined.\n\nIt reads this surface and no other. The component's own " +
			"dependency block and an attachment's block are separate facts " +
			"because each is separately authored and independently absent. A " +
			"rule asking whether a socket is populated across every surface that " +
			"can populate one reads them together, and none of them carries " +
			"another.\n\nIt counts connections and never entries. Which aliases " +
			"the Release wrote an entry under is the separate fact declared " +
			"beside it, and on this surface the two answer differently for most " +
			"of what an operator writes: an entry carrying configuration alone " +
			"appears there and counts nothing here.",
	},
	{
		Name:        "release-authored-uses-socket-entry-aliases",
		Stage:       "normalized",
		SourceField: "types.BoundaryComponent.Uses",
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "[]string",
		Doc: "ReleaseAuthoredUsesSocketEntryAliases are the aliases a Release's " +
			"own edge surface wrote an entry under on one component's socket, in " +
			"author order.\n\nIt reports authorship and nothing else, and an " +
			"entry appears here whether or not it carries any content. That rule " +
			"is stated once and holds for every per-edge surface, which is the " +
			"whole difference between this fact and the connection count declared " +
			"beside it: the count asks which entries draw a connection, a " +
			"question each surface answers by its own opposite rule, while this " +
			"asks which entries the author wrote at all, which every surface " +
			"answers the same way. The gap is widest here, because an entry on " +
			"this surface commonly names no target at all: it configures a " +
			"connection another document drew, so it draws nothing and counts " +
			"nothing, and it is still an entry the operator wrote. An empty " +
			"result says the Release wrote no entry under that socket, and does " +
			"not separate a socket it named and left empty from one it named " +
			"nowhere — the count holds that distinction deliberately and is " +
			"where it is read.\n\n" +
			"Authorship is reported positively rather than inferred, and that is " +
			"why the fact exists. Both fields of an entry on this surface are " +
			"optional, so an entry with neither set is a document this surface " +
			"accepts and carries no content whatever. Every fact keyed per " +
			"connection here reads its zero value for it, and reads the same zero " +
			"value for an alias the Release wrote nothing at — so a rule reading " +
			"content cannot tell those apart, and they need opposite treatment: " +
			"an entry that writes nothing is the maximal violation of a key the " +
			"socket's contract declares required, while a connection this " +
			"surface never wrote is not its to answer for. Inferring authorship " +
			"from what no other surface claims is the alternative and it is " +
			"weaker: it is only ever as complete as the set of surfaces that can " +
			"draw a connection, and a connection a Feature contributes is " +
			"reported at a later stage than this one, so a rule here cannot see " +
			"it at all.\n\nIt is keyed per socket and hands back the set rather " +
			"than answering per connection, because the entries an author wrote " +
			"and the connections a build draws are not the same set, and on this " +
			"surface they routinely differ. An entry naming no target may sit at " +
			"an alias nothing drew, which is an operator configuring a connection " +
			"they expect another document to supply; a fact keyed per connection " +
			"is unreadable there, since keying one takes an alias and only an " +
			"existing connection supplies one. The set also enumerates, so a rule " +
			"over one socket visits exactly the entries the operator wrote. A " +
			"reader narrowing this to a per-connection answer, or to a count, " +
			"removes both of those and can restore neither.\n\nThis surface has " +
			"one authoring form, an alias per entry, so every alias here is a key " +
			"the operator wrote.\n\nIt reads the Release's own edge surface and " +
			"no other. The component's own dependency block is a separate fact " +
			"with the same shape and the same authorship rule, because it is " +
			"separately authored and independently absent, and neither carries " +
			"the other.",
	},
	{
		Name:        "release-component-infra",
		Stage:       "normalized",
		SourceField: "types.BoundaryComponent.Infra",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "types.OrderedMap[any]",
		Doc: "ReleaseComponentInfra is the operator's physical-sizing " +
			"configuration for one component as it was written, carried as an " +
			"opaque ordered payload.\n\nIt is the payload rather than a named " +
			"fact about it, on the same reasoning the configuration pillars " +
			"are: a rule whose predicate is \"what was written here conforms to " +
			"the contract the component type declares\" has that content as its " +
			"input, and the content has no other home to be read from.\n\nIt is " +
			"keyed by the component the operator named, which is the identity " +
			"the operator writes it against. The scope it was written inside " +
			"contributes the component's placement and its inherited metadata " +
			"and contributes nothing to this payload, so the scope is not part " +
			"of this fact's key.\n\nEmpty is the operator writing nothing, and " +
			"a rule reading it as such has nothing to measure: the contract for " +
			"this pillar is opt-in on the component type's side, and a " +
			"component whose operator wrote no sizing has no sizing to " +
			"check.\n\nThe developer's configuration for the same component is " +
			"a different fact on a different surface. The two are written on " +
			"separate documents by separate hands, the component type declares " +
			"a separate contract for each, and each is independently absent.\n" +
			"\nAuthor order is carried because these entries reach rendered " +
			"output, and an unordered payload would put a byte-identical result " +
			"at the mercy of map iteration.",
	},
	{
		Name:        "boundary-attachment-declaration",
		Stage:       "normalized",
		SourceField: "types.Boundary.Features",
		Subject:     []string{"boundary", "attachment"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "(AttachmentDeclaration, bool)",
		Doc: "BoundaryAttachmentDeclaration is what an attachment declared on a " +
			"boundary itself states — the Feature it attaches and the sockets it " +
			"wires on that Feature — reported absent when the boundary declares " +
			"no attachment under that name.\n\nIt is keyed by the " +
			"boundary rather than by a component, and that is the whole reason it " +
			"is a fact of its own. An attachment written at a boundary applies to " +
			"every component the boundary holds, so it is authored once and " +
			"contributes many times; a fact keyed by a component would be asked " +
			"once per component and never at all for a boundary holding none — " +
			"which is silence indistinguishable from approval. Keyed here, it is " +
			"asked exactly once per attachment the operator wrote, and a rule " +
			"over it can name that site.\n\nThe absence is reported rather than " +
			"resolved because an attachment naming no Feature type is something " +
			"the operator surface accepts, so an empty type and no attachment at " +
			"all are different answers that a zero value alone would merge.\n" +
			"\nWhat the sockets are checked against is a separate fact on a " +
			"separate document: the socket set the attached Feature declares. " +
			"The two are written by different hands and either can be absent " +
			"without the other, so a rule reads them together and neither " +
			"carries the other.",
	},
	{
		Name:        "release-attachment-type",
		Stage:       "normalized",
		SourceField: "types.BoundaryFeature.Type",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "ReleaseAttachmentType is the Feature type a Release declares for " +
			"one attachment on one component, reported absent when the " +
			"Release declares no attachment under that name for it.\n\nIt is " +
			"a separate fact from the type the Aggregate declares for the same " +
			"attachment name, and the pair is what makes the agreement question " +
			"askable at all. Both documents may name one attachment — the " +
			"application's own declaration carries its configuration, the " +
			"Release's carries the physical sizing — and the two are merged " +
			"into one attachment further down, which only means anything if they " +
			"name the same Feature. A rule reading one surface alone cannot see " +
			"a disagreement.\n\nThe absence is reported rather than resolved, " +
			"and it carries a distinction the rule turns on: an attachment only " +
			"the application declares is nothing to reconcile, an attachment " +
			"only the Release declares is the supported way to add one, and " +
			"an attachment naming no type states nothing to disagree with. All " +
			"three are legitimate, so only a name declared on both sides with a " +
			"type on both sides is a question.\n\nIt is a separate fact from " +
			"the attachment a boundary itself declares, which is keyed by that " +
			"boundary: this one is written against a named component and the " +
			"other applies to every component in a scope, so they are asked a " +
			"different number of times and a rule over either names a different " +
			"authored site.",
	},
	{
		Name:        "release-attachment-authored-uses-socket-names",
		Stage:       "normalized",
		SourceField: "types.BoundaryFeature.Uses",
		Subject:     []string{"node", "attachment", "socket"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "[]string",
		Doc: "ReleaseAttachmentAuthoredUsesSocketNames are the socket names an " +
			"attachment the Release declares for one component wires on the " +
			"Feature it attaches, in author order.\n\nThese name sockets on the " +
			"attached Feature, a separate declaration surface from the " +
			"component's own sockets, which is why the fact is keyed per " +
			"attachment as well as per node.\n\nIt is a separate fact from the " +
			"sockets the application's own attachment of the same name wires. " +
			"Both documents may declare an attachment under one name and each " +
			"wires what it wires, so a rule reading one surface alone would " +
			"measure the other's sockets against nothing.\n\nIt is also a " +
			"separate fact from the sockets an attachment written at a scope " +
			"wires, which is keyed by that scope: this one is written against a " +
			"named component, so the two are asked a different number of times " +
			"and a rule over either names a different authored site.",
	},
	{
		Name:        "authored-component-schema-text",
		Stage:       "normalized",
		SourceField: "types.ComponentSpec.Schema",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "componentType", Type: "string"}},
		Returns:     "[]byte",
		Doc: "AuthoredComponentSchemaText is the contract document a component " +
			"type declares over its free-form configuration, as authored, " +
			"serialized to opaque bytes — empty when the type declares none.\n" +
			"\nThe bytes are opaque by type, so nothing about the declared " +
			"shape is legible from the fact beyond the document itself. That is " +
			"what lets a rule whose predicate is \"what was written conforms to " +
			"this contract\" receive the whole contract without receiving the " +
			"entity that carries it.\n\nIt answers what was WRITTEN, and the " +
			"resolved form of the same contract is a separate fact that answers " +
			"what it MEANS. A rule asking whether the author wrote something a " +
			"reader would recognise reads this one; a rule interpreting the " +
			"contract reads the resolved one, and does not resolve these bytes " +
			"for itself. Resolving them per reader would multiply the work by the " +
			"number of readers, hand each reader responsibility for supplying the " +
			"documents a reference points at, and turn one failure reported once " +
			"into several each reader reports separately or swallows.\n\nIt is " +
			"the document as " +
			"authored and not a copy of the file's bytes: serializing an " +
			"authored node reproduces its content, not its formatting, so a " +
			"rule reading this must not draw conclusions from layout.\n\nIt is " +
			"one fact over the whole document rather than one per section. The " +
			"sections are declared together in one authored node, a reader " +
			"selects the one it wants from it, and splitting them would be " +
			"three facts sharing one carrier that can only be absent or present " +
			"together.\n\nA type that inherits its contract carries no separate " +
			"authored document to read: inheritance copies the base's sections " +
			"down as the base wrote them, so the authored node already holds " +
			"every inherited constraint and there is no second, composed view " +
			"to prefer over it." +
			declarationCompelsImplementation,
	},
	{
		Name:        "socket-value-schema-text",
		Stage:       "normalized",
		SourceField: "types.UsesSchemaEntry.ValueSchema",
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "[]byte",
		Doc: "SocketValueSchemaText is the contract a component type's dependency " +
			"socket declares over the developer's configuration for one edge, as " +
			"authored, serialized to opaque bytes — empty when the socket " +
			"declares none.\n\nThe bytes are opaque by type, so nothing about the " +
			"declared shape is legible from the fact beyond the document itself. " +
			"That is what lets a rule whose predicate is \"what was written on " +
			"this edge conforms to the contract its socket declares\" receive the " +
			"whole contract without receiving the entity that carries it.\n" +
			"\nIt answers what was WRITTEN. A rule that interprets this contract " +
			"rather than reading it needs the resolved form declared as its own " +
			"fact, and does not resolve these bytes for itself: resolving them " +
			"per reader would multiply the work by the number of readers, hand " +
			"each reader responsibility for supplying the documents a reference " +
			"points at, and turn one failure reported once into several each " +
			"reader reports separately or swallows.\n\nEmptiness is one " +
			"answer rather than two. A socket that declares no contract for this " +
			"pillar and a socket the catalog declares no entry for both leave an " +
			"edge's configuration for it unvalidated, and the question a rule " +
			"over this fact asks is whether there is a contract to evaluate at " +
			"all.\n\nIt covers the developer's pillar alone. The operator's " +
			"per-edge pillar is a separate authored field with its own fact, and " +
			"the two are separately omittable — a socket may declare a contract " +
			"for one and none for the other — so one fact spanning both could not " +
			"report which of them was written." +
			declarationCompelsImplementation,
	},
	{
		Name:        "socket-infra-schema-text",
		Stage:       "normalized",
		SourceField: "types.UsesSchemaEntry.InfraSchema",
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "[]byte",
		Doc: "SocketInfraSchemaText is the contract a component type's dependency " +
			"socket declares over the operator's configuration for one edge, as " +
			"authored, serialized to opaque bytes — empty when the socket " +
			"declares none.\n\nIt is the counterpart of the contract over the " +
			"developer's per-edge pillar and a separate fact rather than the same " +
			"one in another shape, for the reason the two payload facts are " +
			"separate: the pillars are written on different documents by " +
			"different hands, and a socket may declare a contract for one and " +
			"none for the other. One fact spanning both would report a single " +
			"presence for two independently authored fields.\n\nThe bytes are " +
			"opaque by type, and they answer what was WRITTEN. A rule that " +
			"interprets this contract rather than reading it needs the resolved " +
			"form declared as its own fact, and does not resolve these bytes for " +
			"itself: resolving them per reader would multiply the work by the " +
			"number of readers, hand each reader responsibility for supplying the " +
			"documents a reference points at, and turn one failure reported once " +
			"into several each reader reports separately or swallows.\n" +
			"\nEmptiness is one answer rather than two, exactly as it " +
			"is for the developer's pillar: a socket declaring no contract and a " +
			"socket the catalog declares no entry for both leave an edge's " +
			"operator configuration unvalidated, and what a rule asks is whether " +
			"there is a contract to evaluate at all." +
			declarationCompelsImplementation,
	},
	{
		Name:        "compiled-component-schema",
		Stage:       "normalized",
		SourceField: "resolver.CompileComponentSchema(catalog, componentType)",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "componentType", Type: "string"}},
		Returns:     "(map[string]any, bool)",
		Doc: "CompiledComponentSchema is the contract a component type declares " +
			"over its configuration, resolved: the authored document with every " +
			"shorthand expanded and every cross-document reference followed, and " +
			"whether there was a contract that resolved at all.\n\nIt is the " +
			"resolved form and not the authored text, and that is the whole " +
			"reason it is a fact. Resolving happens once, at the point the " +
			"catalog becomes reachable, and every reader downstream of that point " +
			"reads the same result. A rule handed the authored text instead would " +
			"have to resolve it again, which multiplies the work by the number of " +
			"readers, hands each reader responsibility for supplying the " +
			"referenced documents, and turns one failure that is reported once " +
			"into several that each reader reports — or, worse, swallows.\n\nThe " +
			"authored text is a separate fact and stays one. A rule asking what " +
			"was written reads that; a rule asking what the written contract " +
			"means reads this. Neither substitutes for the other, and a rule " +
			"reading this one is asserting that the resolved meaning is its " +
			"input.\n\nThe second result distinguishes a type with no contract, " +
			"and one whose contract could not be resolved, from a contract that " +
			"resolved to nothing. A rule over this fact reads that outcome and " +
			"never produces it, which places one obligation on whatever produces " +
			"the fact: a contract that fails to resolve is reported where the " +
			"resolving happens, once, rather than passed on to every rule as a " +
			"bare absence.\n" +
			"\nThe resolved document holds " +
			"no order. A rule selects from it by key, which iterates nothing; one " +
			"that walked the whole document to build output would put a " +
			"byte-identical result at the mercy of map iteration.",
	},
	{
		Name:        "compiled-socket-value-schema",
		Stage:       "normalized",
		SourceField: `resolver.CompileSocketPillarSchema(catalog, componentType, socket, "values")`,
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "(map[string]any, bool)",
		Doc: "CompiledSocketValueSchema is the contract a component type's " +
			"dependency socket declares over the developer's configuration for " +
			"one edge, resolved: the authored document with every shorthand " +
			"expanded and every cross-document reference followed, and whether " +
			"there was a contract that resolved at all.\n\nIt is the resolved " +
			"form and not the authored text, and that is the whole reason it is " +
			"a fact. Resolving happens once, at the point the catalog becomes " +
			"reachable, and every reader downstream of that point reads the same " +
			"result. A rule handed the authored text instead would have to " +
			"resolve it again, which multiplies the work by the number of " +
			"readers, hands each reader responsibility for supplying the " +
			"referenced documents, and turns one failure that is reported once " +
			"into several that each reader reports — or, worse, swallows.\n" +
			"\nThe authored text is a separate fact and stays one. A rule asking " +
			"what the socket declared reads that; a rule asking what the " +
			"declared contract means reads this. Neither substitutes for the " +
			"other, and a rule reading this one is asserting that the resolved " +
			"meaning is its input.\n\nThe second result reports whether there is " +
			"a resolved contract to read, and nothing finer. It does not " +
			"separate a socket that declares no contract for this pillar, a " +
			"socket the component type declares no entry for, a component type " +
			"the catalog does not hold, and a contract that failed to resolve: " +
			"all four arrive as the same negative. So a rule over this fact may " +
			"ask whether there is a contract to evaluate at all and may ask " +
			"nothing more, which places one obligation on whatever produces the " +
			"fact — a condition needing its own report is settled where the " +
			"resolving happens, once, rather than left for every rule reading a " +
			"bare absence to infer.\n\nThe resolved document is keyed by the " +
			"pillar rather than being the socket's field map directly. A socket " +
			"declares its constraints as bare pillar content, and resolving " +
			"wraps them under the pillar's name, so a rule selects the pillar " +
			"from this document exactly as it would from a component type's own " +
			"resolved contract.\n\nIt covers the developer's pillar alone. The " +
			"operator's per-edge pillar is a separate authored field whose " +
			"resolved form is its own fact, and the two are separately " +
			"omittable — a socket may declare a contract for one and none for " +
			"the other — so one fact spanning both could not report which of " +
			"them resolved.\n\nThe resolved document holds no order. A rule " +
			"selects from it by key, which iterates nothing; one that walked the " +
			"whole document to build output would put a byte-identical result at " +
			"the mercy of map iteration.",
	},
	{
		Name:        "compiled-socket-infra-schema",
		Stage:       "normalized",
		SourceField: `resolver.CompileSocketPillarSchema(catalog, componentType, socket, "infra")`,
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "(map[string]any, bool)",
		Doc: "CompiledSocketInfraSchema is the contract a component type's " +
			"dependency socket declares over the operator's configuration for " +
			"one edge, resolved: the authored document with every shorthand " +
			"expanded and every cross-document reference followed, and whether " +
			"there was a contract that resolved at all.\n\nIt is the counterpart " +
			"of the resolved contract over the developer's per-edge pillar, and a " +
			"separate fact rather than the same one in another shape, for the " +
			"reason the two authored contracts are separate: the pillars are " +
			"written on different documents by different hands, and a socket may " +
			"declare a contract for one and none for the other. One fact " +
			"spanning both would report a single presence for two independently " +
			"authored fields.\n\nIt is the resolved form and not the authored " +
			"text, and that is the whole reason it is a fact. Resolving happens " +
			"once, at the point the catalog becomes reachable, and every reader " +
			"downstream of that point reads the same result. A rule handed the " +
			"authored text instead would have to resolve it again, which " +
			"multiplies the work by the number of readers, hands each reader " +
			"responsibility for supplying the referenced documents, and turns " +
			"one failure that is reported once into several that each reader " +
			"reports — or, worse, swallows. The authored text stays a fact of " +
			"its own: a rule asking what the socket declared reads that one, and " +
			"neither substitutes for the other.\n\nThe second result reports " +
			"whether there is a resolved contract to read, and nothing finer, " +
			"exactly as it does for the developer's pillar. It does not separate " +
			"a socket that declares no contract for this pillar, a socket the " +
			"component type declares no entry for, a component type the catalog " +
			"does not hold, and a contract that failed to resolve: all four " +
			"arrive as the same negative. So a condition needing its own report " +
			"is settled where the resolving happens, once, rather than left for " +
			"every rule reading a bare absence to infer.\n\nThe resolved " +
			"document is keyed by the pillar rather than being the socket's " +
			"field map directly. A socket declares its constraints as bare " +
			"pillar content, and resolving wraps them under the pillar's name, " +
			"so a rule selects the pillar from this document exactly as it would " +
			"from a component type's own resolved contract.\n\nThe resolved " +
			"document holds no order. A rule selects from it by key, which " +
			"iterates nothing; one that walked the whole document to build " +
			"output would put a byte-identical result at the mercy of map " +
			"iteration.",
	},
	{
		Name:        "declared-provides-keys",
		Stage:       "normalized",
		SourceField: "resolver.ComponentProvidesKeys(component, libraries)",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "componentType", Type: "string"}},
		Returns:     "[]string",
		Doc: "DeclaredProvidesKeys are the names a component type's contract " +
			"declares its realization will publish — the output half of that " +
			"contract, resolved.\n\nIt is a separate fact from the resolved " +
			"contract document rather than a reading of it. The names are " +
			"resolved from the contract's output section alone, on its own, so a " +
			"type whose configuration section cannot be resolved still has an " +
			"answer here; one fact could not report that.\n\nIt is a set of " +
			"names rather than a membership question because the rules over it " +
			"ask both: whether one name is declared, and what the declared names " +
			"are when a rule has to say what was expected instead.\n\nThe names " +
			"come back sorted. A resolved contract holds no order, so there is no " +
			"authoring order left to preserve and sorting is what makes the " +
			"result the same on every run.\n\nIt covers a component type's " +
			"contract. The same question about a Feature's contract is a separate " +
			"declaration surface: the two are written on different documents, and " +
			"a rule over either names a different authored site.",
	},
	{
		Name:        "resolved-parameter-context",
		Stage:       "normalized",
		SourceField: "resolver.ApplyParameterDefaults(contract, params)",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "map[string]any",
		Doc: "ResolvedParameterContext is what a document's declared parameter " +
			"contract resolved to, as one map: each slot that resolved to a " +
			"value, under the name the contract declares it by.\n\nThree " +
			"contributions are folded into it, and naming them is most of the " +
			"reason it is a fact. The parameters the Release wrote inline; the " +
			"parameters the Release imported from a file; and the contract's " +
			"own declared defaults, filled into each slot it defaults that the " +
			"Release left unsupplied. A rule handed the three separately would " +
			"fold them again, and a rule handed only the first two would answer " +
			"differently from the compiler for every slot whose value came from " +
			"a default — silently, because a defaulted slot and a supplied one " +
			"are indistinguishable once folded.\n\nIts keys are a subset of the " +
			"declared slots, and which subset it is matters to every rule " +
			"reading a name it expected. The contract is closed: a parameter " +
			"supplied against no declaration halts before this fact is " +
			"complete, so every key here names a declared slot. The converse " +
			"does not hold. A slot the contract marks required has always " +
			"resolved, to a supplied value or to its default, but an optional " +
			"slot that was neither supplied nor given a default is simply " +
			"absent. So a missing name means optional-and-unsupplied, and never " +
			"a failure this fact did not report.\n\nIt is the resolved " +
			"fulfilment and not the declaration. Which parameters a document " +
			"declares is a separate fact and stays one: a rule asking what " +
			"exists reads that, a rule asking what it resolved to reads this, " +
			"and neither substitutes for the other.\n\nIt comes back whole " +
			"rather than one name at a time, and the shape of the rules over it " +
			"is why. A conditional constraint's predicate may ask for one named " +
			"parameter or for the whole set, and the predicate is evaluated by " +
			"the validator rather than by the rule — so the rule hands the " +
			"entire context across and could not assemble one from per-name " +
			"lookups.\n\nThe map holds no order and never acquires one. A rule " +
			"selects from it by key, which iterates nothing; one that walked it " +
			"to build output would put a byte-identical result at the mercy of " +
			"map iteration.\n\nIt is always present. An empty map means no slot " +
			"resolved to a value — because the contract declares none, or " +
			"because every slot it declares is optional and went unsupplied — " +
			"and never that there was no answer, so a rule reading it has no " +
			"absent case to distinguish and needs no second result to report " +
			"one.\n\nTwo of its properties bind whatever reads it, and neither " +
			"is visible in the map itself. Its values may have reached it from " +
			"a file rather than from either authoring document, so it is not " +
			"reconstructible from the authored text alone. And its values may " +
			"be secret-bearing, which is a property of what an author puts in a " +
			"parameter rather than something the contract can mark: so a rule " +
			"neither copies it, nor anything selected from it, into a " +
			"diagnostic, and holds it no longer than the invocation it was " +
			"handed to.\n\nA flattened sub-aggregate node fulfils its own " +
			"contract, and that fulfilment is a separate declaration keyed by " +
			"the node rather than by a document.",
	},

	// ---------------------------------------------------------------- topology

	{
		Name:        "flattened-node-component-type",
		Stage:       "topology",
		SourceField: "topology.TopologyNode.ComponentType",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "(string, bool)",
		Doc: "FlattenedNodeComponentType is the component type a node in the " +
			"flattened set carries, and whether a node of that name is in the " +
			"set at all.\n\nIt answers for every node the composition " +
			"contributes, which is what separates it from the type an instance " +
			"declares. A component a sub-aggregate contributes is declared in " +
			"that sub-aggregate's document, which is read only when the " +
			"composition is flattened — so the fact over the application's own " +
			"instance list cannot see it, and a rule reading that alone reports " +
			"every such component as not existing and then declines to judge it. " +
			"This reads the flattened node instead, where a node contributed by " +
			"any document carries its type the same way.\n\nOne read serves " +
			"both tiers and there is deliberately no second path to keep in " +
			"step. A node the application declared directly and a node a " +
			"composition contributed are the same shape here, so a rule reading " +
			"this needs no fallback and cannot acquire two behaviours that drift " +
			"apart.\n\nIt does not replace the fact over the application's own " +
			"instance list, and that fact does not replace it. That one is " +
			"complete earlier, so a rule asked before the composition is " +
			"flattened can read it and cannot read this; and it separates a name " +
			"no instance declares from an instance declaring the empty type, " +
			"which is a distinction about an authored document that a node in a " +
			"built set cannot carry. A rule asking what an author wrote reads " +
			"that; a rule asking what the build assembled reads this.\n\nThe " +
			"second result reports membership of the flattened set and nothing " +
			"finer. A name absent from it was never contributed, or was " +
			"contributed and then removed by a false activation toggle — two " +
			"findings this fact does not separate. The build keeps the removed " +
			"names apart from the surviving ones, so the distinction is not " +
			"lost by the flattening; reporting it is a separate question and " +
			"not this one.\n\nIt names the type as the node carries it, which is " +
			"a reference to a catalog entity and not a reading of one. Whether " +
			"the catalog holds an entity under that name is a separate fact, and " +
			"a rule needing both asks both.",
	},
	{
		Name:        "resolved-parameter",
		Stage:       "topology",
		SourceField: "topology.TopologyNode.ResolvedParameters",
		Subject:     []string{"node", "parameter"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "name", Type: "string"},
		},
		Returns: "(any, bool)",
		Doc: "ResolvedParameter is the value one named parameter resolved to for " +
			"a node, and whether the node resolved that name at all.\n\nIt " +
			"is keyed rather than enumerating, deliberately. A flattened " +
			"sub-aggregate node carries its own resolved set in a shape that " +
			"holds no order, so an accessor handing back the whole set would " +
			"put a byte-identical output at the mercy of map iteration; a keyed " +
			"lookup iterates nothing.",
	},
	{
		Name:        "resolved-uses-socket-connections",
		Stage:       "topology",
		SourceField: "topology.TopologyResult.Edges",
		Subject:     []string{"node", "socket"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "types.OrderedMap[int]",
		Doc: "ResolvedUsesSocketConnections are the connections a node ends up " +
			"with once every origin that draws one has contributed, one entry " +
			"per socket carrying at least one, in the order they were drawn, " +
			"each holding that socket's connection count.\n\nIt is the third " +
			"answer to whether a socket is populated, and the only one that " +
			"reaches a component the authoring documents never name. A " +
			"component a sub-aggregate contributes keeps its own dependency " +
			"block in that sub-aggregate's document, which is read only when " +
			"the composition is flattened — so the facts over the two " +
			"authoring documents cannot see it, and a rule reading them alone " +
			"reports every socket of such a component as carrying nothing. " +
			"This reads the drawn connections instead, which is one answer " +
			"spanning every origin: what a component's own dependency block " +
			"wrote, wherever that block was authored, and what the " +
			"deployment's edge surface created — the latter decided by the " +
			"same rule that decides whether such an entry draws a connection " +
			"at all, so this fact and the authored one cannot disagree about " +
			"it.\n\nWhat it counts and what it deliberately does NOT is a " +
			"third counting rule, distinct from both authored counts, and a " +
			"reader unifying the three makes at least one of them wrong. A " +
			"connection an attachment contributed is not counted. Those " +
			"socket names are declared on the Feature the attachment names — " +
			"a separate socket set on a separate entity — so an attachment can " +
			"never satisfy a component's own socket, and counting one would " +
			"report a socket as populated that nothing bound. Neither authored " +
			"count states that exclusion, because neither reads a surface an " +
			"attachment contributes to; it belongs here and only here, because " +
			"this is the one fact reading connections after every origin has " +
			"been folded together.\n\nIt is keyed per socket because that is " +
			"where the question is asked, and read per node because a socket " +
			"carrying nothing has no connection to be keyed by. A rule over a " +
			"socket that must carry a connection is asked about every socket " +
			"the component type declares, including the ones nothing bound — " +
			"which are the ones it exists to catch.\n\nThe count is the fact " +
			"rather than a yes-or-no answer, for the reason it is on the two " +
			"authored surfaces: it answers HOW MANY, which is what a rule over " +
			"a socket admitting a single connection needs. One thing those " +
			"counts hold apart collapses here, and a reader must not read that " +
			"collapse back into them. On an authored surface a socket can be " +
			"present carrying zero, because naming a socket and populating it " +
			"are separate acts there. Here only a drawn connection puts a " +
			"socket in this map, so absence is the only shape emptiness takes " +
			"and a zero count never appears.\n\nIt does not replace the facts " +
			"over the two authoring documents, and they do not replace it. " +
			"They are complete earlier, so a rule asked before the composition " +
			"is flattened can read them and cannot read this; and they " +
			"separate a socket named-and-left-empty from one never named, " +
			"which this cannot. A rule asking what an author wrote reads " +
			"those; a rule asking what the build drew reads this.",
	},

	{
		Name:        "node-boundary-placement",
		Stage:       "topology",
		SourceField: "topology.TopologyNode.BoundaryPath",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "(string, bool)",
		Doc: "NodeBoundaryPlacement is the boundary a node was placed in, and " +
			"whether anything placed it at all.\n\nA deployment places a " +
			"component two ways and this answers for both, which is what makes " +
			"it a reading of placement rather than of one spelling. A boundary " +
			"names a component under the id it carries in the flattened set — " +
			"for a component a module contributes, that is its qualified dotted " +
			"id — and a boundary routes a whole module in at once, which places " +
			"every component that module contains without naming any of them. " +
			"The two are reconciled before this fact is produced, so a rule " +
			"reading it cannot be written against one axis and quietly ignore " +
			"the other.\n\nThe second result is what a rule over placement " +
			"turns on, and it is not a convenience. A component nothing placed " +
			"is emitted outside every boundary the deployment declared, which " +
			"is a defect the build otherwise reports nowhere; distinguishing it " +
			"from a component placed in a boundary whose own path happens to be " +
			"empty is what lets the rule name the first and pass the " +
			"second.\n\nThe path is the boundary's own nesting path rather than " +
			"one authored key, because boundaries nest and two boundaries at " +
			"different depths may carry one name.",
	},

	{
		Name:        "module-contributed-nodes",
		Stage:       "topology",
		SourceField: "topology.TopologyNode.SourceAggregate",
		Subject:     []string{"module"},
		Params:      []paramDecl{{Name: "module", Type: "string"}},
		Returns:     "[]string",
		Doc: "ModuleContributedNodes are the nodes a sub-module contributed to " +
			"the flattened set, in the order the composition contributed them, " +
			"including the nodes contributed by the modules nested inside " +
			"it.\n\nIt reads the module each node came from rather than a " +
			"module's own component list, and the difference is what makes it " +
			"answerable at all: a module's components are declared in that " +
			"module's own document, and the identity a rule needs is the one " +
			"each component carries after flattening. Reading the composition " +
			"from the side it landed on gives both at once.\n\nThe nested " +
			"modules are included because a module's placement covers " +
			"everything it contains: routing a module into a boundary places " +
			"the components of every module nested inside it too, so a rule " +
			"asking whether anything placed a module's components has to be " +
			"asked over the same set that a placement would cover.\n\nAn empty " +
			"result is a real answer and not an absence. A module contributing " +
			"no node has nothing that could be emitted outside a boundary, so a " +
			"rule over placement passes it rather than reporting it, and a " +
			"module a false activation toggle removed contributes nothing here " +
			"because it was never composed.",
	},

	// ------------------------------------------------------------------- graph

	{
		Name:        "per-leaf-attachment-provenance",
		Stage:       "graph",
		SourceField: "types.IRFeatureEntry.ValuesProvenance",
		Subject:     []string{"node", "attachment", "pillar", "leaf"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "pillar", Type: "string"},
			{Name: "leaf", Type: "string"},
		},
		Returns: "LeafProvenance",
		Doc: "PerLeafAttachmentProvenance is where one leaf of an attachment's " +
			"configuration came from, as a composite record.\n\nThe " +
			"composite is the fact: an explicit value displacing an " +
			"annotation-supplied one is a single question whose answer needs " +
			"both sides, so splitting it into per-coordinate accessors would " +
			"make the question un-askable and the accessors individually " +
			"meaningless.",
	},
	{
		Name:        "per-leaf-attachment-authored-component",
		Stage:       "graph",
		SourceField: "ir.AttachmentSites",
		Subject:     []string{"node", "attachment", "pillar", "leaf"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "pillar", Type: "string"},
			{Name: "leaf", Type: "string"},
		},
		Returns: "string",
		Doc: "PerLeafAttachmentAuthoredComponent is the component key the entry " +
			"that supplied one leaf of an attachment's configuration was " +
			"written under, empty when that entry was written scope-wide and " +
			"names no component at all.\n\nThe emptiness is the answer rather " +
			"than a missing value. A scope-wide entry takes effect on every " +
			"component the scope holds and none of those is a place anyone can " +
			"edit, so the one editable site is the scope's own block, which " +
			"names no component; a reader handed a component name there would " +
			"be pointed at one of the many places the entry takes effect " +
			"instead of the one place it was written.\n\nIt is a separate fact " +
			"from the leaf's provenance rather than a field on it. The " +
			"provenance record answers where a value came from, and it is one " +
			"fact because the displaced side and the displacing side are only " +
			"visible together. Where the entry sits in its document is a " +
			"different question, asked by a reader who already has the answer " +
			"to the first and needs somewhere to send an author.\n\nThe " +
			"spelling is the document's own and never one naming rule applied " +
			"to every surface. A deployment addresses a component a " +
			"composition contributed by the qualified identifier the " +
			"composition gave it, while the application's own scope tree keys " +
			"that same component by the short name it was declared under, so " +
			"one component is spelled two ways across the two surfaces and " +
			"this names whichever spelling the document that supplied the leaf " +
			"used.",
	},
	{
		Name:        "node-labels",
		Stage:       "graph",
		SourceField: "types.IRNode.Labels",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "types.OrderedMap[string]",
		Doc: "NodeLabels is the label set a node carries once every contributing " +
			"surface has been folded, in author order.\n\nThe accessor " +
			"takes the node as an argument rather than reading the subject under " +
			"inspection, and that is what lets a rule keyed on one node read " +
			"another's labels: a rule about an edge asks about the node at the " +
			"far end of it, which is never the node the rule is invoked over.",
	},
	{
		Name:        "per-edge-origin-feature-type",
		Stage:       "graph",
		SourceField: "types.IREdge.OriginFeature",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "string",
		Doc: "PerEdgeOriginFeatureType is the Feature type an edge originated " +
			"from, empty when the edge was drawn from a component's own " +
			"dependency block.\n\nThe emptiness is the answer and not a " +
			"missing value: it is what tells a rule which declaration surface " +
			"carries the edge's socket, and therefore which socket constraint " +
			"applies.\n\nThis names the Feature TYPE and never the " +
			"per-attachment instance. The instance is a separate coordinate on a " +
			"separate carrier, and a rule reaching for the declaration needs the " +
			"type, so a fact conflating them would hand half of its readers the " +
			"wrong string.",
	},
	{
		Name:        "per-edge-resolved-target",
		Stage:       "graph",
		SourceField: "types.IREdge.Target",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "string",
		Doc: "PerEdgeResolvedTarget is the node an edge resolves to.\n\nIt " +
			"is a separate fact from the target as authored rather than the same " +
			"fact at a later moment. An authored target may be empty and mean " +
			"the connection's own alias, so the resolved form is the only one a rule " +
			"about the node at the far end of an edge can read; reading the " +
			"authored form for that question would make the rule wrong for " +
			"exactly those edges whose target the author left implicit.",
	},
	{
		Name:        "attachment-resolved-type",
		Stage:       "graph",
		SourceField: "types.IRFeatureEntry.Type",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "string",
		Doc: "AttachmentResolvedType is the Feature type an attachment resolves " +
			"to, empty when it resolved to no catalog entity.\n\nIt is the " +
			"resolved type rather than the authored one because an attachment's " +
			"type can be contributed at a scope above the node: the block " +
			"authored on the node itself is silent for precisely the " +
			"attachments a rule most needs to reach.",
	},
	{
		Name:        "merged-attachment-names",
		Stage:       "graph",
		SourceField: "types.IRSpec.FeatureValuesMapping",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "MergedAttachmentNames are the attachment names a node carries once " +
			"the whole attachment cascade has folded, in the order the merged " +
			"set holds them.\n\nIt is deliberately separate from the names " +
			"an instance authored, and that distinction is the point. An " +
			"attachment contributed at a scope the node inherits from binds a " +
			"name on every node beneath that scope, so the authored block is " +
			"empty for exactly those deployments that rely on sharing; a rule " +
			"reading the authored names in place of these rejects a deployment " +
			"shape the product supports.\n\nThe fact is a name list and " +
			"not the merged entries themselves. Handing back the entries would " +
			"let a rule reach the per-leaf configuration provenance the " +
			"framework already exposes as a fact of its own, so a rule could " +
			"re-derive a declared fact from this one — which is the shape every " +
			"return here exists to keep closed.",
	},
	{
		Name:        "uses-inject-delivery-sockets",
		Stage:       "graph",
		SourceField: "types.IREdge.Inject",
		Subject:     []string{"node", "mechanism", "delivery-key"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "mechanism", Type: "string"},
			{Name: "key", Type: "string"},
		},
		Returns: "[]string",
		Doc: "UsesInjectDeliverySockets are the dependency sockets through which " +
			"a node's own dependency edges deliver one injection key under one " +
			"mechanism, in the order the graph holds those edges — one entry per " +
			"edge delivering the key, so a socket appears as often as an edge " +
			"named for it delivers that key.\n\nIt asks the delivery question " +
			"from the consuming node's side rather than from an edge's, and the " +
			"direction is part of the fact. A rule comparing what a component " +
			"delivers to itself against what it delivers across a dependency " +
			"edge is asked once per key the component delivers, and has to name " +
			"the socket the second delivery came through. Keyed by the edge " +
			"instead, that rule would enumerate mechanisms and keys inside its " +
			"own predicate and intersect them there, which is the traversal the " +
			"framework owns.\n\nThe keys are the ones an edge DELIVERS and not " +
			"the ones its author wrote, and the two differ. A delivery whose " +
			"payload cannot be carried as one string per key contributes no key: " +
			"it is absent here and it reaches no consumer, because the same " +
			"per-mechanism map this fact reads is what inject hydration resolves " +
			"against a target's provides. A rule refusing a collision needs " +
			"exactly this side — a key that reaches no consumer collides with " +
			"nothing, so reading the authored side would refuse a pairing the " +
			"build never produces.",
	},
	{
		Name:        "per-edge-inject",
		Stage:       "graph",
		SourceField: "types.IREdge.Inject",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "types.OrderedMap[types.OrderedMap[string]]",
		Doc: "PerEdgeInject is what one dependency edge delivers: per delivery " +
			"mechanism, the key it delivers under and the provides key it reads, " +
			"in author order.\n\nIt asks the delivery question from the edge's " +
			"side, where the fact naming the sockets that deliver a key asks it " +
			"from the consuming node's. The direction is part of each fact " +
			"rather than a shape either converts into: a rule refusing a key the " +
			"target does not declare is asked once per edge and needs that " +
			"edge's whole delivery block, while a rule refusing two deliveries " +
			"of one key is asked once per key and needs the sockets that carried " +
			"them.\n\nIt is placed where every edge exists rather than where an " +
			"author's own text does. An edge is drawn by a component's " +
			"dependency block, contributed by an attachment, or wired by the " +
			"operator surface, and a rule reading the first alone is silent for " +
			"exactly the edges the other two produce — a rule that cannot see " +
			"its subject and says nothing, which reads the same as one that saw " +
			"it and approved.\n\nAuthor order is the fact: a rule reporting one " +
			"finding per delivery emits them in this order, so the order is what " +
			"makes the report the same on every run." +
			declarationCompelsImplementation,
	},

	{
		Name:        "carried-connection-authored-delivery",
		Stage:       "graph",
		SourceField: "ir.CarriedConnections",
		Subject:     []string{"carried-connection"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "carrier", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
			{Name: "target", Type: "string"},
		},
		Returns: "[]types.OrderedMap[any]",
		Doc: "CarriedConnectionAuthoredDelivery is the delivery a connection " +
			"dictates AS IT WAS WRITTEN, keyed by mechanism name in author " +
			"order, each entry carrying that mechanism's payload as authored.\n" +
			"\nIt keeps the distinction the resolved per-edge delivery folds " +
			"away. An entry may deliver a literal or may reference a key the " +
			"target publishes, and the resolved form holds one string per " +
			"delivery key for both — so a rule refusing a reference to a key " +
			"nothing publishes, read over the resolved form, would refuse every " +
			"literal too. The payload is carried rather than decomposed because " +
			"the shape of one delivery entry is the entity contract's to state.\n" +
			"\nIt is keyed by the carrier as well as the connection because the " +
			"connection triple is not an identity. Two attachments on one node " +
			"may each write one socket and one alias, and the component itself " +
			"may write the same pair, so a fact keyed by node, socket and alias " +
			"answers for one of up to three separately authored blocks and " +
			"reports the others as approved. The node the connection resolves to is in " +
			"the key for the same reason: two carriers writing one socket and one " +
			"alias may name different components, and each delivers against the " +
			"one it named.\n\nThe result is every payload the " +
			"key names rather than the first, and the plural is the honest shape " +
			"rather than a convenience. One attachment instance name declared at " +
			"two scopes draws one connection from each, and the graph records the " +
			"two identically — so a key can name more than one authored block, " +
			"and answering with the first would leave the second unexamined while " +
			"a rule over the fact reported nothing.\n\nThe order is the order " +
			"the connections were drawn in, which is the order the graph holds " +
			"them: a rule reporting one finding per delivery emits them in this " +
			"order, so the report is the same on every run.\n\nIt is placed here " +
			"rather than one stage earlier because of the KEY and not the " +
			"content. The connection set is complete once the composition " +
			"finishes and the finalised graph draws none, so the delivery itself " +
			"travels unchanged from there; the surface each carrier was declared " +
			"on is settled by the reading that folds the attachment cascade, and " +
			"a fact keyed by a carrier is complete only once that is.\n\nWhat a delivery " +
			"is measured against is a separate fact on a separate document — the " +
			"keys the target's component type publishes — so a rule reads the two " +
			"together and neither carries the other.",
	},

	// --------------------------------------------------------------- described

	{
		Name:        "step-plugin-compatible-capabilities",
		Stage:       "described",
		SourceField: "wasm.PluginDescriptor.Capabilities",
		Subject:     []string{"node", "pipeline-step"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "step", Type: "string"},
		},
		Returns: "[]string",
		Doc: "StepPluginCompatibleCapabilities are the provider capabilities a " +
			"pipeline step's plugin declares its step is compatible with, in " +
			"declared order.\n\nAn empty result is a claim rather than a " +
			"missing value: a plugin declaring no capability claims " +
			"compatibility with nothing, so a rule asking whether the provider " +
			"a step runs under is among the declared set refuses an empty " +
			"declaration instead of treating it as unconstrained.\n\nThe set " +
			"is the step plugin's own declaration about itself. Which provider " +
			"a step actually runs under is a separate question on a separate " +
			"carrier, so a rule matching the two composes this fact with that " +
			"one rather than asking a single fact to span both surfaces.",
	},
	{
		Name:        "step-export-scope",
		Stage:       "described",
		SourceField: "wasm.PluginExport.Scope",
		Subject:     []string{"node", "pipeline-step"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "step", Type: "string"},
		},
		Returns: "string",
		Doc: "StepExportScope is the document root the export a pipeline step " +
			"dispatches to declares it operates on, empty when the export " +
			"declares none.\n\nEmptiness is one answer rather than two. An " +
			"export declaring no scope and a plugin declaring no such export " +
			"both leave the scope unstated, and the question a rule asks is " +
			"which document set the step addresses; distinguishing the two " +
			"would put a second condition on one question, and the two could " +
			"then disagree.\n\nIt is keyed per step rather than per plugin " +
			"because a plugin offers several exports and a step dispatches to " +
			"exactly one of them, so the plugin alone does not determine the " +
			"answer.",
	},
	{
		Name:        "provider-boundary-assembly",
		Stage:       "described",
		SourceField: "wasm.ProviderContract.BoundaryAssembly",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "string",
		Doc: "ProviderBoundaryAssembly is how the rendering provider a node " +
			"resolves to declares it combines a dispatch group's per-node " +
			"output, empty when it declares no assembly claim.\n\nThe empty " +
			"value is an answer and not a missing one: a provider stating no " +
			"assembly claim writes each node's output directly at the dispatch " +
			"path, which is exactly what distinguishes it from one that folds " +
			"the per-node fragments into a single document and from one that " +
			"nests each node under its own position.\n\nThe fact names the " +
			"claim of the node's OWN provider. Which provider assembles a whole " +
			"dispatch group is a different question, and not this fact: a group " +
			"may hold nodes whose providers resolve at different versions and " +
			"one of them is elected for the group, so the elected claim is not " +
			"a function of any single node in the group. A rule about a group " +
			"needs that election, which is keyed by the group rather than by a " +
			"node.",
	},
	{
		Name:        "provider-cross-boundary-linking",
		Stage:       "described",
		SourceField: "wasm.ProviderContract.CrossBoundaryLinking",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "bool",
		Doc: "ProviderCrossBoundaryLinking reports whether the rendering " +
			"provider a node resolves to advertises cross-boundary linking: " +
			"producing its own cross-reference wiring for a connection whose " +
			"two ends assemble into different dispatch groups.\n\nIt is a " +
			"capability the provider claims about itself and says nothing about " +
			"how the claim is met. What a reference looks like, where in an " +
			"artifact it lands, and what has to be published for the far end to " +
			"resolve it are the provider's own business and are not readable " +
			"here — so a rule composing this fact with another asks whether the " +
			"capability is present and never what shape it takes.\n\nFalse is " +
			"a real answer and three states share it: a provider that makes no " +
			"such claim, a node with no bound Recipe and therefore no provider " +
			"to claim anything, and a binding the build could not resolve to a " +
			"provider at all. All three leave the connection carrying the " +
			"scalar value the injection contract already pipes, which is one " +
			"answer to the question this fact asks rather than three.\n\nIt " +
			"is keyed by the node because the claim reaches a node through the " +
			"provider its own Recipe binds, which is the same granularity the " +
			"provider itself is keyed by. A connection has two ends and each " +
			"end answers for itself, so a rule about a connection reads the " +
			"fact twice rather than asking one coordinate to speak for both.",
	},
	{
		Name:        "node-rendering-provider",
		Stage:       "described",
		SourceField: "types.RecipeSpec.Capability",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "string",
		Doc: "NodeRenderingProvider is the provider that renders a node's own " +
			"documents, as the engine kind its Recipe's declared capability " +
			"projects to.\n\nIt is keyed by the node alone, and that is the " +
			"granularity the value actually varies over: one Recipe declares one " +
			"capability for the whole node, so a per-step key would name a " +
			"coordinate the answer does not depend on and would invite a reader to " +
			"believe two steps could differ here.\n\nThe fact holds a provider and " +
			"not a part a provider plays. This one value is what a rule over a " +
			"node's own pipeline matches its steps against, and it is equally what " +
			"a rule over a pipeline attached to that node matches the attachment's " +
			"host-addressed steps against — the same provider renders the documents " +
			"in both cases. Which of those a reader is doing is a property of the " +
			"rule, so it is stated where the rule is declared and never folded into " +
			"the name here.\n\nEmptiness is a real answer: a node with no bound " +
			"Recipe renders through no provider, and a rule asking what renders its " +
			"documents has nothing to match rather than an unconstrained match.",
	},
	{
		Name:        "attachment-rendering-provider",
		Stage:       "described",
		SourceField: "types.FeatureRecipeSpec.Capability",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "string",
		Doc: "AttachmentRenderingProvider is the provider that renders one " +
			"attachment's own documents on a node, as the engine kind its bound " +
			"FeatureRecipe's declared capability projects to.\n\nIt is a separate " +
			"fact from the provider rendering the node's own documents rather than " +
			"the same fact reached differently. An attachment binds its own " +
			"FeatureRecipe, which declares its own capability, so the two values are " +
			"independently authored and a node whose attachment renders through a " +
			"different provider is ordinary rather than exceptional.\n\nIt is keyed " +
			"per attachment and not per step, for the reason the node-scoped " +
			"provider is keyed per node: one FeatureRecipe declares one capability " +
			"for every step of the pipeline it carries.\n\nEmptiness is a real " +
			"answer, as it is for the node-scoped provider: an attachment with no " +
			"bound FeatureRecipe renders through no provider.",
	},
	{
		Name:        "attachment-execution-scope",
		Stage:       "described",
		SourceField: "types.FeatureRecipeSpec.ExecutionScope",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "string",
		Doc: "AttachmentExecutionScope is the scope one attachment's bound " +
			"FeatureRecipe declares its resource generation runs at, empty when " +
			"it declares none.\n\nAn attachment whose FeatureRecipe declares the " +
			"boundary scope is evaluated once for the boundary that holds it, " +
			"however many components that boundary holds; every other attachment " +
			"is evaluated once for the component carrying it. So the value " +
			"decides which of two surfaces an attachment's own pipeline runs on, " +
			"and a rule over that pipeline reads it to be asked about the surface " +
			"the attachment actually runs on rather than about every component " +
			"the attachment reached.\n\nIt is keyed by the node as well as the " +
			"attachment because the attachment set a node carries is the merged " +
			"one: an attachment written at a boundary appears in the set of every " +
			"component that boundary holds, and the coordinate that names one of " +
			"those appearances is the node together with the attachment.\n\n" +
			"Emptiness is a real answer and is the ordinary one: a FeatureRecipe " +
			"that states no scope generates per component, which is also what an " +
			"attachment with no bound FeatureRecipe amounts to.",
	},
	{
		Name:        "attachment-step-plugin-compatible-capabilities",
		Stage:       "described",
		SourceField: "wasm.PluginDescriptor.Capabilities",
		Subject:     []string{"node", "attachment", "pipeline-step"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "step", Type: "string"},
		},
		Returns: "[]string",
		Doc: "AttachmentStepPluginCompatibleCapabilities are the provider " +
			"capabilities the plugin behind one step of an attachment's pipeline " +
			"declares its step is compatible with, in declared order.\n\nAn empty " +
			"result is a claim rather than a missing value, on the same reading the " +
			"node-scoped form carries: a plugin declaring no capability claims " +
			"compatibility with nothing.\n\nIt is keyed per attachment as well as " +
			"per node because the pipeline is authored on the FeatureRecipe the " +
			"attachment binds, which is a different document from the node's own " +
			"Recipe. Two attachments on one node carry independently authored " +
			"pipelines, and nothing makes their step identifiers distinct from each " +
			"other or from the node's own — so a key without the attachment would " +
			"answer for whichever pipeline happened to be asked last.",
	},
	{
		Name:        "attachment-step-export-scope",
		Stage:       "described",
		SourceField: "wasm.PluginExport.Scope",
		Subject:     []string{"node", "attachment", "pipeline-step"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "step", Type: "string"},
		},
		Returns: "string",
		Doc: "AttachmentStepExportScope is the document root the export one step of " +
			"an attachment's pipeline dispatches to declares it operates on, empty " +
			"when the export declares none.\n\nEmptiness is one answer rather than " +
			"two, exactly as it is for the node-scoped form: an export declaring no " +
			"scope and a plugin declaring no such export both leave the scope " +
			"unstated, and the question a rule asks is which document set the step " +
			"addresses.\n\nThe scope is what decides which provider a step is " +
			"matched against, because an attachment's pipeline reaches two document " +
			"sets: the attachment's own output, rendered by the attachment's " +
			"provider, and the host node's documents, rendered by the node's. So " +
			"this fact and the two provider facts compose, and neither provider fact " +
			"has to know which one a given step will select.",
	},
	{
		Name:        "boundary-attachment-rendering-provider",
		Stage:       "described",
		SourceField: "types.FeatureRecipeSpec.Capability",
		Subject:     []string{"boundary", "attachment"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "string",
		Doc: "BoundaryAttachmentRenderingProvider is the provider that renders one " +
			"boundary-scope attachment's documents, as the engine kind its bound " +
			"FeatureRecipe's declared capability projects to.\n\nIt is rooted at a " +
			"boundary rather than at a node because that is where the attachment is " +
			"declared: such an attachment reaches every component the boundary " +
			"holds, so keying it by a component would present one authored " +
			"attachment many times over and would have no answer at all for a " +
			"boundary holding no components.\n\nIt is keyed per attachment and not " +
			"per step, for the reason the other two provider facts are keyed at " +
			"their own granularity: one FeatureRecipe declares one capability for " +
			"every step of its pipeline.\n\nA boundary-scope attachment addresses no " +
			"host documents, because it stands against no single component — so a " +
			"rule over its pipeline matches every step against this one provider, " +
			"and the host-addressed case does not arise.",
	},
	{
		Name:        "boundary-attachment-step-plugin-compatible-capabilities",
		Stage:       "described",
		SourceField: "wasm.PluginDescriptor.Capabilities",
		Subject:     []string{"boundary", "attachment", "pipeline-step"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "step", Type: "string"},
		},
		Returns: "[]string",
		Doc: "BoundaryAttachmentStepPluginCompatibleCapabilities are the provider " +
			"capabilities the plugin behind one step of a boundary-scope " +
			"attachment's pipeline declares its step is compatible with, in " +
			"declared order.\n\nAn empty result is a claim rather than a missing " +
			"value, on the reading every form of this fact carries: a plugin " +
			"declaring no capability claims compatibility with nothing.\n\nIt is a " +
			"separate fact from the node-rooted forms rather than the same fact " +
			"reached through a different coordinate. A boundary-scope attachment's " +
			"pipeline is authored on a FeatureRecipe the operator attached at the " +
			"boundary, and the step identifiers in it are unrelated to any " +
			"component's — so the coordinates that identify it are the boundary and " +
			"the attachment, and a node-rooted key could not name it at all.",
	},
	{
		Name:        "boundary-attachment-step-export-scope",
		Stage:       "described",
		SourceField: "wasm.PluginExport.Scope",
		Subject:     []string{"boundary", "attachment", "pipeline-step"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "step", Type: "string"},
		},
		Returns: "string",
		Doc: "BoundaryAttachmentStepExportScope is the document root the export one " +
			"step of a boundary-scope attachment's pipeline dispatches to declares " +
			"it operates on, empty when the export declares none.\n\nEmptiness is " +
			"one answer rather than two, exactly as it is for every form of this " +
			"fact: an export declaring no scope and a plugin declaring no such " +
			"export both leave the scope unstated.\n\nThe scope is still worth " +
			"reading even though a boundary-scope attachment addresses only its own " +
			"documents, and that is the point of keeping it a declared fact rather " +
			"than assuming the answer. A step declaring it addresses host documents " +
			"is asking for a document set this site does not have, which is a rule's " +
			"finding about the authoring rather than a condition a rule may take for " +
			"granted.",
	},
	{
		Name:        "authored-node-provides",
		Stage:       "described",
		SourceField: "types.RecipeSpec.Provides",
		Subject:     []string{"node", "provides-key"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "key", Type: "string"},
		},
		Returns: "string",
		Doc: "AuthoredNodeProvides is the template text a node's Recipe declares " +
			"for one provides key, as written and before anything is evaluated.\n" +
			"\nIt is a separate fact from the value that key resolved to rather " +
			"than the same fact at an earlier moment, and the pair is what makes a " +
			"rule over the text possible at all. A rule that admits or refuses " +
			"what an author may write reads the text, because the resolved value " +
			"cannot answer for it: a template such a rule would refuse is exactly " +
			"one whose value is never produced.\n\nThe fact is the text and not " +
			"an analysis of it. A reference set collected from a template is a " +
			"judgement about which references it makes, and the direction of that " +
			"judgement decides which rules may read it — one that over-collects " +
			"serves a rule asking what a template consumed, and would refuse " +
			"authoring the template language permits. Handing over the text " +
			"leaves the direction to the rule that needs it.\n\nEmptiness is one " +
			"answer rather than two. The key is enumerated rather than looked up, " +
			"so it exists whenever the fact is asked, and an entry whose text is " +
			"empty leaves a rule over the text with nothing to read either way; a " +
			"second result reporting presence would put a second condition on one " +
			"question, and the two could then disagree.",
	},
	{
		Name:        "declared-locals-keys",
		Stage:       "described",
		SourceField: "types.RecipeSpec.Locals",
		Subject:     []string{"node", "locals-key"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "DeclaredLocalsKeys are the local names a node's Recipe declares, " +
			"in author order.\n\nAuthor order is the fact, not an " +
			"incidental property of it: locals are evaluated in the order they " +
			"are declared, so a rule about what a local may reference is a rule " +
			"about this order.\n\nIt is complete as soon as the Recipe bound to " +
			"the node is known, which is why it is declared here rather than " +
			"beside the values the block resolves to. The keys are authored and " +
			"the values are evaluated, and a rule that admits or refuses what an " +
			"author may write has to be able to run BEFORE the evaluation it " +
			"exists to prevent — so a rule reading this fact together with the " +
			"authored text beside it decides the whole question from " +
			"declarations.",
	},
	{
		Name:        "authored-node-locals",
		Stage:       "described",
		SourceField: "types.RecipeSpec.Locals",
		Subject:     []string{"node", "locals-key"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "key", Type: "string"},
		},
		Returns: "string",
		Doc: "AuthoredNodeLocals is the template text a node's Recipe declares " +
			"for one local, as written and before anything is evaluated.\n" +
			"\nIt is the pair of the key-order fact beside it rather than a " +
			"second reading of the same thing: that one says which locals exist " +
			"and in what order, this one says what each of them was written as, " +
			"and a rule about what a local may reference needs both. The order " +
			"decides which siblings a given entry may read; the text decides " +
			"which it does read.\n\nThe fact is the text and not an analysis " +
			"of it. A reference set collected from a template is a judgement " +
			"about which references it makes, and the direction of that " +
			"judgement decides which rules may read it — one that over-collects " +
			"serves a rule asking what a template consumed, and would refuse " +
			"authoring the template language permits. Handing over the text " +
			"leaves the direction to the rule that needs it, which for this " +
			"surface is the refusing direction.\n\nEmptiness is one answer " +
			"rather than two. The key is enumerated rather than looked up, so it " +
			"exists whenever the fact is asked, and an entry whose text is empty " +
			"leaves a rule over the text with nothing to read either way; a " +
			"second result reporting presence would put a second condition on " +
			"one question, and the two could then disagree.",
	},
	{
		Name:        "attachment-declared-locals-keys",
		Stage:       "described",
		SourceField: "types.FeatureRecipeSpec.Locals",
		Subject:     []string{"node", "attachment", "locals-key"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "[]string",
		Doc: "AttachmentDeclaredLocalsKeys are the local names the FeatureRecipe " +
			"one attachment binds on a node declares, in author order.\n\nAuthor " +
			"order is the fact, not an incidental property of it: the entries are " +
			"evaluated in the order they are written, so a rule about what one of " +
			"them may reference is a rule about this order.\n\nIt is keyed per " +
			"attachment as well as per node because the block is authored on the " +
			"FeatureRecipe the attachment binds, a different document from the " +
			"node's own Recipe. Two attachments on one node therefore carry " +
			"independently authored locals blocks whose key names are chosen with " +
			"no knowledge of each other, so a key carrying only the node would " +
			"answer for whichever block was asked last.\n\nThe attachment's block " +
			"is its OWN, and that is what makes the answer usable: the node's " +
			"resolved locals are merged over it after it has resolved, so they are " +
			"not readable from inside it, and a set folding them in would report a " +
			"reference as legitimate that the evaluation refuses.\n\nIt is " +
			"complete as soon as the FeatureRecipe the attachment binds is known, " +
			"which is why it is declared here rather than beside the values the " +
			"block resolves to. The keys are authored and the values are " +
			"evaluated, and a rule that admits or refuses what an author may write " +
			"has to be able to run BEFORE the evaluation it exists to prevent.",
	},
	{
		Name:        "attachment-authored-locals",
		Stage:       "described",
		SourceField: "types.FeatureRecipeSpec.Locals",
		Subject:     []string{"node", "attachment", "locals-key"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "key", Type: "string"},
		},
		Returns: "string",
		Doc: "AttachmentAuthoredLocals is the template text the FeatureRecipe one " +
			"attachment binds on a node declares for one local, as written and " +
			"before anything is evaluated.\n\nIt is the pair of the key-order fact " +
			"beside it rather than a second reading of the same thing: that one " +
			"says which locals exist and in what order, this one says what each of " +
			"them was written as, and a rule about what a local may reference " +
			"needs both. The order decides which siblings a given entry may read; " +
			"the text decides which it does read.\n\nIt is keyed per attachment as " +
			"well as per node for the reason the order fact beside it is: the " +
			"block belongs to the FeatureRecipe rather than to the node, and two " +
			"attachments on one node write theirs independently.\n\nThe fact is " +
			"the text and not an analysis of it. A reference set collected from a " +
			"template is a judgement about which references it makes, and the " +
			"direction of that judgement decides which rules may read it — one " +
			"that over-collects serves a rule asking what a template consumed, and " +
			"would refuse authoring the template language permits. Handing over " +
			"the text leaves the direction to the rule that needs it, which for " +
			"this surface is the refusing direction.\n\nEmptiness is one answer " +
			"rather than two. The key is enumerated rather than looked up, so it " +
			"exists whenever the fact is asked, and an entry whose text is empty " +
			"leaves a rule over the text with nothing to read either way.",
	},
	{
		Name:        "boundary-attachment-declared-locals-keys",
		Stage:       "described",
		SourceField: "types.FeatureRecipeSpec.Locals",
		Subject:     []string{"boundary", "attachment", "locals-key"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "[]string",
		Doc: "BoundaryAttachmentDeclaredLocalsKeys are the local names the " +
			"FeatureRecipe one boundary-scope attachment binds declares, in author " +
			"order.\n\nAuthor order is the fact for the reason it is on every form " +
			"of this question: the entries are evaluated in the order they are " +
			"written, so a rule about what one of them may reference is a rule " +
			"about this order.\n\nIt is rooted at a boundary rather than at a node " +
			"because that is where the attachment is declared. Such an attachment " +
			"is written once and reaches every component the boundary holds, so a " +
			"node-rooted key would present one authored block once per component — " +
			"the same text answered for as many times as the boundary has members " +
			"— and would answer for it not at all where the boundary holds none, " +
			"which is silence a rule cannot tell from approval.\n\nThe block is " +
			"the attachment's own and carries no component's locals, because such " +
			"an attachment stands against no single component: there is no node " +
			"whose Recipe could contribute one.",
	},
	{
		Name:        "boundary-attachment-authored-locals",
		Stage:       "described",
		SourceField: "types.FeatureRecipeSpec.Locals",
		Subject:     []string{"boundary", "attachment", "locals-key"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "key", Type: "string"},
		},
		Returns: "string",
		Doc: "BoundaryAttachmentAuthoredLocals is the template text the " +
			"FeatureRecipe one boundary-scope attachment binds declares for one " +
			"local, as written and before anything is evaluated.\n\nIt is the pair " +
			"of the key-order fact beside it: that one says which locals exist and " +
			"in what order, this one says what each was written as, and a rule " +
			"about what a local may reference needs both.\n\nIt is rooted at a " +
			"boundary for the reason the order fact beside it is, and the two are " +
			"read together over one enumerated key.\n\nThe fact is the text and " +
			"not an analysis of it, on the same reading every authored-text fact " +
			"carries: a collected reference set is a judgement whose direction " +
			"decides which rules may read it, and handing over the text leaves " +
			"that direction to the rule that needs it.\n\nEmptiness is one answer " +
			"rather than two. The key is enumerated rather than looked up, so it " +
			"exists whenever the fact is asked.",
	},
	{
		Name:        "provider-artifact-template-delimiters",
		Stage:       "described",
		SourceField: "wasm.ProviderContract.ArtifactDelimiters",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "ArtifactTemplateDelimiters",
		Doc: "ProviderArtifactTemplateDelimiters is the delimiter pair the " +
			"provider rendering a node's documents declares its artifact " +
			"entries are read with, so that a target syntax colliding with the " +
			"usual pair is left untouched.\n\nA provider declaring no pair is " +
			"answered with the usual pair rather than with an absence, and that " +
			"is the fact rather than a convenience. What a rule needs is the " +
			"pair an entry will actually be read with; a rule handed an absence " +
			"would have to supply the fallback itself, so every rule would " +
			"carry its own copy of a decision the production has already " +
			"made.\n\nIt is a provider's own self-report, so it is complete " +
			"once every plugin a node resolves to has described itself and " +
			"before any step runs. That is earlier than the artifact text the " +
			"pair is applied to, and the two are deliberately separate facts at " +
			"separate stages: the pair is a property of the provider, the text a " +
			"property of the Recipe, and a rule over an entry reads both.",
	},
	{
		Name:        "attachment-provider-artifact-template-delimiters",
		Stage:       "described",
		SourceField: "wasm.ProviderContract.ArtifactDelimiters",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "ArtifactTemplateDelimiters",
		Doc: "AttachmentProviderArtifactTemplateDelimiters is the delimiter pair " +
			"the provider rendering one attachment's documents on a node " +
			"declares its artifact entries are read with.\n\nA provider " +
			"declaring no pair is answered with the usual pair rather than with " +
			"an absence, on the same reading the node-scoped form carries: what " +
			"a rule needs is the pair an entry will actually be read with, so " +
			"the fallback is made here once rather than in every rule.\n\nIt " +
			"is a separate fact from the pair the node's own provider declares, " +
			"and the separation is what makes a rule over an attachment's " +
			"artifact text correct rather than approximate. An attachment binds " +
			"its own FeatureRecipe, which declares its own capability, so the " +
			"provider reading the attachment's entries is independently authored " +
			"from the one reading the node's — and read with the wrong pair, a " +
			"template's downstream target syntax surfaces as references it does " +
			"not make. A rule handed the node's pair would therefore report an " +
			"attachment's entry against a syntax nothing applied to it.\n\nIt " +
			"is keyed per attachment and not per entry, because one FeatureRecipe " +
			"binds one provider for every entry in its artifacts block. Which " +
			"entry a pair is applied to is the reading rule's question and is not " +
			"a coordinate of this one.",
	},
	{
		Name:        "boundary-attachment-provider-artifact-template-delimiters",
		Stage:       "described",
		SourceField: "wasm.ProviderContract.ArtifactDelimiters",
		Subject:     []string{"boundary", "attachment"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "ArtifactTemplateDelimiters",
		Doc: "BoundaryAttachmentProviderArtifactTemplateDelimiters is the " +
			"delimiter pair the provider rendering one boundary-scope " +
			"attachment's documents declares its artifact entries are read " +
			"with.\n\nA provider declaring no pair is answered with the usual " +
			"pair rather than with an absence, on the reading every form of this " +
			"fact carries.\n\nIt is rooted at a boundary rather than at a node " +
			"because that is where the attachment is declared: such an " +
			"attachment reaches every component the boundary holds, so keying it " +
			"by a component would present one authored attachment many times " +
			"over and would have no answer at all for a boundary holding no " +
			"components.\n\nIt is a separate fact from the two node-rooted " +
			"forms rather than either of them reached through a different " +
			"coordinate. The boundary-scope attachment binds its own " +
			"FeatureRecipe and therefore its own provider, and it stands against " +
			"no single component — so neither a node's own pair nor the pair of " +
			"an attachment on some component is the pair its entries are read " +
			"with, and no node-rooted key can name it.",
	},

	// ---------------------------------------------------------------- hydrated

	{
		Name:        "resolved-node-provides",
		Stage:       "hydrated",
		SourceField: "renderer.NodeProvides",
		Subject:     []string{"node", "provides-key"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "key", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "ResolvedNodeProvides is the value a node's Recipe resolved for one " +
			"provides key, and whether the key was resolved at all.",
	},
	{
		Name:        "values-mapping-template-refs",
		Stage:       "hydrated",
		SourceField: "gotmpl.CollectTemplateFieldAccesses(valuesMapping.Inline)",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "([]string, bool)",
		Doc: "ValuesMappingTemplateRefs are the context paths a node's inline " +
			"values-mapping template reads, and whether the node's mapping " +
			"is authored inline at all.\n\nThe second result is " +
			"load-bearing. A mapping authored as an import carries no inline " +
			"text, so its reference set is absent rather than empty, and a " +
			"rule that could not tell the two apart would report every " +
			"value unread for exactly those nodes whose mapping it never " +
			"read.\n\nThe reference set over-collects: it records a read " +
			"inside a rebound body, and both halves of a chained " +
			"expression. That direction is part of the fact rather than an " +
			"incidental property of it. A rule asking which values were " +
			"consumed must count a key reached through a rebinding as read, " +
			"because one it failed to attribute would be reported unread " +
			"and the author would be told to remove a key the template " +
			"uses. A rule that instead admits or refuses authoring needs " +
			"the opposite direction and must not read this fact, since " +
			"attributing a reference wrongly refuses authoring the " +
			"template language permits.\n\nThe paths come back sorted. A " +
			"template has no single reading order to preserve, so sorting " +
			"is what makes the result the same on every run.",
	},
	{
		Name:        "resolved-node-source-declared",
		Stage:       "hydrated",
		SourceField: "types.RecipeSpec.Source",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "bool",
		Doc: "ResolvedNodeSourceDeclared reports whether a node's Recipe carries " +
			"a source once source resolution has run.\n\nThe value read is the " +
			"RESOLVED source and not the text the author wrote. Source " +
			"resolution replaces the Recipe with a local copy whose source " +
			"holds the evaluated result, and that copy is what everything " +
			"downstream reads — so a source template evaluating to an empty " +
			"string is reported the same as an absent source. That is one " +
			"condition rather than two, which is what lets a rule about a " +
			"template reading the source pillar name a single cause instead of " +
			"distinguishing two states an author cannot tell apart from the " +
			"outcome.\n\nIt is a yes-or-no answer rather than the source value, " +
			"and that follows from what a source may be rather than from a " +
			"narrowing for convenience. A source is either a location string or " +
			"an open structured object whose keys the provider defines, so " +
			"handing the value over would hand a rule a structure to walk into " +
			"— and what it would find there is provider-defined content the " +
			"framework declares no fact for.",
	},
	{
		Name:        "parsed-node-workload-present",
		Stage:       "hydrated",
		SourceField: "renderer.PhaseBState.ParsedWorkload",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "bool",
		Doc: "ParsedNodeWorkloadPresent reports whether a node's render state " +
			"carries a workload.\n\nA Recipe declaring no workload body reaches " +
			"artifact evaluation with none, because workload synthesis had " +
			"nothing to parse. So this is what separates a node whose templates " +
			"may read the workload pillar from one whose reference to it has " +
			"nothing to resolve against.\n\nIt is a yes-or-no answer rather " +
			"than the workload itself, on the same reading the resolved source " +
			"carries: the workload is a structured document, and a rule handed " +
			"it could walk into content the framework declares no fact for.",
	},
	{
		Name:        "node-inject-deliveries",
		Stage:       "hydrated",
		SourceField: "types.InjectPipe",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]InjectDelivery",
		Doc: "NodeInjectDeliveries are the deliveries a node's injection pipe " +
			"carries once every attachment attached to that node has delivered " +
			"into it, one entry per mechanism and key, each naming whatever " +
			"attachment asked for it and whether a value arrived — in delivery " +
			"order, which is the order the deliveries were made in.\n\nThree " +
			"authoring surfaces deliver into one pipe: the component's own " +
			"injection block, the blocks on the entries of its dependency " +
			"block, and the block on each attachment attached to it. The pipe " +
			"is one keyed space shared between them, so a key two surfaces " +
			"deliver under is a contradiction rather than a precedence " +
			"question — and which surface asked is the only thing that says " +
			"whose authoring has to change. The attribution is therefore part " +
			"of the fact rather than something a reader could recover: the " +
			"pipe itself holds values under keys and remembers nothing about " +
			"where a key came from.\n\nThe first two surfaces are not told " +
			"apart. A duplicate between an attachment and either of them is " +
			"one condition with one remedy — remove the attachment's entry or " +
			"the other one — and the entry that names no attachment is what " +
			"says the other one is not an attachment's.\n\nDelivery order is " +
			"the fact and not an incidental property of it. A pipe is filled " +
			"in a fixed sequence — the component's own block, then its " +
			"connections, then its attachments in the order they are attached, " +
			"and within one attachment the three named mechanisms and then the " +
			"rest in name order — and the first delivery under a key is the one " +
			"that holds it. So a rule about a key delivered twice is a rule " +
			"about this order, and answering in any other would name the wrong " +
			"surface as the one that has to change.\n\nIt is empty for a node " +
			"whose pipe carries nothing, which is the ordinary answer for a " +
			"component that delivers nothing into its own render context.",
	},
	{
		Name:        "attachment-inject-mapping-mechanisms",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.InjectMapping",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "[]InjectMappingMechanism",
		Doc: "AttachmentInjectMappingMechanisms are the delivery mechanisms " +
			"the host component's own injection block declares, one entry " +
			"per mechanism, each naming the FeatureRecipe whose injectMapping " +
			"was evaluated for this attachment and whether that mapping's " +
			"rendered output carries a top-level key for the mechanism — in " +
			"the order the component declared the mechanisms.\n\nA " +
			"FeatureRecipe's injectMapping rewrites the pipe the host " +
			"component delivers into, and it names the mechanisms it writes " +
			"as the top-level keys of its own output. So a component " +
			"declaring a mechanism the mapping's output does not name has " +
			"asked for a delivery this attachment silently drops, and the " +
			"pair of names is what says so. Neither name is recoverable " +
			"afterwards: the merge folds the output into one pipe and the " +
			"pipe remembers no mapping, while the mapping's delivery key is " +
			"itself a template, so only the RENDERED output says which " +
			"mechanisms it filled.\n\nIt is keyed per attachment because " +
			"each attachment carries its own mapping and each is evaluated " +
			"against the same component's declarations, so two attachments " +
			"on one node answer independently.\n\nIt is empty for an " +
			"attachment binding a FeatureRecipe that declares no " +
			"injectMapping, for one whose mapping rendered nothing, and for " +
			"one whose output is a flat delivery dictionary rather than a " +
			"mechanism-keyed document — a flat output names no mechanism at " +
			"all, so it makes no claim about the component's declarations to " +
			"read against. It is empty for a component that declares no " +
			"injection block of its own, which is the ordinary answer.",
	},
	{
		Name:        "artifact-template-text",
		Stage:       "hydrated",
		SourceField: "types.RecipeSpec.Artifacts",
		Subject:     []string{"node", "artifact"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "string",
		Doc: "ArtifactTemplateText is the template text standing at one of a " +
			"node's artifacts, as it is before anything evaluates " +
			"it.\n\nIt is a separate fact from the bytes that artifact " +
			"projected to, and the pair is two questions rather than one " +
			"question under two names. THIS ONE answers what the entry says, " +
			"which a rule admitting or refusing authoring has to read, because " +
			"a template such a rule refuses is exactly one whose bytes are " +
			"never produced. THE OTHER answers what evaluating it produced, " +
			"which is unanswerable until evaluation has run.\n\nThat " +
			"distinction is worth stating here because nothing else states it. " +
			"The two read the same authoring block at the same coordinates and " +
			"differ in nothing but the stage and whether evaluation has " +
			"happened — and the report of facts sharing a carrier pairs them by " +
			"stage, carrier and subject together, so it does not pair these two " +
			"and never will. A reader who suspects duplication has this " +
			"paragraph and nothing else to settle it with.\n\nThe text is the " +
			"entry's content whichever form the entry took: written inline, " +
			"supplied by an imported file, or supplied by an entry of a library " +
			"bundle. A rule asking what an artifact template may reference asks " +
			"the same question of all three, so one fact over the three is what " +
			"keeps such a rule from having to know which form it was handed.",
	},
	{
		Name:        "artifact-recorded-content-pin",
		Stage:       "hydrated",
		SourceField: "types.RecipeSpec.ArtifactDigests",
		Subject:     []string{"node", "artifact"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "ArtifactRecordedContentPin is the content digest a node's Recipe " +
			"recorded for one of its artifacts, and whether the entry carries a " +
			"recorded pin at all.\n\nThe second result is load-bearing and is not " +
			"a convenience over an empty string. A Recipe that was never frozen " +
			"records no pin, an entry whose authoring form carries none records " +
			"none either, and a rule that could not tell those from a pin whose " +
			"value happened to be empty would either refuse every unfrozen " +
			"Recipe or admit a pin it never compared.\n\nA pin is recorded for " +
			"exactly one authoring form: an entry supplied by an imported file " +
			"from the workspace. An inline entry is part of the entity, so its " +
			"content is already pinned by whatever pins the entity; an entry " +
			"supplied by a library bundle is pinned by the version that bundle " +
			"was resolved at rather than by a file digest; and a reference " +
			"carrying a registry host is refused where it is consumed and never " +
			"reaches this fact. So the form gate lives in the production, at the " +
			"one site that knows which form the walk resolved, rather than in " +
			"each reader — a rule handed the pin plus a second fact naming the " +
			"form would be free to pair them wrongly, and the pairing is the " +
			"whole of what decides whether a comparison is owed.\n\nIt is " +
			"declared beside the entry's template text and keyed the same way, " +
			"because the two are the two halves of one comparison and a reader " +
			"holding one without the other has nothing to compare. The " +
			"coordinate is the name the entry resolved to, which for an imported " +
			"entry is the authored key itself — so a bundle-supplied output, " +
			"whose coordinate is the key with the bundle's own filename nested " +
			"under it, matches no recorded pin, which is the same answer its " +
			"authoring form gives.",
	},
	{
		Name:        "attachment-resolved-source-declared",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.Source",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "bool",
		Doc: "AttachmentResolvedSourceDeclared reports whether the FeatureRecipe " +
			"one attachment binds on a node carries a source once source " +
			"resolution has run.\n\nThe value read is the RESOLVED source and " +
			"not the text the author wrote, on the same reading the node-scoped " +
			"form carries: a source template evaluating to an empty string " +
			"leaves the attachment with no source just as an absent source does, " +
			"and one condition covers both so a rule can name a single cause.\n" +
			"\nIt is a separate fact from the node's own resolved source rather " +
			"than the same fact reached differently, and this is the pair where " +
			"the difference decides a verdict rather than a filing. The " +
			"attachment's render runs under the host component's node identity " +
			"over a source the FeatureRecipe declares, so the two answers are " +
			"independently authored and routinely differ: asked of the node, a " +
			"host that declares a source would have an attachment's reference to " +
			"an absent one admitted, and a host that declares none would have a " +
			"legitimate attachment reference refused. Both directions are wrong " +
			"and neither is visible from the outcome, which is why the " +
			"attachment coordinate is in the key.\n\nIt is a yes-or-no answer " +
			"rather than the source value, on the reading the node-scoped form " +
			"carries: a source is either a location string or an open structured " +
			"object whose keys the provider defines, so handing the value over " +
			"would hand a rule provider-defined content the framework declares " +
			"no fact for.",
	},
	{
		Name:        "attachment-parsed-workload-present",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.Workload",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "bool",
		Doc: "AttachmentParsedWorkloadPresent reports whether the render of one " +
			"attachment on a node carries a workload.\n\nA FeatureRecipe " +
			"declaring no workload body reaches artifact evaluation with none, " +
			"because workload synthesis had nothing to parse. So this is what " +
			"separates an attachment whose artifact templates may read the " +
			"workload pillar from one whose reference to it has nothing to " +
			"resolve against.\n\nIt is a separate fact from the node's own " +
			"parsed workload for the reason the attachment's resolved source is: " +
			"the attachment renders under the host component's node identity over " +
			"a workload the FeatureRecipe declares, so a key without the " +
			"attachment would answer about the host's pillars while the template " +
			"being read is the attachment's. Two attachments on one node are " +
			"likewise independently authored, and one declaring a workload says " +
			"nothing about the other.\n\nIt is a yes-or-no answer rather than " +
			"the workload itself, on the reading the node-scoped form carries: " +
			"the workload is a structured document, and a rule handed it could " +
			"walk into content the framework declares no fact for.",
	},
	{
		Name:        "attachment-artifact-template-text",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.Artifacts",
		Subject:     []string{"node", "attachment", "artifact"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "string",
		Doc: "AttachmentArtifactTemplateText is the template text standing at one " +
			"artifact of the FeatureRecipe one attachment binds on a node, as it " +
			"is before anything evaluates it.\n\nIt is keyed per attachment as " +
			"well as per node because the entry is authored on the FeatureRecipe " +
			"the attachment binds, a different document from the node's own " +
			"Recipe. Two attachments on one node therefore carry independently " +
			"authored artifacts blocks, and nothing makes their output filenames " +
			"distinct from each other or from the node's own — so the attachment " +
			"is what tells one block from another, and a key carrying only the " +
			"node and the artifact would answer for whichever was asked last.\n" +
			"\nIt is the authored text and not the bytes the entry projected to, " +
			"which is the same division the node-scoped pair draws: THIS ONE " +
			"answers what the entry says, which a rule admitting or refusing " +
			"authoring has to read, because a template such a rule refuses is " +
			"exactly one whose bytes are never produced.\n\nThe text is the " +
			"entry's content whichever form the entry took: written inline, " +
			"supplied by an imported file, or supplied by an entry of a library " +
			"bundle. A rule asking what an artifact template may reference asks " +
			"the same question of all three.",
	},
	{
		Name:        "attachment-artifact-recorded-content-pin",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.ArtifactDigests",
		Subject:     []string{"node", "attachment", "artifact"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "AttachmentArtifactRecordedContentPin is the content digest the " +
			"FeatureRecipe one attachment binds on a node recorded for one of its " +
			"artifacts, and whether the entry carries a recorded pin at all.\n\n" +
			"Every reading the node-scoped form carries applies here over a " +
			"different document: the second result separates an unfrozen " +
			"FeatureRecipe and an entry whose form carries no pin from a pin whose " +
			"value is empty, and a pin is recorded for exactly one authoring form, " +
			"the entry supplied by an imported file.\n\nIt is keyed by the " +
			"attachment as well as by the node for the reason the text fact beside " +
			"it is: the attachment's render walks the same producer under the HOST " +
			"component's node identity, so a pin keyed by the node alone would " +
			"measure the attachment's own content against whatever the host's " +
			"Recipe happened to record — and two attachments on one node record " +
			"independently, with nothing making their output names distinct.",
	},
	{
		Name:        "boundary-attachment-resolved-source-declared",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.Source",
		Subject:     []string{"boundary", "attachment"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "bool",
		Doc: "BoundaryAttachmentResolvedSourceDeclared reports whether the " +
			"FeatureRecipe one boundary-scope attachment binds carries a source " +
			"once source resolution has run.\n\nThe value read is the RESOLVED " +
			"source and not the text the author wrote, on the reading every form " +
			"of this fact carries: a source template evaluating to an empty " +
			"string leaves the attachment with no source just as an absent " +
			"source does.\n\nIt is rooted at a boundary rather than at a node " +
			"because that is where the attachment is declared, and it reaches " +
			"every component the boundary holds — so a component-rooted key would " +
			"present one authored attachment many times over and would have no " +
			"answer at all for a boundary holding no components.\n\nIt is a " +
			"separate fact from the node-rooted forms rather than either of them " +
			"reached through a different coordinate. A boundary-scope attachment " +
			"stands against no single component, so no component's resolved " +
			"source is its own, and no node-rooted key can name it.\n\nIt is " +
			"a yes-or-no answer rather than the source value, for the reason " +
			"every form of this fact is: a source may be an open structured " +
			"object whose keys the provider defines, and the framework declares " +
			"no fact for what is inside it.",
	},
	{
		Name:        "boundary-attachment-parsed-workload-present",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.Workload",
		Subject:     []string{"boundary", "attachment"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "bool",
		Doc: "BoundaryAttachmentParsedWorkloadPresent reports whether the render " +
			"of one boundary-scope attachment carries a workload.\n\nA " +
			"FeatureRecipe declaring no workload body reaches artifact " +
			"evaluation with none, because workload synthesis had nothing to " +
			"parse. So this is what separates a boundary-scope attachment whose " +
			"artifact templates may read the workload pillar from one whose " +
			"reference to it has nothing to resolve against.\n\nIt is rooted " +
			"at a boundary for the reason the boundary-scope resolved source is: " +
			"the attachment is declared once at the boundary and reaches every " +
			"component the boundary holds, so a component-rooted key would " +
			"present one authored workload declaration many times over.\n\nIt " +
			"is a yes-or-no answer rather than the workload itself, on the " +
			"reading every form of this fact carries: the workload is a " +
			"structured document, and a rule handed it could walk into content " +
			"the framework declares no fact for.",
	},
	{
		Name:        "boundary-attachment-artifact-template-text",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.Artifacts",
		Subject:     []string{"boundary", "attachment", "artifact"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "string",
		Doc: "BoundaryAttachmentArtifactTemplateText is the template text " +
			"standing at one artifact of the FeatureRecipe one boundary-scope " +
			"attachment binds, as it is before anything evaluates it.\n\nIt is " +
			"rooted at a boundary rather than at a node because that is where the " +
			"attachment is declared. Such an attachment is written once and " +
			"reaches every component the boundary holds, so a component-rooted " +
			"key would offer one authored entry once per component — the same " +
			"authored text under as many keys as the boundary has members — and " +
			"would offer it not at all for a boundary holding none.\n\nIt is a " +
			"separate fact from the node-rooted forms rather than either of them " +
			"reached differently. The output filenames in this block are chosen " +
			"independently of any component's and of any other attachment's, and " +
			"nothing declares them unique across documents, so the coordinates " +
			"that identify one entry are the boundary and the attachment together " +
			"with the entry's own name.\n\nIt is the authored text and not the " +
			"bytes the entry projected to, which is the division every form of " +
			"this fact draws: a rule admitting or refusing authoring reads the " +
			"text, because a template such a rule refuses is exactly one whose " +
			"bytes are never produced.\n\nThe text is the entry's content " +
			"whichever form the entry took: written inline, supplied by an " +
			"imported file, or supplied by an entry of a library bundle.",
	},
	{
		Name:        "boundary-attachment-artifact-recorded-content-pin",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.ArtifactDigests",
		Subject:     []string{"boundary", "attachment", "artifact"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "BoundaryAttachmentArtifactRecordedContentPin is the content digest " +
			"the FeatureRecipe one boundary-scope attachment binds recorded for one " +
			"of its artifacts, and whether the entry carries a recorded pin at " +
			"all.\n\nEvery reading the two node-rooted forms carry applies here: " +
			"the second result separates an unfrozen FeatureRecipe and an entry " +
			"whose form carries no pin from a pin whose value is empty, and a pin " +
			"is recorded for exactly one authoring form, the entry supplied by an " +
			"imported file.\n\nIt is rooted at a boundary because that is where the " +
			"attachment is declared, on the reading the text fact beside it gives: " +
			"such an attachment is written once and reaches every component the " +
			"boundary holds, so a component-rooted key would hold one recorded pin " +
			"under as many keys as the boundary has members and none at all for a " +
			"boundary holding none.",
	},

	// ----------------------------------------------------------------- emitted

	{
		Name:        "emitted-file-origin-set",
		Stage:       "emitted",
		SourceField: "renderer.PhaseBState.EmittedFiles",
		Subject:     []string{"node", "path"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "path", Type: "string"},
		},
		Returns: "[]string",
		Doc: "EmittedFileOriginSet are the authored input blocks that produced " +
			"the file standing at one output name within one node, " +
			"deduplicated, in the order the render produced them.\n\nA Recipe " +
			"can produce a file from more than one block — a " +
			"source, a workload, an artifacts entry, and a step of a pipeline " +
			"each contribute under a name chosen independently of the others — " +
			"and nothing declares those names distinct from one another. So more " +
			"than one entry here means two blocks of one Recipe each produced a " +
			"file under one name, which is a state only the whole set makes " +
			"visible: from either block alone the name looks unclaimed.\n\nOne " +
			"entry is the ordinary answer, and the ordinary answer is not the " +
			"empty one: a name no block produced is a name the node did not " +
			"emit, so the coordinate does not exist and no rule is invoked for " +
			"it.\n\nIt is the origins and not the bytes, and that is the whole " +
			"of what this fact carries. What separates two blocks meeting on one " +
			"name from one block emitting once is which blocks arrived, so a " +
			"rule reading this needs no content — and a rule that could reach " +
			"the bytes could compare them, which would let two blocks agreeing " +
			"by accident pass as though the name had been claimed once.\n\nThe " +
			"order is stated rather than inherited. A node's blocks are read " +
			"in a fixed sequence and each contributes by sorted name, so the " +
			"sequence this returns is a function of the build alone; the first " +
			"entry is the block that reached the name first, which is what " +
			"tells an author which of two blocks to rename.\n\nThe set counts a file the " +
			"node's provider will hand to an assembly rather than write out " +
			"directly. Two blocks meeting on one name have already collided in " +
			"the node's own output, whichever of the two an assembly later " +
			"consumes — so excluding such a file would answer emptily for " +
			"exactly the providers that combine their nodes' output.\n\nIt is " +
			"keyed by the node and the output name, before anything is grouped " +
			"for assembly. The output paths a provider wrote within a boundary " +
			"are a separate fact at a later stage, and neither is derivable from " +
			"the other: this one is one node's own emission under the names that " +
			"node chose, and that one is what several nodes' output became once " +
			"a group assembled it, under paths an assembly may have renamed, " +
			"nested or merged away.",
	},
	{
		Name:        "attachment-emitted-file-origin-set",
		Stage:       "emitted",
		SourceField: "renderer.PhaseBState.EmittedFiles",
		Subject:     []string{"node", "attachment", "path"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "path", Type: "string"},
		},
		Returns: "[]string",
		Doc: "AttachmentEmittedFileOriginSet are the authored input blocks that " +
			"produced the file standing at one output name within the render one " +
			"attachment carries on a node, deduplicated, in the order that render " +
			"produced them.\n\nIt is the carrier-keyed form of the node's own " +
			"origin set and answers the same question of a different document. A " +
			"FeatureRecipe can produce a file from more than one block — a source, " +
			"a workload, an artifacts entry, and a step of a pipeline each " +
			"contribute under a name chosen independently of the others — so more " +
			"than one entry here means two blocks of one FeatureRecipe each " +
			"produced a file under one name, which is a state only the whole set " +
			"makes visible.\n\nIt is keyed per attachment as well as per node " +
			"because the blocks are authored on the FeatureRecipe the attachment " +
			"binds, a different document from the node's own Recipe. The render " +
			"runs under the HOST component's node identity, so a key carrying only " +
			"the node would answer about the host's own emission while the blocks " +
			"being read are the attachment's — admitting a collision the attachment " +
			"never wrote wherever the host has one, and refusing none of the " +
			"attachment's own. Both directions are wrong and neither is visible " +
			"from the outcome. Two attachments on one node are likewise " +
			"independently authored, and nothing makes one's output names distinct " +
			"from the other's.\n\nIt is a separate fact rather than the node's own " +
			"set reached through a wider enumeration, because widening an " +
			"enumerator cannot supply a coordinate the fact is not keyed by: the " +
			"attachment is what tells one block from another, and no enumeration " +
			"over nodes and names produces it.\n\nIt is the origins and not the " +
			"bytes, on the reading the node's own form carries: what separates two " +
			"blocks meeting on one name from one block emitting once is which " +
			"blocks arrived, and a rule that could reach the bytes could compare " +
			"them, which would let two blocks agreeing by accident pass as though " +
			"the name had been claimed once.\n\nThe order is stated rather than " +
			"inherited, for the reason the node's own form gives: the blocks are " +
			"read in a fixed sequence and each contributes by sorted name, so the " +
			"first entry is the block that reached the name first.\n\nThe name is " +
			"the one the render itself chose, before the caller places the output " +
			"under the attachment's own prefix. A collision between two of this " +
			"render's blocks has already happened under that name, whatever prefix " +
			"is later composed onto it.",
	},
	{
		Name:        "boundary-attachment-emitted-file-origin-set",
		Stage:       "emitted",
		SourceField: "renderer.PhaseBState.EmittedFiles",
		Subject:     []string{"boundary", "attachment", "path"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "path", Type: "string"},
		},
		Returns: "[]string",
		Doc: "BoundaryAttachmentEmittedFileOriginSet are the authored input blocks " +
			"that produced the file standing at one output name within the render " +
			"one boundary-scope attachment carries, deduplicated, in the order that " +
			"render produced them.\n\nIt answers the same question its two sibling " +
			"forms answer of their own documents: more than one entry means two " +
			"blocks of one FeatureRecipe each produced a file under one name, which " +
			"is a state only the whole set makes visible.\n\nIt is rooted at a " +
			"boundary rather than at a node because that is where such an " +
			"attachment is declared. It is written once and reaches every component " +
			"the boundary holds, so a component-rooted key would hold one authored " +
			"document under as many keys as the boundary has members, and none at " +
			"all for a boundary holding none. The render carries a composite " +
			"dispatch marker rather than any component's node identity, so no " +
			"node-rooted key can name it.\n\nIt is a separate fact rather than " +
			"either sibling reached through a different coordinate, because the " +
			"output names in this render are chosen independently of any " +
			"component's and of any other attachment's, and nothing declares them " +
			"unique across documents.\n\nIt is the origins and not the bytes, and " +
			"the order is stated rather than inherited, on the readings every form " +
			"of this fact carries.\n\nThe name is the one the render itself chose, " +
			"before the caller routes the output by the flag saying whether an " +
			"assembly consumes it. Two blocks meeting on one name have already " +
			"collided in that render's own output, whichever way it is later routed.",
	},

	// -------------------------------------------------------------- dispatched

	{
		Name:        "assembly-group-provider-set",
		Stage:       "dispatched",
		SourceField: "renderer.AssemblyContribution.Provider",
		Subject:     []string{"assembly-path", "provider"},
		Params:      []paramDecl{{Name: "assemblyPath", Type: "string"}},
		Returns:     "[]string",
		Doc: "AssemblyGroupProviderSet are the providers that assemble at one " +
			"evaluated assembly path, in the order the grouping recorded " +
			"them.\n\nIt is keyed by the path alone while the subject it " +
			"belongs to names a path and a provider together, and the " +
			"difference is the question it exists to answer: a rule invoked " +
			"for one group asks what ELSE arrived at its path, which is " +
			"unanswerable from that group's own coordinates. More than one " +
			"entry means several providers wrote their output to one " +
			"directory, which is a state only the whole set makes " +
			"visible.\n\nOne entry is the ordinary answer, and the ordinary " +
			"answer is not the empty one: a path with no provider at it is a " +
			"path no group assembled at, so the coordinate does not exist and " +
			"no rule is invoked for it.",
	},
	{
		Name:        "assembly-group-boundary-set",
		Stage:       "dispatched",
		SourceField: "renderer.AssemblyContribution.Boundaries",
		Subject:     []string{"assembly-path", "provider"},
		Params: []paramDecl{
			{Name: "assemblyPath", Type: "string"},
			{Name: "provider", Type: "string"},
		},
		Returns: "[]string",
		Doc: "AssemblyGroupBoundarySet are the boundaries whose components fed " +
			"one assembly group, deduplicated, in the order the group first " +
			"heard from each.\n\nSeveral boundaries reach one group exactly " +
			"when their own path declarations evaluate alike under one " +
			"provider, and the group is what the collapse leaves: the path and " +
			"the provider name the group, and nothing in them names the " +
			"boundaries behind it. This is where they survive, so a rule " +
			"objecting about a group can name the sites an operator can " +
			"actually edit.\n\nA boundary appears once however many components " +
			"it contributed, because the fact is about which boundaries met " +
			"rather than about how much each brought.",
	},
	{
		Name:        "assembly-group-provider-assembly",
		Stage:       "dispatched",
		SourceField: "wasm.ProviderContract.BoundaryAssembly",
		Subject:     []string{"assembly-path", "provider"},
		Params: []paramDecl{
			{Name: "assemblyPath", Type: "string"},
			{Name: "provider", Type: "string"},
		},
		Returns: "string",
		Doc: "AssemblyGroupProviderAssembly is how the provider elected to " +
			"assemble one dispatch group declares it combines the group's " +
			"per-node output, empty when it declares no assembly " +
			"claim.\n\nIt is the group-keyed reading of a claim each provider " +
			"makes for itself. A group may hold nodes whose providers resolve " +
			"at different versions, and one of them is elected to assemble for " +
			"the whole group — so the claim that governs the group is the " +
			"elected one, and it is not a function of any single node in the " +
			"group. This is where the elected claim survives.\n\nThe empty " +
			"value is an answer and not a missing one, and two states share it: " +
			"a provider stating no assembly claim writes each node's output " +
			"directly at the assembly path, and a group no provider was " +
			"elected for has no claim to state. Both leave the group with no " +
			"assembly to perform, which is one answer to the question this fact " +
			"asks rather than two.",
	},
	{
		Name:        "assembly-group-assembler-provided",
		Stage:       "dispatched",
		SourceField: "types.PluginSpec.Exports",
		Subject:     []string{"assembly-path", "provider"},
		Params: []paramDecl{
			{Name: "assemblyPath", Type: "string"},
			{Name: "provider", Type: "string"},
		},
		Returns: "bool",
		Doc: "AssemblyGroupAssemblerProvided reports whether the plugin serving " +
			"the provider elected to assemble one dispatch group offers the " +
			"assembly that provider's claim names.\n\nA provider's assembly " +
			"claim and the assembly a plugin offers are two independent " +
			"declarations. The claim says what becomes of a group's per-node " +
			"output; the offer is an entry in the plugin's own export surface. " +
			"A plugin serving a provider that claims assembly can offer per-node " +
			"rendering alone, and nothing in the claim reveals it — which is why " +
			"the two are separate facts, and why comparing them is what makes " +
			"the mismatch visible at all.\n\nFalse is the answer for a group no " +
			"provider was elected for, exactly as it is for a group whose plugin " +
			"offers no assembly: neither has an assembly it can perform. What " +
			"separates a group that owes one from a group that does not is the " +
			"claim, which is the other fact, so a rule about the mismatch reads " +
			"both.",
	},
	{
		Name:        "assembly-group-assembly-name-annotation",
		Stage:       "dispatched",
		SourceField: "wasm.ProviderContract.AssemblyNameAnnotation",
		Subject:     []string{"assembly-path", "provider"},
		Params: []paramDecl{
			{Name: "assemblyPath", Type: "string"},
			{Name: "provider", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "AssemblyGroupAssemblyNameAnnotation is the boundary annotation " +
			"the provider elected to assemble one dispatch group names the " +
			"group's assembly from, and whether that provider requires every " +
			"boundary feeding the group to resolve it to one value.\n\nThe two " +
			"arrive together because neither says anything alone. A requirement " +
			"that the name agree is empty without the annotation it is a " +
			"requirement about, and the annotation without the requirement " +
			"leaves open whether disagreement is a fault at all — a provider " +
			"may name its assembly from the first value it finds and be " +
			"indifferent to the rest. This is one declaration a provider makes " +
			"about naming, so it is one fact rather than two that could only " +
			"ever be read together.\n\nIt is the group-keyed reading of a claim " +
			"each provider makes for itself, for the reason the group's " +
			"assembly claim is: a group may hold nodes whose providers resolve " +
			"at different versions, one of them is elected to assemble for the " +
			"whole group, and the naming that governs the group is the elected " +
			"one rather than any single node's.\n\nThe empty annotation is an " +
			"answer and not a missing one, and two states share it: a provider " +
			"naming its assembly from the group's own path declares no " +
			"annotation, and a group no provider was elected for has none to " +
			"declare. Both leave the group's name outside anything a boundary " +
			"authored, which is one answer to the question this fact asks " +
			"rather than two.",
	},
	{
		Name:        "assembly-group-boundary-assembly-names",
		Stage:       "dispatched",
		SourceField: "resolver.ResolveResult.BoundaryAnnotations",
		Subject:     []string{"assembly-path", "provider"},
		Params: []paramDecl{
			{Name: "assemblyPath", Type: "string"},
			{Name: "provider", Type: "string"},
		},
		Returns: "types.OrderedMap[string]",
		Doc: "AssemblyGroupBoundaryAssemblyNames is what each boundary feeding " +
			"one dispatch group resolved the elected provider's assembly-name " +
			"annotation to, keyed by boundary, in the order the group first " +
			"heard from each.\n\nA boundary's value for an annotation is what " +
			"the cascade left it with rather than what its own author typed " +
			"there: a boundary inherits the value declared above it where it " +
			"declares none, and displaces it where it does. So the resolved " +
			"value is the one that governs the boundary, and it is the only one " +
			"a rule comparing the group's names can compare.\n\nIt is empty " +
			"when the elected provider names its assembly from no annotation, " +
			"and empty for a group no provider was elected for: there is no " +
			"annotation to resolve, so there is nothing to report per " +
			"boundary.\n\nA boundary that resolved the annotation to nothing is " +
			"present with an empty value, which is a different answer from " +
			"being absent. Nothing declared the annotation anywhere above that " +
			"boundary, so it brought no name to the assembly it feeds — and a " +
			"rule about whether the group's boundaries agree has to be able to " +
			"say which one that was.",
	},
	{
		Name:        "assembly-group-derived-names",
		Stage:       "dispatched",
		SourceField: "rendercore.RenderNodeOutput.DerivedKeys",
		Subject:     []string{"assembly-path", "provider"},
		Params: []paramDecl{
			{Name: "assemblyPath", Type: "string"},
			{Name: "provider", Type: "string"},
		},
		Returns: "[]DerivedNameDeclaration",
		Doc: "AssemblyGroupDerivedNames are the names the contributors to one " +
			"dispatch group declared they derived inside their own output, one " +
			"entry per contributor per name per space, in key-space order and " +
			"then name order and then contributor order.\n\nA contributor " +
			"derives the names its output addresses resources by, and those " +
			"names sit inside the file bytes the host writes verbatim — so the " +
			"host cannot read them out, and reading them out would mean " +
			"interpreting the contributor's own format. The contributor declares " +
			"them instead, and this is where the declarations of everything " +
			"feeding one group stand together. The group is the scope because it " +
			"is the set of contributors whose output merges, which is where one " +
			"name can displace another.\n\nThe order is stated rather than " +
			"inherited. Declarations accumulate as contributors render, and the " +
			"walks that reach them are not all fixed by the input, so answering " +
			"in arrival order would return a different sequence for identical " +
			"input; space, then name, then contributor is a total order derived " +
			"from the declared strings alone and is therefore the same on every " +
			"run.\n\nA contributor appears once per name per space however many " +
			"of its own crossings declared that name, because a contributor " +
			"legitimately restates a name across a source render, a workload " +
			"synthesis and an artifacts placement — one resource described in " +
			"each report it belongs to. What a single declaration carrying the " +
			"name twice looks like is a different question, and each entry " +
			"answers it in place rather than leaving it to be counted from " +
			"repetitions that no longer distinguish the two.\n\nIt is empty for " +
			"a group whose contributors declared nothing, which is the ordinary " +
			"answer: a contributor deriving no names it wants compared declares " +
			"none, and a group of them has nothing to compare.\n\nNothing here " +
			"reads a name. A space is an opaque string its declarer chose and a " +
			"name is an opaque string its declarer derived, so a rule over this " +
			"covers every space any contributor declares without naming a " +
			"provider or learning a format.",
	},

	// ---------------------------------------------------------------- rendered

	{
		Name:        "emitted-file-path-set",
		Stage:       "rendered",
		SourceField: "renderer.AssembledFile",
		Subject:     []string{"boundary", "provider", "path"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "provider", Type: "string"},
		},
		Returns: "[]string",
		Doc: "EmittedFilePathSet are the output paths written for one provider " +
			"within one boundary, in path order.\n\nThe order is stated rather " +
			"than inherited. The order in which files are placed into a group " +
			"is not a property of the input, so answering in that order would " +
			"return a different sequence for identical input; path order is " +
			"derived from something ordered and is therefore the same on every " +
			"run.\n\nNo rule reads it yet, which is a real state rather than " +
			"an oversight: a fact is declared from where its subject is " +
			"produced, so it stands whether or not a rule has been placed on " +
			"it. The rules it is declared for are collision rules over " +
			"emitted paths — each asking, at its own point of assembly, " +
			"whether one path is claimed more than once — and where in " +
			"assembly each of them belongs is still open. When they are " +
			"placed, they stay distinct rules over this one fact rather than " +
			"becoming several facts, because what differs between them is the " +
			"predicate and not the question.",
	},
	{
		Name:        "assembled-file-content",
		Stage:       "rendered",
		SourceField: "renderer.AssembledFile.Content",
		Subject:     []string{"boundary", "provider", "path"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "provider", Type: "string"},
			{Name: "path", Type: "string"},
		},
		Returns: "[]byte",
		Doc: "AssembledFileContent is the bytes standing at one output path " +
			"for one provider within one boundary.\n\nIt is keyed by the " +
			"path as well as by the boundary and the provider, where the " +
			"path set naming those same three coordinates is keyed by the " +
			"first two and enumerates the third. The two answer different " +
			"questions: one is which paths were written, this is what " +
			"stands at one of them, and neither is derivable from the " +
			"other.\n\nA rule scanning assembled output reads one path's " +
			"bytes at a time rather than receiving the assembly, so no " +
			"rule can walk from one path to another's content.",
	},
	{
		Name:        "artifact-projection-name-set",
		Stage:       "rendered",
		SourceField: "types.RecipeSpec.Artifacts",
		Subject:     []string{"node", "artifact"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "ArtifactProjectionNameSet are the artifact names a node " +
			"projects, in author order.\n\nIt is a separate fact from " +
			"the bytes one artifact projects to, and the pair is " +
			"deliberate rather than one question under two names. This " +
			"one answers which artifacts exist, which a rule needs in " +
			"hand before it can ask about any single one; the other " +
			"answers what stands in one, which is unanswerable until a " +
			"name is in hand.\n\nThe fact is a name list and not the " +
			"projected entries themselves. Handing back the entries " +
			"would let a rule reach the projected bytes the framework " +
			"already exposes as a fact of its own, so a rule could " +
			"re-derive a declared fact from this one.",
	},
	{
		Name:        "artifact-projection-content",
		Stage:       "rendered",
		SourceField: "types.RecipeSpec.Artifacts",
		Subject:     []string{"node", "artifact"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "[]byte",
		Doc: "ArtifactProjectionContent is the bytes one of a node's artifacts " +
			"projected to.",
	},
}
