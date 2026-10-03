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
		Name:    "catalog",
		Extends: "document",
		Holds: "one authored document as the document stage hands it over, " +
			"together with the catalog its own references resolve against — " +
			"nothing composed into a compilation, and no second document read",
		// Resolving what the presented document names is what completes these
		// facts, and a catalog entry is reached either as a directory in the
		// workspace or as a published artifact fetched into it. That is the same
		// pair the stage after this one declares, and for the same reason: the
		// reach is the catalog's, not the compilation's.
		//
		// 🔴 THIS STAGE PRESENTS ONE AUTHORED DOCUMENT AND NOT A COMPILATION, so
		// it is the second stage the document-kind axis could apply at — and the only
		// reason it does not is that every fact here is carried by one kind, the
		// Manifest, which the stage itself fixes. A stage whose facts all name one
		// kind has a single document kind, so there is nothing for a second axis to
		// distinguish and none is declared.
		//
		// 🔴 THE FIRST FACT A SECOND DOCUMENT KIND NEEDS HERE ENDS THAT, AND
		// NOTHING WILL STOP YOU. deriveDocumentKinds refuses a NON-EMPTY document kind
		// declared away from the document stage, and a fact declared here with no
		// document kind trips nothing — so the day a Release or a Recipe fact lands
		// here, the stage silently starts answering two kinds through one set of
		// accessors with no axis separating them. Making the axis apply at more
		// than one stage is the change that owes: per-kind bases and obligation
		// interfaces here as well as at the root, and a document-kind declaration that
		// is refused today. It is deferred rather than avoided, and it is written
		// on the declaration rather than only in a record so that whoever adds
		// that fact meets it where they are working.
		HostPowers: []hostPower{powerFilesystem, powerArtifactFetch},
	},
	{
		Name:    "normalized",
		Extends: "catalog",
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
		// attachment's, and the Release's edge surface — has contributed by
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
		Holds: "a Recipe bound to every node and a FeatureRecipe bound to every " +
			"attachment, with what each of them declares readable as authored — " +
			"the source it declares, its provides and locals, and the template " +
			"text of every field it writes one in — together with the path " +
			"templates the Release's own scopes declare, every plugin those " +
			"bindings resolve to, loaded and self-described: the declared " +
			"capabilities, the scope each dispatched export declares, what each " +
			"provider reports about assembling and linking a scope's output, and " +
			"the name each node's provider gives the output it renders from " +
			"authored source — all of it before any step runs",
		// This is the earliest stage a fact carried by a Recipe or a FeatureRecipe
		// can occupy, and the floor is the BINDING rather than the node. A node
		// exists earlier — facts keyed by one are complete as soon as the
		// authoring documents are normalized — but a Recipe is not an authoring
		// document: it is selected for a node by the node's resolved component,
		// and a FeatureRecipe by an attachment's resolved type, both of which the
		// attachment cascade settles. Until then there is no bound document to
		// read a field out of, whichever authored fields a node already carries.
		// So no such fact is declared before this stage, and one that appears to
		// belong earlier is reading a binding that does not exist yet.
		//
		// The powers below are what the plugin self-reports cost: producing one
		// resolves the reference against the sources the workspace configures,
		// fetches the guest bytes, and calls describe() across the boundary. They
		// are declared for the stage because the declaration is per stage, not
		// because every fact here reaches for them — the authored-declaration
		// facts above are read out of documents the binding already resolved.
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
			"render, so a node's own render, the render one attachment carries on " +
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
		Name: "AttachmentContribution",
		Doc: "AttachmentContribution is one place an attachment a node carries was " +
			"contributed from: the authoring surface, the scope and component it " +
			"was written under, and — where annotation-driven expansion produced " +
			"it — the annotation that did.\n\nIt is one fact rather than several " +
			"because no part of it names a place alone. A scope path is a " +
			"subsystem on one surface and a boundary on the other, a component " +
			"name is read under whichever of the two holds it, and an annotation " +
			"key says nothing about which block it was declared in; a reader " +
			"handed the parts as separate answers would have to re-pair them, " +
			"which is the re-derivation a declared fact exists to close.",
		Fields: []recordField{
			{Name: "Surface", Type: "string", Doc: "Surface is where the contribution was written: one of [ContributionAggregateComponent], [ContributionSubsystem], [ContributionReleaseBoundary], [ContributionReleaseComponent] or [ContributionAnnotation]."},
			{Name: "ScopePath", Type: "string", Doc: "ScopePath is the subsystem or boundary the contribution was written in, joined the way that surface's axis joins its levels, and empty where the contribution sits at its document's top level."},
			{Name: "Component", Type: "string", Doc: "Component is the component the contribution was keyed under, empty where it was written scope-wide."},
			{Name: "AnnotationKey", Type: "string", Doc: "AnnotationKey is the annotation whose expansion produced the contribution, empty where the author wrote the attachment explicitly."},
			{Name: "AnnotationSource", Type: "string", Doc: "AnnotationSource names where that annotation was declared, empty where the author wrote the attachment explicitly."},
			{Name: "Socket", Type: "string", Doc: "Socket is the dependency socket of the connection the contribution was written on, empty where it was written on an attachment block rather than on a connection entry."},
			{Name: "Alias", Type: "string", Doc: "Alias is the alias of the connection the contribution was written on, empty where it was written on an attachment block rather than on a connection entry. Together with Socket it names the connection entry an author edits, which the component the contribution was keyed under does not."},
		},
	},
	{
		Name: "AttachmentConnection",
		Doc: "AttachmentConnection is the connection one attachment a node " +
			"carries was written on: the dependency socket and the alias naming the " +
			"connection, and the slot of that socket the attachment fills.\n\nIt is " +
			"one fact rather than three because no part of it answers anything " +
			"alone. A slot is a slot of one socket, so the slot without its socket " +
			"names no declaration; and a socket carries many connections, so the " +
			"socket without the alias names no connection a fill can be owed by.",
		Fields: []recordField{
			{Name: "Socket", Type: "string", Doc: "Socket is the consuming component's dependency socket the connection is wired through."},
			{Name: "Alias", Type: "string", Doc: "Alias names the connection within its socket."},
			{Name: "Slot", Type: "string", Doc: "Slot is the slot the socket declares that the attachment fills — the name the attachment was written under on its connection entry."},
		},
	},
	{
		Name: "DerivedNameDeclaration",
		Doc: "DerivedNameDeclaration is one contributor's declaration that it " +
			"derived one name into one of its own key spaces: the space, the " +
			"name, every coordinate that identifies the contributor, whether " +
			"a single one of that contributor's declarations carried the name " +
			"more than once within that space, and where the render put that " +
			"contributor's output.\n\nIt is one fact " +
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
			"a reader from having to pair two answers up and stopping halfway. " +
			"Where the render put the output travels with the declaration for " +
			"that same reason: two contributors' names are the same pair of " +
			"strings whichever files they landed in, so the accumulated names " +
			"cannot show that one of them stands in a document the other never " +
			"reaches.",
		Fields: []recordField{
			{Name: "KeySpace", Type: "string", Doc: "KeySpace is the space the declaring contributor named, an opaque string of its own choosing."},
			{Name: "DerivedName", Type: "string", Doc: "DerivedName is the name derived into that space, an opaque string the contributor derived."},
			{Name: "Contributor", Type: "string", Doc: "Contributor is the authoring-level name of whatever derived the name: a component instance, or a feature instance attached to a boundary."},
			{Name: "ContributorKind", Type: "string", Doc: "ContributorKind is what that contributor is in authoring terms, either a component or a feature, which is what says where its name is written."},
			{Name: "ContributorScope", Type: "string", Doc: "ContributorScope is the further authored coordinate that completes the contributor's identity within what its kind names: the attachment key a component carries, or the path of the boundary a feature is attached at. It is empty where the kind's own name identifies the contributor on its own, which is the case for a component's own render. It is carried apart from Contributor rather than run together with it because the pair is compared as well as read: two contributors differing only in this coordinate have to arrive as two, and one joined string could fold them into one — leaving a name two of them derived compared against nothing."},
			{Name: "RepeatedWithinOneDeclaration", Type: "bool", Doc: "RepeatedWithinOneDeclaration reports whether a single one of that contributor's declarations carried this name more than once inside this space, which is a failure of that contributor's own derivation rather than a meeting between two of them."},
			{Name: "OutputPlacedStandalone", Type: "bool", Doc: "OutputPlacedStandalone reports whether the render wrote this contributor's output as discrete files at its own position rather than handing it to its dispatch group's assembler. It states where the output went and nothing that follows from it: it is true for every contributor to a group whose provider folds nothing into one document, because such a group has no assembled document for anything to be handed to. A reader treating it alone as a reason not to compare two names would therefore stop comparing everywhere, which is a removal rather than a narrowing — what the group's provider declares it does with the group's output is a separate answer, and the two are read together."},
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
	{
		Name: "ExtendsChainCycleEdge",
		Doc: "ExtendsChainCycleEdge is the edge at which an extends chain returns " +
			"to an entity already in it: the entity whose extends declaration " +
			"closes the loop, and the reference that declaration names.\n\nIt is " +
			"one fact rather than two because neither half sends an author " +
			"anywhere on its own. An entity without the reference names a file to " +
			"open and not the line to change, since an entity may legitimately " +
			"declare an extends that is not the offending one; a reference " +
			"without the entity names a target and leaves the author to find " +
			"which of the chain's entities pointed at it. Splitting the pair " +
			"would also hand a rule two values it has to re-pair, which is the " +
			"re-derivation a declared fact exists to close.\n\nThe entity that " +
			"closes a loop is rarely the entity a resolution was requested for, " +
			"which is why the edge is carried at all rather than left to a " +
			"reader who has the presented document already.",
		Fields: []recordField{
			{Name: "Declaring", Type: "string", Doc: "Declaring is the entity whose extends declaration reaches an entity already in the chain, named the way a diagnostic names that entity."},
			{Name: "Reference", Type: "string", Doc: "Reference is the extends reference that declaration names, as its author wrote it — which is the text the author edits, and not the identity the reference resolved to."},
		},
	},
	{
		Name: "AnnotationBindingRoute",
		Doc: "AnnotationBindingRoute is one routing slot an annotation binding " +
			"claims on the Feature it targets: the pillar the value lands in " +
			"and the dotted path within that pillar.\n\nIt is one fact rather " +
			"than two because a path without its pillar has not met itself. " +
			"One Feature legitimately declares the same property name under " +
			"both pillars, so two bindings routing the same path to different " +
			"pillars claim different slots, and a reader holding the paths " +
			"alone would read those two as one. Splitting the pair into two " +
			"accessors would hand a rule two lists it has to re-pair by " +
			"position, which is the re-derivation declaring the fact exists " +
			"to close.",
		Fields: []recordField{
			{Name: "Pillar", Type: "string", Doc: "Pillar is the configuration pillar the routed value lands in, as the binding's author wrote it."},
			{Name: "Path", Type: "string", Doc: "Path is the dotted path within that pillar the value is routed to, as the binding's author wrote it."},
		},
	},
	{
		Name: "AnnotationSubkeyTarget",
		Doc: "AnnotationSubkeyTarget is what one authored annotation subkey has " +
			"to satisfy: the Feature whose contract governs the value, the " +
			"pillar of that contract the value lands in, and the dotted path " +
			"within that pillar.\n\nIt is one fact rather than three because no " +
			"part of it answers anything alone. A value is measured against a " +
			"sub-contract located by a pillar and a path inside one Feature's " +
			"document, so a reader holding any two of the three has located " +
			"nothing — and a reader handed the three as separate answers would " +
			"have to re-pair them, which is the re-derivation a declared fact " +
			"exists to close.\n\nIt is a separate fact from the routing slots a " +
			"binding declares, and the difference is that this one is the " +
			"LOOKUP applied rather than the table read. The table says what a " +
			"binding claims; this says which of those claims an authored subkey " +
			"falls under, including the tie-break between a path declared in " +
			"both pillar lists and the rule that a value written without " +
			"subkeys routes through the binding's single declared path. Those " +
			"are rules with one home, and a rule re-deriving them from the " +
			"table would be a second copy of them.",
		Fields: []recordField{
			{Name: "FeatureType", Type: "string", Doc: "FeatureType is the Feature the binding routes to, as the binding's author wrote it."},
			{Name: "Pillar", Type: "string", Doc: "Pillar is the configuration pillar of that Feature's contract the value lands in."},
			{Name: "Path", Type: "string", Doc: "Path is the dotted path within that pillar the value is routed to."},
		},
	},
	{
		Name: "ManifestRecipeMapping",
		Doc: "ManifestRecipeMapping is one entry of a Manifest's recipe " +
			"mapping table, identified the way the table itself identifies " +
			"it: the tier it is written under, the axis it keys on, and the " +
			"key the author wrote.\n\nIt is one fact rather than three " +
			"because no one of the three names an entry. A tier holds two " +
			"axes and an axis holds many keys, so a key alone names as many " +
			"entries as there are axes, and a reader holding the three as " +
			"separate lists would have to re-pair them by position — which " +
			"is the re-derivation a declared fact exists to close. The " +
			"three together name exactly one entry, because each axis is a " +
			"mapping and a mapping holds one value per key.\n\nThe tier is " +
			"one of the two keys a Manifest writes under mappings, and the " +
			"axis one of the two a tier writes under; both are carried as " +
			"the words the author typed, so a rule reporting on an entry " +
			"reports it in the author's own spelling.",
		Fields: []recordField{
			{Name: "Tier", Type: "string", Doc: "Tier is the mapping tier the entry is written under, as the author wrote it."},
			{Name: "Axis", Type: "string", Doc: "Axis is the keying axis within that tier the entry is written under, as the author wrote it."},
			{Name: "Key", Type: "string", Doc: "Key is the key the entry is declared under, which is what the mapping maps."},
		},
	},
	{
		Name: "BoundaryPlacement",
		Doc: "BoundaryPlacement is one thing a Release's boundary places, as the " +
			"coordinates that identify it: the authoring document it was " +
			"written in, the boundary it sits in, what it addresses, and the " +
			"block it was written in.\n\nIt is one fact rather than four " +
			"because no part of it names a placement alone. One boundary can " +
			"name a component directly under the id it carries once the " +
			"composition is flattened AND reach the same component again " +
			"through a routing entry — two entries the author wrote at two " +
			"keys — and a routed sub-module's dotted prefix can equal a " +
			"component's id, so any two of the coordinates leave pairs nothing " +
			"could tell apart. That is exactly the collision a rule over this " +
			"axis exists to see, so a reader handed the coordinates as " +
			"separate lists would have to re-pair them by position, which is " +
			"the re-derivation a declared fact exists to close.\n\nReference " +
			"is what the entry ADDRESSES rather than what the composition " +
			"holds: the id a component entry names once the composition is " +
			"flattened — resolved from the local name for a routed entry, " +
			"because inside its module that is the only name it has — or the " +
			"dotted prefix of a routed sub-module. Whether anything answers to " +
			"it is a separate question asked of the flattened set.\n\nIt is " +
			"also the coordinate tuple of the subject a rule over this axis is " +
			"invoked once per, so the entry a rule is invoked for and the set " +
			"it reads back are spelled by one type and cannot come to describe " +
			"an entry differently.",
		Fields: []recordField{
			{Name: "Document", Type: "string", Doc: "Document is which authoring document the placement was written in, one of [AuthoringDocumentAggregate] or [AuthoringDocumentRelease]. It selects the scope nesting the boundary path is spelled with."},
			{Name: "BoundaryPath", Type: "string", Doc: "BoundaryPath is the boundary the placement was written in, joined the way that axis joins its levels. It is empty for a placement inside the boundary normalization synthesizes to hold the entries an author wrote at the document's top level, which is not a boundary any file spells."},
			{Name: "Reference", Type: "string", Doc: "Reference is the id a component entry addresses in the flattened composition, or the dotted prefix of a routed sub-module."},
			{Name: "Axis", Type: "string", Doc: "Axis is the block the entry was written in: one of [PlacementNamedComponent], [PlacementRoutedComponent] or [PlacementRoutedModule]."},
		},
	},
	{
		Name: "ReleaseEdgeConfiguration",
		Doc: "ReleaseEdgeConfiguration is one entry a Release's per-edge `uses` " +
			"surface writes, as the coordinates that identify it: the " +
			"authoring document it was written in, the boundary it sits in, " +
			"and the consuming component, socket and alias it was keyed " +
			"under.\n\nIt is one fact rather than five because no shorter " +
			"tuple names an entry. The consumer, the socket and the alias " +
			"together identify a connection, which is the key every fact " +
			"about one takes; the boundary is part of the identity because a " +
			"Release writes its per-edge surface inside a boundary's own " +
			"component entry, so the boundary is where an author edits the " +
			"entry and two boundaries are two separate blocks. A reader " +
			"handed the coordinates as separate lists would have to re-pair " +
			"them by position, which is the re-derivation a declared fact " +
			"exists to close.\n\nIt reports what the Release WROTE and never " +
			"what the composition drew. An entry that drew no connection " +
			"appears here exactly as one that drew a connection does, because " +
			"the Release wrote it either way and whether anything answers to " +
			"it is a different question, asked of the connection set by the " +
			"fact keyed per connection. That separation is the whole of why " +
			"this axis exists: the connection set holds one instance per " +
			"connection DRAWN, so an entry that drew none is absent from it " +
			"by construction and a rule over such an entry could never be " +
			"invoked for one.\n\nIt is also the coordinate tuple of the " +
			"subject a rule over this axis is invoked once per, so the entry " +
			"a rule is invoked for and the set it reads back are spelled by " +
			"one type and cannot come to describe an entry differently.",
		Fields: []recordField{
			{Name: "Document", Type: "string", Doc: "Document is which authoring document the entry was written in, one of [AuthoringDocumentAggregate] or [AuthoringDocumentRelease]. It selects the scope nesting the boundary path is spelled with."},
			{Name: "BoundaryPath", Type: "string", Doc: "BoundaryPath is the boundary the entry was written in, joined the way that axis joins its levels. It is empty for an entry inside the boundary normalization synthesizes to hold the components an author wrote at the document's top level, which is not a boundary any file spells."},
			{Name: "Node", Type: "string", Doc: "Node is the consuming component the entry was keyed under, by the identity every keyed fact takes."},
			{Name: "Socket", Type: "string", Doc: "Socket is the dependency socket the entry sits under."},
			{Name: "Alias", Type: "string", Doc: "Alias names the entry within its socket, which is the key the author wrote."},
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

// perComponentBoundaryFold is the paragraph a fact carries when it reads the
// Release's per-component projection — the one that folds every scope writing
// against a component into a single entry keyed by that component.
//
// It is one shared paragraph rather than a copy inside each Doc, on the same
// reasoning as the paragraph above: the fold is a property of the projection, so
// it says the same thing wherever it is read, and copies of it would agree on the
// day they were written and be free to drift afterwards with nothing comparing
// them. Which facts carry it is readable from the references to this constant
// rather than from a list kept beside them.
//
// A fact keyed on that projection whose ANSWER does not turn on the fold does not
// carry it, and that exclusion is a judgement rather than an oversight. Whether
// the Release wrote an entry against a component at all is true wherever any
// scope wrote one, so there is no contest for a fold to settle and stating one
// would imply a precedence the answer never consults.
const perComponentBoundaryFold = "\n\nWhere several scopes of the Release " +
	"write against one component, this answers from exactly one of those " +
	"entries, and which one is settled rather than left to a reader: the scopes " +
	"are walked in the order the document declares them, each scope's own " +
	"entries read before the scopes nested inside it, and every entry read " +
	"replaces the one before it. So a nested scope displaces the scope " +
	"containing it, and a later-declared scope displaces an earlier one whatever " +
	"their depths. The rule belongs in the declaration because the fact is keyed " +
	"by the component alone and has to yield one answer: an implementor left to " +
	"pick would pick the opposite as readily, and a rule measuring this against " +
	"what the Aggregate declared would then be comparing the wrong entry."

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
		Name:         "raw-aggregate-text",
		Stage:        "document",
		SourceField:  "resolver.ResolveInputs.AggregateRawBytes",
		DocumentKind: "Aggregate",
		Subject:      []string{"document"},
		Params:       []paramDecl{{Name: "document", Type: "string"}},
		Returns:      "[]byte",
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
		Name:         "entity-assertion-declaring-entity",
		Stage:        "document",
		SourceField:  "types.ComponentMetadata.Name",
		DocumentKind: "Component",
		Subject:      []string{"document", "assertion"},
		Params: []paramDecl{
			{Name: "document", Type: "string"},
			{Name: "assertion", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "EntityAssertionDeclaringEntity is the entity that declares an " +
			"assertion name nearest the root of the chain the presented entity " +
			"extends, and whether the chain declares that name at all.\n\nThe " +
			"chain is walked root-first and the first declarer answers, so a " +
			"name several entities declare answers with the one furthest from " +
			"the presented entity. The tie-break belongs in the declaration " +
			"because the fact is keyed by the name alone and has to yield one " +
			"answer: an implementor left to pick would pick the nearest as " +
			"readily, and a rule reporting a name that carries two rules would " +
			"then name a restatement rather than the entity the name " +
			"originates on.\n\nA name no ancestor declares answers with the " +
			"presented entity itself, which is the only declarer there. The " +
			"second result is false only for a name no entity along the chain " +
			"declares: a name nobody declared and a declaration by an entity " +
			"carrying no name are different findings, and a rule that cannot " +
			"tell them apart reports the wrong one.",
	},
	{
		Name:        "component-schema-pillar-keys",
		Stage:       "document",
		SourceField: "types.ComponentSpec.Schema",
		Subject:     []string{"document", "schema-pillar"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "[]string",
		Doc: "ComponentSchemaPillarKeys are the top-level keys the presented " +
			"Component's schema block declares, in author order.\n\nThey are the " +
			"keys the author wrote rather than the data pillars the block may " +
			"carry, and that is the whole of what makes a rule over them " +
			"possible: a key naming no pillar is exactly what a set already " +
			"filtered to the pillars could not report. An entity declaring no " +
			"schema block declares none, and so does one stating that its pillars " +
			"are the entity it extends.\n\nThe order is the author's and is " +
			"carried rather than sorted, because a rule invoked once per key " +
			"raises in invocation order and that order is the order the findings " +
			"reach the reader.",
	},
	{
		Name:        "feature-schema-pillar-keys",
		Stage:       "document",
		SourceField: "types.FeatureSpec.Schema",
		Subject:     []string{"document", "schema-pillar"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "[]string",
		Doc: "FeatureSchemaPillarKeys are the top-level keys the presented " +
			"Feature's schema block declares, in author order.\n\nIt is a second " +
			"fact rather than one fact over both authored kinds, and the " +
			"plurality is forced rather than chosen: a fact is filed under exactly " +
			"one authored document kind, so a single fact would be readable from " +
			"one kind's view and would refuse the other at the point a " +
			"rule read it. The two kinds write the block under the same key and " +
			"one rule governs both, which is why the second fact is a declaration " +
			"rather than a second design.\n\nWhat the keys are, and why they are " +
			"carried in the author's order, is what the Component's own fact " +
			"states; it is not restated here, because a fact's documentation has " +
			"one home and a second copy would be the one that drifts.",
	},
	{
		Name:        "component-extends-chain-cycle-edge",
		Stage:       "document",
		SourceField: "types.ComponentSpec.Extends",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "(ExtendsChainCycleEdge, bool)",
		Doc: "ComponentExtendsChainCycleEdge is the edge at which the chain the " +
			"presented Component extends returns to an entity already in it, and " +
			"whether the chain closes on itself at all.\n\nThe second result is " +
			"load-bearing rather than a zero value: a chain that closes on itself " +
			"and a chain whose entities carry no names would otherwise answer the " +
			"same, and a rule that cannot tell them apart reports the wrong one.\n\n" +
			"The chain is followed outward from the presented Component and stops " +
			"at the first entity it reaches twice, so the edge answered is the one " +
			"that closes the loop rather than any earlier edge along it. The " +
			"tie-break belongs in the declaration because the fact is keyed by the " +
			"document alone and has to yield one answer: an implementor left to " +
			"pick would as readily answer the edge the walk started on, which every " +
			"chain has and which is the one edge an author must not be sent to.\n\n" +
			"Membership is decided on the entity a reference resolved to rather " +
			"than on the reference text, because the two are written in different " +
			"vocabularies — an author may pin a version in the reference while the " +
			"entity names itself without one — so a fact comparing the texts would " +
			"report an edge one lap further round the loop than the edge at fault.",
	},
	{
		Name:        "component-extends-chain-length",
		Stage:       "document",
		SourceField: "types.ComponentSpec.Extends",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "(int, bool)",
		Doc: "ComponentExtendsChainLength is how many entities the chain the " +
			"presented Component extends reaches, and whether that chain ends " +
			"cleanly.\n\nA chain that closes on itself has no length to report, " +
			"which is what the second result says and why it is not answered as a " +
			"zero. The ordering between the two questions a chain's shape raises " +
			"is carried here rather than left to whoever invokes the rules over " +
			"them: a rule comparing this count against a bound would otherwise " +
			"judge a loop by its length, and an author told to shorten a chain has " +
			"been handed advice that cannot remove a loop. Because the ordering is " +
			"in the fact, the two rules may be invoked in either order and only the " +
			"one whose subject the chain actually has can raise.\n\nThe count is of " +
			"entities rather than of edges, so a Component extending nothing reaches " +
			"one.",
	},
	{
		Name:        "feature-extends-chain-cycle-edge",
		Stage:       "document",
		SourceField: "types.FeatureSpec.Extends",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "(ExtendsChainCycleEdge, bool)",
		Doc: "FeatureExtendsChainCycleEdge is the edge at which the chain the " +
			"presented Feature extends returns to an entity already in it, and " +
			"whether the chain closes on itself at all.\n\nIt is a fact of its own " +
			"rather than one fact over every authored kind that declares extends " +
			"this way, and the plurality is forced rather than chosen: a fact is " +
			"filed under exactly one authored document kind, so a single fact would " +
			"be readable from one kind's view and would refuse the rest at the " +
			"point a rule read it. What the edge is, how the chain is followed and " +
			"why membership is decided on the resolved entity are what the " +
			"Component's own fact states; they are not restated here, because a " +
			"fact's documentation has one home and a second copy would be the one " +
			"that drifts.",
	},
	{
		Name:        "feature-extends-chain-length",
		Stage:       "document",
		SourceField: "types.FeatureSpec.Extends",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "(int, bool)",
		Doc: "FeatureExtendsChainLength is how many entities the chain the presented " +
			"Feature extends reaches, and whether that chain ends cleanly.\n\nIt is " +
			"the Feature document's declaration of what the Component's own length " +
			"fact declares for a Component. Why there are two, and why a chain that " +
			"closes on itself has no length to report, is stated there.",
	},
	{
		Name:        "manifest-extends-chain-cycle-edge",
		Stage:       "document",
		SourceField: "types.ManifestSpec.Extends",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "(ExtendsChainCycleEdge, bool)",
		Doc: "ManifestExtendsChainCycleEdge is the edge at which the chain the " +
			"presented Manifest extends returns to a Manifest already in it, and " +
			"whether the chain closes on itself at all.\n\nIt is the Manifest " +
			"document's declaration of what the Component's own cycle fact declares " +
			"for a Component, and the reason there are several is stated on the " +
			"Feature's.\n\nOne thing about it is the Manifest's own and is stated " +
			"here because no other document's declaration could: a link is a FILE. " +
			"Two Manifest files may legally declare one name, so they are two links " +
			"and a chain passing through both has not met itself — while the edge " +
			"this answers names each link the way its author named it, which is the " +
			"text an author reads rather than the path a machine composed.",
	},
	{
		Name:        "manifest-extends-chain-length",
		Stage:       "document",
		SourceField: "types.ManifestSpec.Extends",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "(int, bool)",
		Doc: "ManifestExtendsChainLength is how many Manifests the chain the " +
			"presented Manifest extends reaches, and whether that chain ends " +
			"cleanly.\n\nIt is the Manifest document's declaration of what the " +
			"Component's own length fact declares for a Component. Why there are " +
			"several, and why a chain that closes on itself has no length to " +
			"report, is stated there. The count is of files rather than of names, for the " +
			"reason the Manifest's own cycle fact gives.",
	},
	{
		Name:        "release-extends-chain-cycle-edge",
		Stage:       "document",
		SourceField: "types.ReleaseSpec.Extends",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "(ExtendsChainCycleEdge, bool)",
		Doc: "ReleaseExtendsChainCycleEdge is the edge at which the chain the " +
			"presented Release extends returns to a Release already in it, and " +
			"whether the chain closes on itself at all.\n\nIt is the Release " +
			"document's declaration of what the Component's own cycle fact declares " +
			"for a Component, and the reason there are several is stated on the " +
			"Feature's.\n\nWhat the Release contributes of its own is what the " +
			"Manifest's declaration contributes of its: a link is a FILE. Two " +
			"Release files may legally declare one name — an environment pair " +
			"naming the deployed system rather than the environment is the ordinary " +
			"shape — so they are two links and a chain passing through both has not " +
			"met itself. The edge this answers names each link the way its author " +
			"named it, which is the text an author reads rather than the path a " +
			"machine composed.",
	},
	{
		Name:        "release-extends-chain-length",
		Stage:       "document",
		SourceField: "types.ReleaseSpec.Extends",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "(int, bool)",
		Doc: "ReleaseExtendsChainLength is how many Releases the chain the " +
			"presented Release extends reaches, and whether that chain ends " +
			"cleanly.\n\nIt is the Release document's declaration of what the " +
			"Component's own length fact declares for a Component. Why there are " +
			"several, and why a chain that closes on itself has no length to " +
			"report, is stated there. The count is of files rather than of names, " +
			"for the reason the Release's own cycle fact gives.",
	},
	{
		Name:        "release-extends-chain-boundaries-declared-by",
		Stage:       "document",
		SourceField: "types.ReleaseSpec.Boundaries",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "(string, bool)",
		Doc: "ReleaseExtendsChainBoundariesDeclaredBy is the Release along the " +
			"chain that declares named boundaries, and whether any Release along " +
			"it declares them at all.\n\nIt names a Release the way its author " +
			"named it, because the answer is read back into a diagnostic an author " +
			"acts on and the file they have to open is the one they wrote.\n\nIt " +
			"is a fact about the chain rather than about a document, and the " +
			"distinction is the point: named boundaries and top-level components " +
			"are mutually exclusive in one Release, and every authored document is " +
			"already held to that. What no document-level reading can see is a " +
			"chain whose links are each well-formed and whose composition carries " +
			"both. Answering which Release contributes which key is what lets the " +
			"rule name both files instead of blaming whichever one was presented.",
	},
	{
		Name:        "release-extends-chain-components-declared-by",
		Stage:       "document",
		SourceField: "types.ReleaseSpec.Components",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "(string, bool)",
		Doc: "ReleaseExtendsChainComponentsDeclaredBy is the Release along the " +
			"chain that declares top-level components, and whether any Release " +
			"along it declares them at all.\n\nIt is the counterpart of the " +
			"chain's boundaries fact, and the reason both exist rather than one " +
			"answering both sides is stated there.",
	},
	{
		Name:         "release-extends-chain-inherited-literal-directories",
		Stage:        "document",
		SourceField:  "types.Boundary.Directory",
		DocumentKind: "Release",
		Subject:      []string{"document"},
		Params:       []paramDecl{{Name: "document", Type: "string"}},
		Returns:      "[]string",
		Doc: "ReleaseExtendsChainInheritedLiteralDirectories are the boundaries " +
			"whose output directory the presented Release inherited from the " +
			"Release it extends and which carry no template variable, named by " +
			"the boundary path the composition placed them at, in composition " +
			"order.\n\nOrder is the composition's and is carried rather than " +
			"sorted, because a rule invoked once per entry raises in invocation " +
			"order and that order is the order the findings reach the " +
			"reader.\n\nIt answers about the composition rather than about either " +
			"document, and it has to: whether a directory was inherited is not " +
			"readable from the Release that declared it, and whether the value " +
			"carries a variable is not readable from the Release that did not. " +
			"Only the fold holds both halves.",
	},
	{
		Name:         "manifest-annotation-binding-keys",
		Stage:        "document",
		SourceField:  "types.MappingTier.Annotations",
		DocumentKind: "Manifest",
		Subject:      []string{"document", "annotation-binding"},
		Params:       []paramDecl{{Name: "document", Type: "string"}},
		Returns:      "[]string",
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
		Name:         "manifest-annotation-binding-feature",
		Stage:        "document",
		SourceField:  "types.AnnotationBinding.Feature",
		DocumentKind: "Manifest",
		Subject:      []string{"document", "annotation-binding"},
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
	{
		Name:         "manifest-annotation-binding-routes",
		Stage:        "document",
		SourceField:  "types.AnnotationBinding.Maps",
		DocumentKind: "Manifest",
		Subject:      []string{"document", "annotation-binding"},
		Params: []paramDecl{
			{Name: "document", Type: "string"},
			{Name: "binding", Type: "string"},
		},
		Returns: "[]AnnotationBindingRoute",
		Doc: "ManifestAnnotationBindingRoutes are the routing slots one " +
			"annotation binding claims on the Feature it targets, in author " +
			"order with each pillar's own paths in the order the binding " +
			"declares them.\n\nA binding routes an annotation's value into " +
			"named fields of a Feature attachment, and each entry here is one " +
			"such destination: the pillar it lands in and the dotted path " +
			"within it. A binding declaring none routes nowhere, which is a " +
			"different answer from a key nobody declared — that second " +
			"question is what the binding's Feature answers, so a rule " +
			"needing both asks both.\n\nThe order is the author's and is " +
			"carried rather than sorted, because a rule reporting on a route " +
			"reports in the order it read them, and that order is the order " +
			"the findings reach the reader.",
	},
	{
		Name:         "annotation-binding-subkey-target",
		Stage:        "document",
		SourceField:  "types.AnnotationBinding",
		DocumentKind: "Manifest",
		Subject:      []string{"document", "annotation-binding"},
		Params: []paramDecl{
			{Name: "binding", Type: "string"},
			{Name: "subkey", Type: "string"},
		},
		Returns: "(AnnotationSubkeyTarget, bool)",
		Doc: "AnnotationBindingSubkeyTarget is what one subkey written under an " +
			"annotation key has to satisfy — the Feature, the pillar and the " +
			"path its value is routed to — and whether the presented Manifest " +
			"routes it anywhere at all.\n\nThe subkey is the key an author wrote " +
			"inside a structured annotation value. An annotation written as a " +
			"bare value has no subkey, and the empty subkey is how that shape is " +
			"asked about: such a value routes through the binding's single " +
			"declared path, so it is answered where exactly one path is declared " +
			"across both pillars and reported absent where none or several are. " +
			"That is the same reading the expansion applies, expressed once " +
			"here rather than by every rule that needs it.\n\nThe second result " +
			"separates a subkey the binding routes from one it does not, which " +
			"is a finding in its own right and not a zero value: a key nobody " +
			"declared a binding for and a subkey the binding's own map does not " +
			"name are both answered as absent, and a rule needing to tell those " +
			"apart asks the binding's Feature beside this.\n\nIt is a separate " +
			"fact from the routing slots the binding declares, because this is " +
			"the lookup applied where that is the table read. The table lists " +
			"what the binding claims; this says which claim an authored subkey " +
			"falls under, tie-break included — a path declared in both pillar " +
			"lists routes through infra, which is the routing the compiler acts " +
			"on and a separate condition advises about.",
	},
	{
		Name:        "recipe-values-mapping-declared",
		Stage:       "document",
		SourceField: "types.RecipeSpec.ValuesMapping",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "bool",
		Doc: "RecipeValuesMappingDeclared reports whether the presented Recipe " +
			"declares a values overlay.\n\nIt answers whether the author wrote " +
			"the block at all, in any of the forms the block accepts — an " +
			"inline template, a file import, a library import — and never what " +
			"the overlay resolves to. The three forms are one authoring " +
			"decision and a rule asking whether an overlay exists is asking " +
			"about that decision, so splitting them would hand every such rule " +
			"three answers to recombine.",
	},
	{
		Name:        "recipe-source-declared",
		Stage:       "document",
		SourceField: "types.RecipeSpec.Source",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "bool",
		Doc: "RecipeSourceDeclared reports whether the presented Recipe declares " +
			"a source pointer.\n\nIt is a separate fact from the values " +
			"overlay above rather than the other half of one, because the two " +
			"are written at two authored keys: a fact naming both source fields " +
			"would be a fact about two sites, and a rule reading it could not " +
			"say which of them the author is being sent to.\n\nWhether a " +
			"source is present is a shape question and is the whole of what " +
			"this answers. What the pointer contains is the engine's contract " +
			"to define, so nothing here reads inside it.",
	},
	{
		Name:        "recipe-workload-declared",
		Stage:       "document",
		SourceField: "types.RecipeSpec.Workload",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "bool",
		Doc: "RecipeWorkloadDeclared reports whether the presented Recipe " +
			"declares a workload body.\n\nIt answers whether the author wrote " +
			"the block at all, in any of the forms the block accepts — an " +
			"inline template, a file import, a library import — and never what " +
			"the body synthesizes to. The three forms are one authoring " +
			"decision and a rule asking whether a workload exists is asking " +
			"about that decision, so splitting them would hand every such rule " +
			"three answers to recombine.",
	},
	{
		Name:        "recipe-artifacts-declared",
		Stage:       "document",
		SourceField: "types.RecipeSpec.Artifacts",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "bool",
		Doc: "RecipeArtifactsDeclared reports whether the presented Recipe " +
			"declares any artifact entry.\n\nIt answers whether the block " +
			"carries an entry rather than how many, because a rule over the " +
			"Recipe's input kinds asks whether this one is present at all and " +
			"would read every count above zero the same way. A count is also a " +
			"different fact with a different life: an entry importing a bundle " +
			"expands into one output per file when it is resolved, so a number " +
			"the authored document carries is not the number of outputs. " +
			"Nothing here reads inside an entry.",
	},
	{
		Name:        "recipe-declared-capability",
		Stage:       "document",
		SourceField: "types.RecipeSpec.Capability",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "string",
		Doc: "RecipeDeclaredCapability is the capability the presented Recipe " +
			"declares, as the author wrote it.\n\nIt carries the declaration's " +
			"own text: the bare capability name where the author pinned no " +
			"version, and the name together with the pin where they did. " +
			"Nothing here splits the two apart. Which plugin version a pin " +
			"selects is settled where the binding is resolved, and a fact " +
			"answering that here would be answering about a provider under a " +
			"name that promises an answer about the document.\n\nA Recipe " +
			"declaring no capability answers with the empty string, which is " +
			"the authored state rather than a convenience over a missing one.",
	},

	{
		Name:        "recipe-output-label-keys",
		Stage:       "document",
		SourceField: "types.RecipeSpec.OutputLabels",
		Subject:     []string{"document", "output-label"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "[]string",
		Doc: "RecipeOutputLabelKeys are the keys the presented Recipe's " +
			"literal output-label block declares, in sorted order.\n\nThey are " +
			"the keys as authored, whatever namespace they sit in, and that is " +
			"what makes a rule over them possible: a key claiming a reserved " +
			"namespace is exactly what a set already filtered to the admissible " +
			"ones could not report. A Recipe declaring no such block declares " +
			"none.\n\nThe order is sorted rather than the author's, and the " +
			"departure from every other authored-key fact here is forced by the " +
			"block's own shape. The block is a mapping, whose iteration order is " +
			"unspecified, so there is no author order to carry and a walk would " +
			"otherwise present the keys differently on different runs. A rule " +
			"invoked once per key raises in invocation order, and that order is " +
			"the order the findings reach the reader — so sorting is what makes " +
			"the first finding the same one every time.",
	},
	{
		Name:        "recipe-output-component-label-keys",
		Stage:       "document",
		SourceField: "types.RecipeSpec.OutputComponentLabels",
		Subject:     []string{"document", "output-label"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "[]string",
		Doc: "RecipeOutputComponentLabelKeys are the component label keys the " +
			"presented Recipe promotes onto its rendered output, in author " +
			"order.\n\nIt is a second fact rather than the other half of one, " +
			"because the two blocks are written at two authored keys: a fact " +
			"naming both would be a fact about two sites, and a rule reading it " +
			"could not say which of them the author is being sent to. A Recipe " +
			"declaring no such block declares none.\n\nThe order is the author's " +
			"and is carried rather than sorted, because the block is a sequence " +
			"and a sequence has one. That is the other half of what the literal " +
			"block's own fact states about its sorting: the two orders differ " +
			"because the two blocks differ in shape, not because either rule " +
			"wanted a different one.",
	},

	{
		Name:        "manifest-recipe-mapping-entries",
		Stage:       "document",
		SourceField: "types.ManifestSpec.Mappings",
		Subject:     []string{"document", "mapping-entry"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "[]ManifestRecipeMapping",
		Doc: "ManifestRecipeMappingEntries are the entries of the presented " +
			"Manifest's recipe mapping table, in author order within each " +
			"axis: the components tier before the features tier, and within " +
			"each tier the type axis before the name axis.\n\nA mapping " +
			"entry is how a Manifest says which Recipe or FeatureRecipe " +
			"renders a component or a feature — the key names what is being " +
			"mapped and the value names the recipe. This fact is the entry " +
			"set; what one entry maps to is a fact of its own, keyed by the " +
			"entry.\n\nThe order inside an axis is the author's and is " +
			"carried rather than sorted, because a rule invoked once per " +
			"entry raises in invocation order and that order is the order " +
			"the findings reach the reader. The order BETWEEN the four is " +
			"fixed here rather than carried, because the two tiers are keys " +
			"of a mapping rather than a sequence and so is the pair of axes " +
			"inside a tier: there is no author order between them to carry, " +
			"and a rule reporting in invocation order needs one that does " +
			"not move.",
	},
	{
		Name:         "manifest-recipe-mapping-reference",
		Stage:        "document",
		SourceField:  "types.MappingTier",
		DocumentKind: "Manifest",
		Subject:      []string{"document", "mapping-entry"},
		Params: []paramDecl{
			{Name: "document", Type: "string"},
			{Name: "tier", Type: "string"},
			{Name: "axis", Type: "string"},
			{Name: "key", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "ManifestRecipeMappingReference is the Recipe or FeatureRecipe " +
			"reference one mapping entry names, and whether the presented " +
			"Manifest declares an entry at those coordinates at all.\n\nThe " +
			"second result is load-bearing on the terms every other " +
			"two-result fact states: coordinates nobody wrote and a declared " +
			"entry naming the empty reference are different findings, and a " +
			"rule that cannot tell them apart reports the wrong one.\n\nIts " +
			"source field names the structure and not one of its fields, " +
			"which is the honest naming here rather than a looser one. A " +
			"tier holds the type axis and the name axis as two sibling maps " +
			"of identical shape, and which of the two an entry sits in is " +
			"the entry's own axis coordinate — so naming either field would " +
			"state half the source and leave the other half to be discovered " +
			"from the accessor's parameters.",
	},
	{
		Name:        "mapped-recipe-implements",
		Stage:       "catalog",
		SourceField: "resolver.MappedRecipeImplements(catalog, tier, reference)",
		Subject:     []string{"document", "mapping-entry"},
		Params: []paramDecl{
			{Name: "tier", Type: "string"},
			{Name: "reference", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "MappedRecipeImplements is the type the Recipe or FeatureRecipe a " +
			"mapping reference names declares it implements, and whether that " +
			"reference resolves in the catalog at all.\n\nA recipe's declared " +
			"implements is a consistency anchor rather than a dispatch " +
			"mechanism: the mapping value names the recipe directly, and this " +
			"is what a rule compares the mapped key against.\n\nThe tier " +
			"selects which catalog kind the reference is resolved as, because " +
			"the components tier names Recipes and the features tier names " +
			"FeatureRecipes and the reference string alone does not say which. " +
			"One fact serves both rather than two facts serving one tier each, " +
			"because the pair is one question asked of whichever kind the entry " +
			"was written under — the tier is a coordinate of the entry, the way " +
			"the reference is, and a rule holding an entry holds both.\n\nThe " +
			"second result reports whether a recipe was found to read the " +
			"declaration off, and nothing finer. A reference the catalog holds " +
			"no entry for is a resolution concern owned by the path that " +
			"resolves references, which reports it with the richer context that " +
			"path has — including, for an entity skipped at a tolerant load, " +
			"why it went missing. So a rule reading this passes such an entry " +
			"over rather than reporting it, and the better diagnostic is the " +
			"one the author gets.\n\nIt is keyed per mapping entry although the " +
			"answer follows from the tier and the reference alone, for the " +
			"reason the catalog presence facts are keyed per attachment: the " +
			"stage is where the ANSWER is complete, while the key is what a " +
			"rule reading it is invoked once per, and a rule about a mapping " +
			"entry is invoked once per entry.\n\nIt is complete as soon as the " +
			"catalog is reachable — the answer is a function of a reference and " +
			"the catalog, and nothing normalized enters it — so it lands at the " +
			"catalog stage rather than at the one after it.",
	},
	{
		Name:        "mapped-type-extends-ancestry",
		Stage:       "catalog",
		SourceField: "resolver.CatalogLookup.ComponentAncestry(componentType)",
		Subject:     []string{"document", "mapping-entry"},
		Params:      []paramDecl{{Name: "componentType", Type: "string"}},
		Returns:     "([]string, bool)",
		Doc: "MappedTypeExtendsAncestry are the mapping keys of every base a " +
			"component type descends from, nearest parent first, and whether " +
			"the chain could be walked at all.\n\nEach ancestor is rendered as " +
			"the key an author would write for that base type, so a rule " +
			"offering an ancestor to the same comparison it applies to the " +
			"mapped key itself is offering the same shape of value. That is " +
			"what lets a Recipe written against a base serve everything " +
			"extending that base: the specialization needs a mapping entry and " +
			"no Recipe of its own.\n\nA type the catalog holds no entry for has " +
			"no ancestry and the chain is still walkable. The two are " +
			"deliberately not distinguished here, because \"the mapped type is " +
			"absent\" belongs to the path that resolves types and not to a " +
			"judgement about a mapping.\n\nThe second result reports a chain " +
			"that could not be walked — a loop, or a link naming a type that " +
			"does not resolve. It is the one outcome a rule cannot act on: the " +
			"mapping may be perfectly well written, and reporting it as the " +
			"fault would send the author to the wrong file. So a rule reading " +
			"this passes such an entry over, and the fault is reported by " +
			"whatever holds the resolution, once, rather than raised again by " +
			"every rule that met the absence.\n\nIt is keyed per mapping entry " +
			"for the reason the fact beside it is, and it is complete as soon " +
			"as the catalog is reachable: the chain is a function of a type " +
			"name and the catalog's own Component entries, and nothing " +
			"normalized enters it.\n\nThe order is the chain's and is carried " +
			"rather than sorted, because an extends chain is a sequence and has " +
			"one.",
	},

	{
		Name:        "feature-recipe-source-declared",
		Stage:       "document",
		SourceField: "types.FeatureRecipeSpec.Source",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "bool",
		Doc: "FeatureRecipeSourceDeclared reports whether the presented " +
			"FeatureRecipe declares a source pointer.\n\nIt answers whether the " +
			"author wrote the key at all, in either of the forms the key accepts " +
			"— a URI string or a structured object — and never what the pointer " +
			"names. What the pointer contains is the engine's contract to " +
			"define, so nothing here looks inside it.\n\nIt is a separate fact " +
			"from the values overlay beside it rather than the other half of " +
			"one, because the two are written at two authored keys: a fact " +
			"naming both source fields would be a fact about two sites, and a rule " +
			"consulting it could not say which of them the author is being sent " +
			"to.",
	},
	{
		Name:        "feature-recipe-values-mapping-declared",
		Stage:       "document",
		SourceField: "types.FeatureRecipeSpec.ValuesMapping",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "bool",
		Doc: "FeatureRecipeValuesMappingDeclared reports whether the presented " +
			"FeatureRecipe declares a values overlay.\n\nIt answers whether the " +
			"author wrote the block at all, in any of the forms the block " +
			"accepts — an inline template, a file import, a library import — and " +
			"never what the overlay resolves to. The three forms are one " +
			"authoring decision and a rule asking whether an overlay exists is " +
			"asking about that decision, so splitting them would hand every such " +
			"rule three answers to recombine.",
	},
	{
		Name:        "feature-recipe-workload-declared",
		Stage:       "document",
		SourceField: "types.FeatureRecipeSpec.Workload",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "bool",
		Doc: "FeatureRecipeWorkloadDeclared reports whether the presented " +
			"FeatureRecipe declares a workload body.\n\nIt answers whether the " +
			"author wrote the block at all, in any of the forms the block " +
			"accepts — an inline template, a file import, a library import — and " +
			"never what the body synthesizes to.\n\nWhether the bound provider " +
			"can synthesize a workload at all is a later question and a " +
			"different one, settled once the binding is resolved. A fact " +
			"answering it here would be answering about a provider under a name " +
			"that promises an answer about the document.",
	},
	{
		Name:        "feature-recipe-artifacts-declared",
		Stage:       "document",
		SourceField: "types.FeatureRecipeSpec.Artifacts",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "bool",
		Doc: "FeatureRecipeArtifactsDeclared reports whether the presented " +
			"FeatureRecipe declares any artifact entry.\n\nIt answers whether " +
			"the block carries an entry rather than how many, because the rules " +
			"over a FeatureRecipe's authored surface ask whether this one is " +
			"present at all and would treat every count above zero alike. A " +
			"count is also a different fact with a different life: an entry " +
			"importing a bundle expands into one output per file once it is " +
			"resolved, so a number the authored document carries is not the " +
			"number of outputs. Nothing here looks inside an entry.",
	},
	{
		Name:        "feature-recipe-declared-capability",
		Stage:       "document",
		SourceField: "types.FeatureRecipeSpec.Capability",
		Subject:     []string{"document"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "string",
		Doc: "FeatureRecipeDeclaredCapability is the capability the presented " +
			"FeatureRecipe declares, as the author wrote it.\n\nIt carries the " +
			"declaration's own text: the bare capability name where the author " +
			"pinned no version, and the name together with the pin where they " +
			"did. Nothing here splits the two apart. Which plugin version a pin " +
			"selects is settled where the binding is resolved, and a fact " +
			"answering that here would be answering about a provider under a " +
			"name that promises an answer about the document.\n\nA FeatureRecipe " +
			"declaring no capability answers with the empty string, which is the " +
			"authored state rather than a convenience over a missing one.",
	},
	{
		Name:        "release-boundary-placements",
		Stage:       "document",
		SourceField: "types.ReleaseSpec.Boundaries",
		Subject:     []string{"boundary-placement"},
		Returns:     "[]BoundaryPlacement",
		Doc: "ReleaseBoundaryPlacements is everything the presented Release's " +
			"boundaries place, in the order the document declares it — a " +
			"boundary's own component entries, then the sub-modules it routes and " +
			"the components those entries name, then the boundaries nested inside " +
			"it.\n\nIt reports the DECLARATIONS and never the reconciled component " +
			"set, and that difference is the whole of why it is a fact of its own. " +
			"The reconciled reading folds two entries resolving to one id together " +
			"and keeps the later, so a document placing one component twice " +
			"presents as a document that placed it once — precisely the shape a " +
			"rule over placement exists to see. Both readings are available from " +
			"one parsed document at any time, so this is a choice of source rather " +
			"than a moment: it is complete as soon as the document is parsed, it " +
			"stands unchanged for as long as that document is presented, and there " +
			"is no window outside which it answers differently.\n\nEach element " +
			"carries the coordinates that identify one placement: the authoring " +
			"document, the boundary, what the entry addresses, and the block it " +
			"was written in. The axis is part of the identity because one boundary " +
			"can name a component directly under the id it carries once the " +
			"composition is flattened and reach the same component again through a " +
			"routing entry, and because a routed sub-module's dotted prefix can " +
			"equal a component's id — so any two of the three leave pairs nothing " +
			"could tell apart.\n\nA routed sub-module appears whether or not its " +
			"entry names a component, because routing a module into a boundary " +
			"places every component that module contains: it is the only record of " +
			"a placement whose components cannot be enumerated until the module's " +
			"own document is loaded.\n\nIt walks the Release after " +
			"normalization, so a document declaring its components at the top " +
			"level rather than under named boundaries is covered by the same walk " +
			"— normalization gives such a document a boundary of its own holding " +
			"exactly those entries, and that boundary spells no level, so its " +
			"entries carry the empty boundary path and are named at the top-level " +
			"shape their author wrote.",
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
			"is the fact that answers which sockets exist to be asked about.\n" +
			"\nIt answers for a connection the consuming component's own " +
			"dependency block drew, and for one the Release's edge surface " +
			"wired, and for no other. Its key is a component type and a socket, " +
			"and that is the whole of it, while the socket a connection an " +
			"attachment drew is wired into is declared on the attached Feature's " +
			"own dependency sockets and never on the consuming component's. A " +
			"rule reading this fact alone would approve every attachment-drawn " +
			"dictation — silently, for exactly the connections it could not see " +
			"— so the rule over what a connection may dictate reads the " +
			"Feature-side counterpart beside it and picks between the two by the " +
			"surface that drew the connection.",
	},
	{
		Name:        "socket-declared-slot-names",
		Stage:       "normalized",
		SourceField: "types.UsesSchemaEntry.FeaturesSchema",
		Subject:     []string{"node", "socket"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "[]string",
		Doc: "SocketDeclaredSlotNames are the attachment slot names a dependency " +
			"socket declared on a component type lets each connection on it fill, " +
			"in author order.\n\nThe set CLOSES the surface it governs, which is " +
			"the difference between this and the slots a component type declares " +
			"for its own attachment block. A component's own block admits a name " +
			"no slot declares, so that set says which names carry a declaration to " +
			"check a fill against; a connection admits only the names this set " +
			"holds, so a rule reads it as the whole of what a connection may " +
			"carry.\n\nEmpty is the most constrained answer rather than an absent " +
			"one: a socket declaring no slot admits no attachment on its " +
			"connections. Whether the socket is declared at all is a separate " +
			"question — a socket the component type does not declare reads empty " +
			"here too, and declares no slot either — so a rule naming the reason " +
			"to an author reads the declared socket set beside this.",
	},
	{
		Name:        "attachment-socket-inject-mechanism-names",
		Stage:       "normalized",
		SourceField: "types.UsesSchemaEntry.Inject",
		Subject:     []string{"node", "attachment", "socket"},
		Params: []paramDecl{
			{Name: "featureType", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "[]string",
		Doc: "AttachmentSocketInjectMechanismNames are the delivery mechanisms a " +
			"dependency socket of the Feature an attachment resolves to allows a " +
			"connection wired into it to dictate, in author order.\n\nIt is the " +
			"Feature-side counterpart of the allowlist a component type's own " +
			"dependency socket declares, and the two are separate facts because " +
			"they read separate declaration surfaces: a connection an " +
			"attachment's own dependency block drew is wired into a socket the " +
			"attached Feature declares, never into one the consuming component " +
			"declares. One fact spanning both would have to pick a surface, and " +
			"would answer for the wrong one wherever the other applied.\n\nThe " +
			"names are an allowlist and nothing more: they constrain which " +
			"mechanisms a consumer may dictate delivery through, never what any " +
			"of them carries. The shape of one delivery entry is the entity " +
			"contract's to state, and restating it here would give one rule two " +
			"homes.\n\nEmpty is a declaration rather than an absence, and a rule " +
			"reading this fact must not take it for \"unconstrained\" — it is the " +
			"most constrained answer a socket can give. A socket naming no " +
			"mechanism permits no dictation at all, so a connection dictating any " +
			"delivery for it is refused. An omitted allowlist and an explicitly " +
			"empty one are deliberately the same answer, because reading them " +
			"apart would give an empty list a meaning no authoring surface " +
			"documents.\n\nWhether the socket carries a declaration at all is a " +
			"different question, and this fact does not answer it: a socket the " +
			"Feature declares no entry for reads the same as one declaring no " +
			"mechanism. A rule that turns on the difference reads the Feature's " +
			"declared socket set first.\n\nIt is keyed per attachment as well as " +
			"per node although the answer follows from the Feature type alone, " +
			"for the reason the Feature's declared socket set gives: a rule " +
			"refusing a dictation has to name the attachment that wrote it, which " +
			"is what an author can act on, where the type alone would leave them " +
			"looking for which attachment meant it.",
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
		Name:        "per-edge-attachment-names",
		Stage:       "normalized",
		SourceField: "types.VerboseUsesEntry.Features",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "[]string",
		Doc: "PerEdgeAttachmentNames are the names one connection of an instance's " +
			"own dependency block carries an attachment under, in author order — " +
			"each the slot the attachment claims to fill.\n\nIt names the " +
			"attachments and nothing about them, because the question a rule over " +
			"it asks is whether each name is one the connection's socket declares " +
			"a slot for, and the name is the whole of what that question reads. " +
			"Empty for a connection carrying no attachment and for one the short " +
			"authoring form wrote, which has nowhere to carry one.\n\nIt reads the " +
			"developer's connection entry and no other surface. The operator may " +
			"put attachments on the same connection from the Release, and that is " +
			"a separate fact with the same shape, because the two are written by " +
			"separate hands on separate documents and a finding has to name the " +
			"one its author wrote.",
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
			"surface's and does not carry to the Release's edge surface, whose " +
			"own connection count is a separate fact with the same shape and the " +
			"opposite rule — so a reader unifying the two makes one of them wrong. " +
			"There an entry counts only when it names a target, because an omitted " +
			"target falls back to nothing: on that surface the alias identifies " +
			"which existing connection the entry configures rather than supplying " +
			"a target for a new one, so an entry naming no target draws nothing. " +
			"One authored shape, two meanings, because the alias is doing a " +
			"different job on each surface.\n\nIt reads the instance's own " +
			"dependency block " +
			"and no other surface. An attachment's block wires sockets on the " +
			"Feature it attaches, and a Release's own edge surface wires sockets " +
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
			"Release's own edge surface is a separate fact with the same shape " +
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
		Doc: "AuthoredAttachmentType is the Feature type an Aggregate declares " +
			"for one attachment on one component, reported absent when the " +
			"Aggregate declares no attachment under that name for it.\n\nIt is " +
			"a separate fact from the type a Release declares for the same " +
			"attachment name, and the pair is what makes the agreement question " +
			"askable at all. Both documents may name one attachment — this one " +
			"carries the Aggregate's own configuration, the Release's carries " +
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
		Name:        "authored-annotation-subvalue",
		Stage:       "normalized",
		SourceField: "types.AnnotationValue",
		Subject:     []string{"authored-annotation"},
		Params: []paramDecl{
			{Name: "document", Type: "string"},
			{Name: "scope", Type: "string"},
			{Name: "component", Type: "string"},
			{Name: "key", Type: "string"},
			{Name: "subkey", Type: "string"},
		},
		Returns: "(any, bool)",
		Doc: "AuthoredAnnotationSubvalue is the value an author wrote at one " +
			"annotation entry, and whether an entry stands there at all.\n" +
			"\nIt is keyed by the site the entry was WRITTEN at rather than by " +
			"the component the entry takes effect on, and that is the whole " +
			"reason it is a fact of its own. An annotation written on a scope " +
			"reaches every component that scope holds, so it is authored once " +
			"and contributes many times: a fact keyed by a component would be " +
			"asked once per component about one authored string, would be asked " +
			"not at all for a scope holding none — silence indistinguishable " +
			"from approval — and could not be asked at all where two scopes " +
			"write against one component name, because the composition folds " +
			"those into one entry and keeps the last.\n\nThe site is the " +
			"authoring document, the scope's path within it, and the component " +
			"key the entry was written under. The component key is empty for an " +
			"entry written scope-wide, which is the shape a component-rooted " +
			"key cannot express rather than an edge of it: such an entry names " +
			"no component, and the scope's own block is the one place anyone " +
			"can edit it. The document is carried apart from the scope because " +
			"the two documents' scope paths are composed with separators of " +
			"their own, and a key that ran them together would rest on those " +
			"separators staying different with nothing comparing them.\n" +
			"\nThe subkey is the key written inside a structured value, and is " +
			"empty for a value written bare — so the empty subkey asks about " +
			"the whole entry. The result is the value as authored, at whatever " +
			"shape the author wrote it in, because what it is measured against " +
			"is a contract declared elsewhere and narrowing it here would " +
			"decide that measurement in advance.\n\nThe second result reports " +
			"whether this view holds a routed value at those " +
			"coordinates at all. Two shapes answer no and both are deliberate: " +
			"coordinates nobody wrote, and an entry written as an opt-out — an " +
			"explicit empty value, which is how an author removes an entry " +
			"inherited from a scope above. An opt-out routes nothing, so there " +
			"is no value to measure and no instance to be invoked for; a rule " +
			"that judged one would refuse an author for removing something.",
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
		Name:        "socket-slot-required",
		Stage:       "normalized",
		SourceField: "types.AttachmentSlot.Required",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "slot", Type: "string"},
		},
		Returns: "bool",
		Doc: "SocketSlotRequired reports whether one slot a component type's " +
			"dependency socket declares must be filled on every connection wired " +
			"through that socket.\n\nIt is a separate fact from the requirement a " +
			"component type's own slot declares, although both come from one " +
			"slot declaration, because the two are owed by different things. A " +
			"component's own slot is filled once for the component, by whichever " +
			"scope binds its name; a socket's slot is filled per connection, so the " +
			"demand is owed once by each connection on the socket and is keyed by " +
			"the connection rather than by the node.\n\nA slot the socket does not " +
			"declare is not required, which is the same answer as a declared slot " +
			"left optional: the question is whether a connection owes a fill, and " +
			"neither owes one.",
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
		Name:        "socket-slot-selector",
		Stage:       "normalized",
		SourceField: "types.AttachmentSlot.Selector",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "slot", Type: "string"},
		},
		Returns: "string",
		Doc: "SocketSlotSelector is the label constraint one slot a component " +
			"type's dependency socket declares on which Feature may fill it on a " +
			"connection, empty when it accepts any fill.\n\nIt is keyed per " +
			"attachment because the attachment a connection carries is what fills " +
			"the slot: a rule asks it of each such attachment a node carries, with " +
			"the socket and the slot the attachment's connection names. It is a " +
			"separate fact from the constraint a component type's own slot " +
			"declares, although both are written in one grammar on one kind of " +
			"declaration, because they sit on separate authored surfaces — a " +
			"socket's slots under its entry in the type's dependency sockets, a " +
			"component's own beside them — and a name may be declared on both " +
			"with different constraints.\n\nEmptiness is one answer rather than " +
			"two. A slot declared with no constraint and a slot the socket does not " +
			"declare both leave the fill unconstrained here, and whether a " +
			"connection may carry an attachment under a name at all is the socket's " +
			"slot-name set's question, asked when the documents are read.",
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
		Name:        "feature-conflicts-with",
		Stage:       "normalized",
		SourceField: "types.Interoperability.ConflictsWith",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "featureType", Type: "string"}},
		Returns:     "[]string",
		Doc: "FeatureConflictsWith are the Features a Feature declares it cannot " +
			"reach a component alongside, as its author wrote them and in author " +
			"order, empty when it declares none.\n\nThey are names and nothing " +
			"more: a rule over them compares each against the Features a " +
			"component carries, and reads no meaning into either side. The list " +
			"is the one the Feature carries once its extends chain has merged, so " +
			"a Feature declaring no list of its own answers with its base's.\n" +
			"\nIt is complete as soon as the catalog is reachable, so it is placed " +
			"there even though the rule reading it runs once the attachment " +
			"cascade has folded; a fact is placed at the earliest stage at which it " +
			"is complete, the way a Feature's applicability is.",
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
			"Release populates. A component's own dependency block may name a " +
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
			"Release may attach a Feature under a name no slot declares, so a " +
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
		Stage:       "catalog",
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
			"consult, which is settled where the view is produced.\n\nIt is " +
			"complete as soon as the catalog is reachable — the answer is a " +
			"function of a type name and the catalog, and nothing normalized " +
			"enters it — so it lands at the catalog stage rather than at the one " +
			"after it. Its stage and its key answer different questions and do " +
			"not have to agree: the stage is where the ANSWER is complete, while " +
			"the key is what a rule reading it is invoked once per. A rule asked " +
			"of an attachment is invoked where a node set exists and reads this " +
			"fact there, because a stage contains the stage it extends.",
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
			"Release document may not redirect a connection the architecture " +
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
		Name:        "release-per-edge-attachment-names",
		Stage:       "normalized",
		SourceField: "types.ReleaseUsesEntry.Features",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "[]string",
		Doc: "ReleasePerEdgeAttachmentNames are the names a Release's per-edge " +
			"entry carries an attachment under on one connection, in author order " +
			"— each the slot the attachment claims to fill.\n\nIt is the " +
			"operator's counterpart of the names the developer's connection entry " +
			"carries, and a separate fact for the reason the two documents' " +
			"per-edge configuration is: each is written by its own hand and is " +
			"independently absent, and a finding has to name the document whose " +
			"author wrote the name.\n\nIt answers for every component the Release " +
			"writes against, a component a sub-module contributes included, because " +
			"the Release addresses such a component by the identity the composition " +
			"gives it. Whether that component's type is knowable yet is a separate " +
			"question, answered by the stage that reads it.",
	},
	{
		Name:        "release-per-edge-attachment-type",
		Stage:       "normalized",
		SourceField: "types.ReleaseConnectionAttachment.Type",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "ReleasePerEdgeAttachmentType is the Feature type one attachment a " +
			"Release's per-edge entry carries names, reported absent when the entry " +
			"carries no attachment under that name.\n\nThe absence is reported rather " +
			"than resolved, and an empty type on an attachment that is present is " +
			"its own answer: the operator may write an attachment on a connection " +
			"to size one the developer already declared there, naming no type " +
			"because the developer's names it, so an empty type is a fault only " +
			"where the developer declared nothing under that name on that " +
			"connection. Reading that takes the developer's names beside this, " +
			"which is why this fact states the type and nothing about the other " +
			"document.\n\nIt is keyed by the connection and the attachment rather " +
			"than by the consuming component, because a connection's attachment " +
			"lives in that connection's entry and the component's own attachment " +
			"block is a separate surface with a fact of its own.",
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
			"appears there and counts nothing here." +
			perComponentBoundaryFold,
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
			"the other." +
			perComponentBoundaryFold,
	},
	{
		Name:        "release-component-entry-present",
		Stage:       "normalized",
		SourceField: "types.Boundary.Components",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "bool",
		Doc: "ReleaseComponentEntryPresent reports whether the Release wrote " +
			"an entry against one component at all.\n\nIt reports authorship and " +
			"nothing else, and an entry appears here whether or not it carries " +
			"any sizing. That rule is stated once for every surface an operator " +
			"writes entries on, and the reason it is needed here is the one the " +
			"per-connection authorship fact states: every field of such an entry " +
			"is optional, so an entry written and left empty carries nothing at " +
			"all, and the sizing fact declared beside this one reads its zero " +
			"value for it — the same zero it reads for a component the Release " +
			"never wrote against. The two need opposite treatment. An entry that " +
			"writes no sizing is the maximal violation of a key the component " +
			"type's contract declares required, while a component this Release " +
			"never placed is not its to answer for: a component nothing placed is " +
			"reported on its own once the composition is flattened, naming the " +
			"ways to place it, and measuring its absent sizing instead would " +
			"report a sizing fault against a document whose fault is " +
			"placement.\n\nAuthorship is reported " +
			"positively rather than inferred, and that is why the fact exists. " +
			"Emptiness is the value both cases produce, so the inference has " +
			"nothing to read.\n\nIt answers for both axes a boundary names a " +
			"component on — an entry under the boundary's own components, and an " +
			"entry naming a component inside a sub-module the boundary routes — " +
			"because on both the Release wrote against that component. A " +
			"routing entry naming no component writes against none, so it " +
			"contributes nothing here even though it places what the module " +
			"contains: this reports what the Release WROTE and never what it " +
			"placed, and a rule wanting placement reads the fact that names " +
			"it.\n\nA Release declaring components at its own top level rather " +
			"than under named boundaries answers here like any other. Normalizing " +
			"such a document gives it a boundary of its own holding exactly those " +
			"entries, so the fact covers that authoring form without a reader " +
			"knowing the form exists.",
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
			"it is one value for two documents that need opposite treatment: an " +
			"entry the Release wrote and left without sizing, and a component " +
			"the Release wrote no entry against at all. This fact cannot " +
			"separate them, so a rule that has to reads the authorship fact " +
			"declared beside it. What empty never means is that there is nothing " +
			"to measure — a key the component type's contract declares required " +
			"is violated exactly by the sizing nobody wrote.\n\n" +
			"The developer's configuration for the same component is " +
			"a different fact on a different surface. The two are written on " +
			"separate documents by separate hands, the component type declares " +
			"a separate contract for each, and each is independently absent.\n" +
			"\nAuthor order is carried because these entries reach rendered " +
			"output, and an unordered payload would put a byte-identical result " +
			"at the mercy of map iteration." +
			perComponentBoundaryFold,
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
			"Aggregate's own declaration carries its configuration, the " +
			"Release's carries the physical sizing — and the two are merged " +
			"into one attachment further down, which only means anything if they " +
			"name the same Feature. A rule reading one surface alone cannot see " +
			"a disagreement.\n\nThe absence is reported rather than resolved, " +
			"and it carries a distinction the rule turns on: an attachment only " +
			"the Aggregate declares is nothing to reconcile, an attachment " +
			"only the Release declares is the supported way to add one, and " +
			"an attachment naming no type states nothing to disagree with. All " +
			"three are legitimate, so only a name declared on both sides with a " +
			"type on both sides is a question.\n\nIt is a separate fact from " +
			"the attachment a boundary itself declares, which is keyed by that " +
			"boundary: this one is written against a named component and the " +
			"other applies to every component in a scope, so they are asked a " +
			"different number of times and a rule over either names a different " +
			"authored site." +
			perComponentBoundaryFold,
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
			"sockets the Aggregate's own attachment of the same name wires. " +
			"Both documents may declare an attachment under one name and each " +
			"wires what it wires, so a rule reading one surface alone would " +
			"measure the other's sockets against nothing.\n\nIt is also a " +
			"separate fact from the sockets an attachment written at a scope " +
			"wires, which is keyed by that scope: this one is written against a " +
			"named component, so the two are asked a different number of times " +
			"and a rule over either names a different authored site." +
			perComponentBoundaryFold,
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
			"three facts sharing one source field that can only be absent or present " +
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
		Name:        "compiled-feature-schema",
		Stage:       "catalog",
		SourceField: "resolver.CompileFeatureSchema(catalog, featureType)",
		Subject:     []string{"document", "annotation-binding"},
		Params:      []paramDecl{{Name: "featureType", Type: "string"}},
		Returns:     "(map[string]any, bool)",
		Doc: "CompiledFeatureSchema is the contract a Feature type declares over " +
			"the pillars an attachment of it may carry, resolved: the authored " +
			"document with every shorthand expanded and every cross-document " +
			"reference followed, and whether there was a contract that resolved " +
			"at all.\n\nIt is the Feature's counterpart to a component type's " +
			"resolved contract, and a separate fact rather than the same one over " +
			"another key, for the reason the presence facts beside them are " +
			"separate: the two are different catalog kinds reached through " +
			"different lookups, so one fact spanning both would have to pick a " +
			"kind and would answer for the wrong one wherever the other " +
			"applied.\n\nWhere a Feature composes onto a parent, the composed " +
			"contract is what resolves, not the child's own text. An attachment " +
			"inherits the parent's pillar keys, so a reader handed the child-only " +
			"document would find a key absent that the attachment may legitimately " +
			"carry.\n\nThe second result reports whether there is a resolved " +
			"contract to read, and nothing finer. It does not separate a Feature " +
			"declaring no contract, a Feature type the catalog does not hold, and " +
			"a contract that failed to resolve: all three arrive as the same " +
			"negative, and whether the catalog holds the type at all is the " +
			"separate fact beside this one. A contract that failed to resolve is " +
			"reported by whatever holds the resolution, once, rather than left to " +
			"every rule reading a bare absence to infer — a view that " +
			"resolves the bound Features up front keeps that failure and reports " +
			"it, while one that resolves per call keeps nothing afterwards and " +
			"reports nothing, which is why a rule over this fact is invoked on a " +
			"view that holds the resolution.\n\nIt is complete as soon as the " +
			"catalog is reachable — the contract is a function of a Feature type " +
			"and the catalog, and nothing normalized enters it — so it lands at " +
			"the catalog stage. That is what lets a rule reading it be invoked " +
			"over a view holding one authored Manifest and the catalog, rather " +
			"than over a compilation it has no question about.\n\nThe resolved " +
			"document holds no order. A rule selects from it by key, which iterates nothing; one " +
			"that walked the whole document to build output would put a " +
			"byte-identical result at the mercy of map iteration.",
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
			"composition is flattened — so the fact over the Aggregate's own " +
			"instance list cannot see it, and a rule reading that alone reports " +
			"every such component as not existing and then declines to judge it. " +
			"This reads the flattened node instead, where a node contributed by " +
			"any document carries its type the same way.\n\nOne read serves " +
			"both tiers and there is deliberately no second path to keep in " +
			"step. A node the Aggregate declared directly and a node a " +
			"composition contributed are the same shape here, so a rule reading " +
			"this needs no fallback and cannot acquire two behaviours that drift " +
			"apart.\n\nIt does not replace the fact over the Aggregate's own " +
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
		Name:        "node-mapped-recipe-reference",
		Stage:       "topology",
		SourceField: "resolver.NodeRecipeResolution.Reference",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "(string, bool)",
		Doc: "NodeMappedRecipeReference is the Recipe reference the Manifest's " +
			"mapping table names for a node, verbatim as the author wrote it, " +
			"and whether the mapping reached that node at all.\n\nIt is a fact " +
			"of the compilation keyed by a node, and not a reading of the " +
			"Manifest. The question it answers is what THIS NODE resolved to, " +
			"which the mapping table cannot answer on its own: resolution is " +
			"name-wins-over-type, so the answer depends on the identity the " +
			"composition gave the node as much as on the type it carries, and a " +
			"node a sub-aggregate contributed carries a qualified identity no " +
			"authored document spells. The compiler already performs that " +
			"resolution to decide which Recipe the node renders through, so this " +
			"projects an answer already reached rather than reaching for a " +
			"document.\n\nThat is why it lands where the flattened node set is " +
			"complete rather than where the Manifest is first in hand. Keyed " +
			"earlier it would be asked only about the nodes one authoring " +
			"document declares, and a rule over it would report success for " +
			"every component a sub-aggregate contributes — a silence " +
			"indistinguishable from approval.\n\nThe second result reports " +
			"whether a mapping key reached the node, and nothing finer. A lookup " +
			"that REFUSED reports not-mapped here too, because it named no " +
			"reference to return; the keys it refused over are a separate fact, " +
			"and a rule that has to tell an unmapped node from a refused lookup " +
			"reads that one first. Collapsing the two would send an author to " +
			"write a mapping entry for a type the table already maps twice.\n\n" +
			"A reference is what the author wrote and not a resolved entity. " +
			"Whether the catalog holds a Recipe at it is a separate fact, and a " +
			"rule needing both asks both.",
	},
	{
		Name:        "node-recipe-mapping-ambiguous-keys",
		Stage:       "topology",
		SourceField: "resolver.NodeRecipeResolution.AmbiguousKeys",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "NodeRecipeMappingAmbiguousKeys are the version-distinct mapping " +
			"keys a node's component type matched when no key matched it exactly " +
			"and more than one matched its base, in the order the mapping table " +
			"declares them.\n\nIt is empty when the lookup settled — on one key, " +
			"or on none. So a non-empty answer is the refusal itself rather than " +
			"a hint about one, and a rule reads this before asking whether the " +
			"node was mapped: the refusal names no reference, so it reports as " +
			"unmapped there, and a rule reading that alone would tell the author " +
			"to add an entry the table already carries twice.\n\nThe keys travel " +
			"rather than a bare refusal flag because they are the remedy. An " +
			"author shown which keys collide can pin the authored type's version " +
			"or remove a duplicate key; an author told only that a lookup was " +
			"ambiguous can do neither, and the compiler is the only party that " +
			"knows which keys matched.\n\nA type the table maps at two distinct " +
			"versions is legal authoring and is deliberately not reported here. " +
			"An exact key match wins before any base match is considered, so an " +
			"instance whose authored type pins a version resolves against that " +
			"key and this fact stays empty for it. The refusal is relative to the " +
			"INSTANCE, not to the table: the same table refuses an unpinned " +
			"instance and resolves a pinned one, which is why the fact is keyed " +
			"by a node rather than by a mapping entry.\n\nThe order is the " +
			"mapping table's own and is carried rather than sorted, so a " +
			"diagnostic names the keys in the order the author reads them in the " +
			"document.",
	},
	{
		Name:        "node-mapped-recipe-present",
		Stage:       "topology",
		SourceField: "resolver.NodeRecipeResolution.Present",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "bool",
		Doc: "NodeMappedRecipePresent reports whether the catalog holds a Recipe " +
			"at the reference the Manifest's mapping names for a node.\n\nIt is " +
			"false for a node no mapping reached, because there is no reference " +
			"to resolve — so it is read together with the reference fact beside " +
			"it rather than alone. The pair separates the two faults an author " +
			"acts on differently: a node nothing maps needs a mapping entry " +
			"written, and a node mapped at a reference the catalog does not hold " +
			"needs the reference corrected or the Recipe published.\n\nIt is a " +
			"catalog read keyed by a node, and the key is what places it. The " +
			"answer follows from the reference and the catalog alone, both of " +
			"which are settled earlier; the NODE is what a rule over it is " +
			"invoked once per, and the node set is complete only once the " +
			"composition is flattened.\n\nA reference the catalog does not hold " +
			"reports false whatever the reason — a Recipe absent outright, one " +
			"present at a version the reference does not select, and one whose " +
			"bare name two capabilities both answer to all read the same here. " +
			"Which of those it was belongs to the path that resolves references, " +
			"which reports it with the context that path has; this says only that " +
			"nothing the author can be said to have chosen came back.",
	},
	{
		Name:        "node-recipe-pin-candidates",
		Stage:       "topology",
		SourceField: "resolver.NodeRecipeResolution.PinCandidates",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "NodeRecipePinCandidates are the catalog Recipes whose declared " +
			"implements satisfies a node's component type, as the identities an " +
			"author would write into a mapping value, sorted.\n\nThey are a " +
			"suggestion and never a resolution. Resolution is mapping-only: the " +
			"compiler never chooses a Recipe by what it declares it implements, " +
			"so a Recipe named here is one the author may pin and never one the " +
			"build will pick. The declaration informs the choice, the author " +
			"makes it, and the mapping entry records it.\n\nThe set is resolved " +
			"only for a node that needs one, and is empty otherwise. A node the " +
			"Manifest already maps has had its choice made, so offering " +
			"candidates for it would name alternatives to a decision already " +
			"taken; a node whose lookup refused has a fault in the mapping " +
			"table's keys rather than a missing entry, so candidates would " +
			"suggest the wrong edit. An empty answer therefore means no " +
			"candidate is OFFERED, which is not the same as no Recipe " +
			"implementing the type existing — a rule distinguishing those reads " +
			"the mapping facts beside this one first.\n\nThe identities come " +
			"back sorted. They are gathered from a recipe index that holds no " +
			"authoring order, so there is no author order to preserve and " +
			"sorting is what makes the same input produce the same diagnostic on " +
			"every run.\n\nIt is deliberately blind to a type's extends " +
			"ancestry, which is a narrower set than the rule judging whether an " +
			"operator-written mapping is consistent reads. Widening the " +
			"suggestions to Recipes implementing a base would change which " +
			"candidates a diagnostic lists, and what to suggest is a separate " +
			"choice from what to accept.",
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
			"Release's edge surface created — the latter decided by the " +
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
		Name:        "flattened-connection-attachment-names",
		Stage:       "topology",
		SourceField: "topology.TopologyNode.Uses",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "types.OrderedMap[types.OrderedMap[[]string]]",
		Doc: "FlattenedConnectionAttachmentNames are the attachments the " +
			"connections of a node in the flattened set carry, keyed by socket and " +
			"then by alias, each listing the names its attachments are written " +
			"under, in author order at every level.\n\nIt answers for every node " +
			"the composition holds, which is what separates it from the names read " +
			"off the Aggregate's own instance list: a component a sub-module " +
			"contributes keeps its dependency block in that module's document, " +
			"read only when the composition is flattened, so the fact over the " +
			"authoring document reports every such connection as carrying " +
			"nothing.\n\nOnly a connection carrying at least one attachment " +
			"appears, and a socket appears only through such a connection. It is " +
			"keyed by the node and answers for the node's whole block, because a " +
			"rule over one connection reads that connection's names out of it and a " +
			"rule over a component entry the Release wrote has to see every " +
			"connection the node's block carries an attachment on.",
	},
	{
		Name:        "flattened-connection-attachment-type",
		Stage:       "topology",
		SourceField: "types.ConnectionAttachment.Type",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "FlattenedConnectionAttachmentType is the Feature type one attachment " +
			"the developer wrote on one connection of a node in the flattened set " +
			"names, reported absent when that connection carries no attachment " +
			"under the name.\n\nIt is the developer's counterpart of the type a " +
			"Release's per-edge entry names for the same attachment, and the pair " +
			"is what a rule comparing the two documents reads: each is written by " +
			"its own hand on its own document, and a disagreement between them is " +
			"visible in neither alone.\n\nIt answers for every node the composition " +
			"holds, for the reason the attachment names on the same connections " +
			"are read here: a component a sub-module contributes keeps its " +
			"dependency block in that module's document, which is read only when " +
			"the composition is flattened.",
	},

	{
		Name:        "per-edge-authored-target",
		Stage:       "topology",
		SourceField: "topology.NormalizedEdge.AuthoredTarget",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "string",
		Doc: "PerEdgeAuthoredTarget is the target one connection names, as the " +
			"document that drew it wrote the name — empty when the composition " +
			"drew no connection there.\n\nIt answers for every connection " +
			"whichever surface drew it: a component's own dependency block, an " +
			"attachment's, and the Release's edge surface alike. That breadth " +
			"is what separates it from the authored targets read off the two " +
			"authoring documents, each of which answers for one surface and one " +
			"authoring form and reports every other connection as naming " +
			"nothing.\n\nIt is a separate fact from the target a connection " +
			"RESOLVED to, and the pair is not interchangeable. A connection may " +
			"name a module's published alias rather than a component id; " +
			"resolving that alias replaces the resolved target with the id the " +
			"module publishes it for, while this keeps the name the author " +
			"typed. So a rule comparing what one document wrote against what " +
			"another wrote reads this on both sides — reading the resolved " +
			"target for one of them would report a disagreement between two " +
			"documents that named the very same alias.\n\nIt is keyed by the " +
			"connection rather than by the surface that drew it, because a " +
			"connection is drawn once: where two surfaces write one node, socket " +
			"and alias, the composition keeps one connection and this names the " +
			"target that connection carries.",
	},

	{
		Name:        "per-edge-connection-drawn",
		Stage:       "topology",
		SourceField: "topology.TopologyResult.Edges",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
		},
		Returns: "bool",
		Doc: "PerEdgeConnectionDrawn reports whether the composition drew a " +
			"connection at one coordinate, whichever surface drew it.\n\nIt is " +
			"membership of the connection set and nothing finer. A rule asking " +
			"what an authored entry achieved needs this and cannot take it from " +
			"the target a connection names: that fact answers with the empty " +
			"string where nothing was drawn, and an authored target may itself " +
			"be empty, so the two cases share one answer there and a reader " +
			"taking emptiness for absence would read a drawn connection as one " +
			"that was never drawn. Membership is a separate question from what " +
			"the member carries, and it is declared separately for that reason " +
			"rather than folded into the answer beside it.\n\nIt answers for " +
			"the whole connection set rather than for one surface's " +
			"contribution, which is what a rule over an entry that configures " +
			"rather than creates needs: such an entry applies to a connection " +
			"some other document drew, so a reading bounded to the entry's own " +
			"surface would report every one of them as carrying nothing.\n\n" +
			"False is the answer for a coordinate no surface reached and for " +
			"one whose consuming component the composition does not hold, and " +
			"the two are deliberately one answer: no connection was drawn " +
			"either way. A rule needing to tell them apart reads the flattened " +
			"node set beside this, which is the fact reporting membership of " +
			"the composition.",
	},

	{
		Name:        "release-authored-edge-configurations",
		Stage:       "topology",
		SourceField: "types.BoundaryComponent.Uses",
		Subject:     []string{"release-edge-configuration"},
		Returns:     "[]ReleaseEdgeConfiguration",
		Doc: "ReleaseAuthoredEdgeConfigurations is every entry the Release's per-edge " +
			"`uses` surface writes, in the order the document declares them — an " +
			"enclosing boundary before the boundaries nested inside it, and " +
			"within a boundary its components in author order, each component's " +
			"sockets in author order and each socket's aliases in author " +
			"order.\n\nIt reports what the Release WROTE and never which of " +
			"those entries reached a connection. The connection set holds one " +
			"instance per connection DRAWN, so an entry that drew none — one " +
			"carrying configuration for a connection some other document was " +
			"expected to draw, where nothing drew it — is absent from that set " +
			"by construction. A rule over such an entry can therefore be invoked " +
			"over this axis and over no other, which is the whole of why the " +
			"axis exists.\n\nEach element carries the coordinates that identify " +
			"one entry, and the boundary is among them because the Release " +
			"writes this surface inside a boundary's own component entry: that " +
			"entry is the site an author edits, and two boundaries writing one " +
			"component are two separate blocks. Nothing is folded away between " +
			"them — a Release that places one component in two boundaries is " +
			"refused when the document loads, so by the time anything reads this " +
			"fact each component is written in exactly one boundary and the " +
			"reconciled reading beside it loses no entry.\n\nIt walks the " +
			"Release after normalization, so a document declaring its components " +
			"at the top level rather than under named boundaries is covered by " +
			"the same walk — normalization gives such a document a boundary of " +
			"its own holding exactly those entries, and that boundary spells no " +
			"level, so its entries carry the empty boundary path and are named " +
			"at the top-level shape their author wrote.\n\nIt is the Release's " +
			"surface alone. An Aggregate writes per-edge entries too, in a " +
			"component's own dependency block, and those are read by the facts " +
			"over that surface: they are written by another hand on another " +
			"document and carry another contract, so one axis spanning both " +
			"would answer a rule about the operator's entries with the " +
			"developer's." +
			declarationCompelsImplementation,
	},

	{
		Name:        "node-boundary-placement",
		Stage:       "topology",
		SourceField: "topology.TopologyNode.BoundaryPath",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "(string, bool)",
		Doc: "NodeBoundaryPlacement is the boundary a node was placed in, and " +
			"whether anything placed it at all.\n\nA Release places a " +
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
			"is emitted outside every boundary the Release declared, which " +
			"is a defect the build otherwise reports nowhere, so a rule needs to " +
			"be able to name it.\n\nAn empty boundary path reports as unplaced " +
			"rather than as a placement carrying no path, and the two are " +
			"deliberately one answer: a component whose boundary path is empty " +
			"renders in the position an unplaced one does, taking its output " +
			"location, sync wave and annotations from the same nowhere, and earns " +
			"the same report. A caller wanting to tell the two apart cannot do it " +
			"from this fact.\n\nThe path is the boundary's own nesting path rather than " +
			"one authored key, because boundaries nest and two boundaries at " +
			"different depths may carry one name.",
	},

	{
		Name:        "node-declaring-subsystems",
		Stage:       "topology",
		SourceField: "topology.TopologyResult.DeclaringSubsystems",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "NodeDeclaringSubsystems are the subsystems that declared a " +
			"component under the identity a node carries, in the order the " +
			"composition met each declaration.\n\nOne entry is the ordinary " +
			"answer and means one component was declared under that identity. " +
			"More than one means several were, which is the whole reason the " +
			"fact is a list: a subsystem contributes nothing to the identity a " +
			"component carries, so two subsystems of one Aggregate may each " +
			"declare a component under the same name — two distinct authored " +
			"keys the schema accepts — and the two then answer to one " +
			"identity.\n\nIt is a separate fact from the node set rather than " +
			"something read off it, and it has to be: the node set holds one " +
			"component per identity by construction, so the second declaration " +
			"replaces the first there and nothing downstream can tell the pair " +
			"from a single component. This is taken as each declaration is met " +
			"and keeps both, which is what makes the pair answerable at " +
			"all.\n\nThe value is complete when the composition is, and it stays " +
			"complete for as long as the composition does. It is not a reading of " +
			"something mid-production: nothing prunes it once the walk has " +
			"finished, and reading it at any later point gives the same answer as " +
			"reading it the moment flattening returned.\n\nEach subsystem is named " +
			"by its dotted path within the document that declared the component, " +
			"so a component a module contributed names the subsystems of that " +
			"module's own document. An empty result means no component of that " +
			"identity was composed — a name the composition never held, or one a " +
			"false activation toggle removed, which the facts beside this one tell " +
			"apart.",
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

	{
		Name:        "component-deactivated",
		Stage:       "topology",
		SourceField: "topology.TopologyResult.Pruned",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "bool",
		Doc: "ComponentDeactivated reports whether the composition removed a " +
			"component because its activation toggle read false.\n\nIt is the " +
			"distinction the fact naming a flattened node's type declines to " +
			"draw, and it is declared separately for that reason rather than " +
			"folded into it. That fact reports membership of the flattened set " +
			"and nothing finer, so a name absent from it was never contributed " +
			"OR was contributed and then removed — two findings a rule may need " +
			"to treat oppositely. The composition keeps the removed names, so " +
			"the distinction survives flattening and is answerable here.\n\nThe " +
			"two are asked together by any rule that would otherwise refuse a " +
			"document an author deliberately wrote. Switching a component off is " +
			"an authoring act, and the Release that configures it is not " +
			"expected to be rewritten in the same edit — so a rule refusing a " +
			"reference to a component that is not in the flattened set has to " +
			"pass a deactivated one, or the toggle would be unusable while any " +
			"Release named the component.\n\nA name the composition never " +
			"held answers false, exactly as a live component does. The two are " +
			"distinguished by the flattened-set fact beside this one and never " +
			"by this one alone, which reports removal and never existence.",
	},

	{
		Name:        "boundary-declared-component-references",
		Stage:       "topology",
		SourceField: "types.Boundary.Components",
		Subject:     []string{"boundary-component-reference"},
		Returns:     "[]string",
		Doc: "BoundaryDeclaredComponentReferences are the components the " +
			"Release's boundaries name, by the id each entry addresses in the " +
			"flattened composition, in the order the Release declares them — " +
			"a boundary's own entries before the sub-modules it routes, and an " +
			"enclosing boundary before the boundaries nested inside it.\n\nA " +
			"boundary names a component on two axes and this answers for both, " +
			"which is what makes it a reading of what the Release declared " +
			"rather than of one spelling. One axis names a component under the id " +
			"it carries once the composition is flattened; the other routes a " +
			"sub-module and names a component inside it under the local name it " +
			"carries within that module, because inside the module that is the " +
			"only name it has. The second is resolved to the same id the first " +
			"writes, so a reader cannot be written against one axis and quietly " +
			"ignore the other, and a reader that took a routed entry's local name " +
			"as an id would refuse every module-routed Release that names " +
			"components.\n\nIt reports what the Release ASKED FOR and never " +
			"what the composition holds, and the two are separate on purpose. An " +
			"entry addressing an id the composition does not hold appears here " +
			"exactly as one that resolves does: the Release wrote it either " +
			"way, and whether anything answers to it is a different question, " +
			"asked of the flattened set by the fact keyed per node. A reader " +
			"needing both asks both.\n\nIt is keyed per reference because that is " +
			"where the question is asked — a rule over an entry is invoked once " +
			"per entry the Release wrote — and read with no key because the " +
			"set is the axis itself. The id identifies the entry across the whole " +
			"Release rather than within one boundary, and that rests on a rule " +
			"rather than on the authoring surface: the rule over the placements " +
			"beside this fact refuses a document that places one component twice, " +
			"and it is asked when the Release document loads, so by the time " +
			"anything reads this fact no two entries share an id. A reader " +
			"needing the entries a document DECLARES, collisions and all, asks " +
			"that fact instead — the two are separate for exactly that " +
			"reason.\n\nAn " +
			"entry the Release wrote at its own top level appears here like " +
			"any other. Normalizing such a document gives it a boundary of its " +
			"own holding exactly those entries, so the axis covers that authoring " +
			"form without a reader knowing the form exists.",
	},

	{
		Name:        "boundary-attachment-reached-nodes",
		Stage:       "topology",
		SourceField: "topology.TopologyNode.BoundaryPath",
		Subject:     []string{"boundary", "attachment"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "[]string",
		Doc: "BoundaryAttachmentReachedNodes are the nodes one attachment a " +
			"boundary declares on itself reaches once the composition is " +
			"flattened — every node placed in that boundary or in a boundary " +
			"nested inside it, in the order the composition produced them — and " +
			"none when the boundary declares no attachment under that name.\n\n" +
			"It reads the placement each node carries rather than the components " +
			"the boundary names, and the difference is what makes it the reach " +
			"rather than one spelling of it. A boundary places a component it " +
			"names and every component of a sub-module it routes in, and a " +
			"routed module's components exist only once that module's own " +
			"document is read — so the answer is complete here and nowhere " +
			"earlier, and a reading of the boundary's own entries would never " +
			"reach a routed module's components at all.\n\nThe nested boundaries " +
			"are included because a boundary-wide attachment cascades: written on " +
			"a boundary, it is attached to every component beneath it at any " +
			"depth. A component a false activation toggle removed was never " +
			"composed, so nothing reaches it.\n\nIt is keyed by the attachment " +
			"as well as the boundary because that is where the question is " +
			"asked: a rule over a boundary-wide attachment is invoked once per " +
			"attachment the operator wrote, and reads what that attachment " +
			"reaches. Every attachment one boundary declares reaches the same " +
			"nodes.",
	},

	// ------------------------------------------------------------------- graph

	{
		Name:        "per-leaf-attachment-provenance",
		Stage:       "graph",
		SourceField: "types.IRFeatureEntry.ValuesProvenance and .InfraProvenance",
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
			"meaningless.\n\nEach pillar has its own source field, and the pillar " +
			"coordinate is what selects between them: an attachment's " +
			"configuration is recorded as one provenance map per pillar, so " +
			"asking about a leaf of the developer's configuration and asking " +
			"about a leaf of the operator's read different maps under one fact. " +
			"Both source fields are named in this declaration for that reason — a " +
			"reader reconciling the placement table against the code would " +
			"otherwise find one of the two and conclude the other pillar is " +
			"unaccounted for. A pillar neither source field is kept for yields the " +
			"zero record, which is the same answer as a leaf the author wrote " +
			"with no annotation behind it and deliberately so: every field a rule " +
			"reads is absent in both cases.",
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
			"to every surface. A Release addresses a component a " +
			"composition contributed by the qualified identifier the " +
			"composition gave it, while the Aggregate's own scope tree keys " +
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
			"separate source field, and a rule reaching for the declaration needs the " +
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
			"empty for exactly those Releases that rely on sharing; a rule " +
			"reading the authored names in place of these rejects a Release " +
			"shape the product supports.\n\nThe fact is a name list and " +
			"not the merged entries themselves. Handing back the entries would " +
			"let a rule reach the per-leaf configuration provenance the " +
			"framework already exposes as a fact of its own, so a rule could " +
			"re-derive a declared fact from this one — which is the shape every " +
			"return here exists to keep closed.",
	},
	{
		Name:        "attachment-contributions",
		Stage:       "graph",
		SourceField: "ir.AttachmentSites",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "[]AttachmentContribution",
		Doc: "AttachmentContributions are the places one attachment a node carries " +
			"was contributed from, in the order the attachment cascade folded " +
			"them: the Aggregate's own tiers before the Release's, and an outer " +
			"scope before the scopes nested inside it. The same place is named " +
			"once however many times it contributed.\n\nIt is held rather than " +
			"derived because the graph cannot answer it. The merged entry keeps " +
			"the winning configuration and whether an annotation produced it, and " +
			"never which tiers contributed the attachment, so which subsystem, " +
			"boundary or Release entry wrote it is settled while the cascade " +
			"folds and is gone once it finishes. Reconstructing it from the " +
			"authoring documents afterwards would mean walking the scope trees a " +
			"second time, beside the walk that produced the merged set, and the " +
			"two would be free to disagree about which scopes a node sits " +
			"under.\n\nIt is a separate fact from the site that won one leaf of " +
			"the attachment's configuration. That one names the single place a " +
			"leaf's value came from; this names every place the attachment itself " +
			"was written, including one whose configuration every other place " +
			"displaced and one that wrote no configuration at all.",
	},
	{
		Name:        "attachment-connection",
		Stage:       "graph",
		SourceField: "types.IRFeatureEntry.Connection",
		Subject:     []string{"node", "attachment"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "(AttachmentConnection, bool)",
		Doc: "AttachmentConnection is the connection one attachment a node " +
			"carries was written on — the socket, the alias and the slot it fills " +
			"there — reported absent for an attachment the node's own block or a " +
			"scope above it supplied.\n\nIt is taken from the record the lowering " +
			"carried onto the attachment and never out of the attachment's name. " +
			"An attachment a connection carries reaches its node under a composed " +
			"name, and a rule recovering the socket, the alias or the slot by " +
			"splitting that name would be a second spelling of the composition, " +
			"free to disagree with the one that produced it.\n\nIt is placed where " +
			"the attachment cascade has folded because the mark is set while the " +
			"cascade folds: the developer's and the operator's contributions to one " +
			"connection attachment are merged into one attachment there, and either " +
			"may be the one that carried the connection.",
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
		Name:        "originated-connection-authored-delivery",
		Stage:       "graph",
		SourceField: "ir.OriginatedConnections",
		Subject:     []string{"originated-connection"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "origin", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "socket", Type: "string"},
			{Name: "alias", Type: "string"},
			{Name: "target", Type: "string"},
		},
		Returns: "[]types.OrderedMap[any]",
		Doc: "OriginatedConnectionAuthoredDelivery is the delivery a connection " +
			"dictates AS IT WAS WRITTEN, keyed by mechanism name in author " +
			"order, each entry carrying that mechanism's payload as authored.\n" +
			"\nIt keeps the distinction the resolved per-edge delivery folds " +
			"away. An entry may deliver a literal or may reference a key the " +
			"target publishes, and the resolved form holds one string per " +
			"delivery key for both — so a rule refusing a reference to a key " +
			"nothing publishes, read over the resolved form, would refuse every " +
			"literal too. The payload is carried rather than decomposed because " +
			"the shape of one delivery entry is the entity contract's to state.\n" +
			"\nIt is keyed by the origin as well as the connection because the " +
			"connection triple is not an identity. Two attachments on one node " +
			"may each write one socket and one alias, and the component itself " +
			"may write the same pair, so a fact keyed by node, socket and alias " +
			"answers for one of up to three separately authored blocks and " +
			"reports the others as approved. The node the connection resolves to is in " +
			"the key for the same reason: two origins writing one socket and one " +
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
			"travels unchanged from there; the surface each origin was declared " +
			"on is settled by the reading that folds the attachment cascade, and " +
			"a fact keyed by an origin is complete only once that is.\n\nWhat a delivery " +
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
			"source field, so a rule matching the two composes this fact with that " +
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
		Name:        "node-declares-authored-source",
		Stage:       "described",
		SourceField: "types.RecipeSpec.Source",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "bool",
		Doc: "NodeDeclaresAuthoredSource reports whether a node's Recipe declares " +
			"source the author supplied, taking the text that Recipe wrote rather " +
			"than anything the build resolved.\n\nIt is the authored " +
			"reading on purpose, and the two part: a Recipe whose source entry is " +
			"a template evaluating to nothing declares source and renders none. " +
			"The authored reading is the one available before anything is " +
			"evaluated, which is where a rule over authored text runs, and it is " +
			"also the one an author can act on — the entry they wrote is the entry " +
			"they would change.\n\nWhat it is for is telling two absences apart " +
			"where the remedy differs. A node with no supplied source has its " +
			"output named after its own identity, so an author reaching for a name " +
			"that does not exist there has a correct value already available; a " +
			"node that DOES supply source and whose target still reports no name " +
			"has no such substitute, and a rule offering one would send its reader " +
			"to a value that is wrong for them.\n\nFalse is a real answer and " +
			"covers a node with no bound Recipe as well as one whose Recipe " +
			"declares no source: neither supplies source for a target to name " +
			"output from, which is the one question this fact is asked.",
	},
	{
		Name:        "node-rendered-name",
		Stage:       "described",
		SourceField: "rendercore.RenderedNameOutput.Name",
		Subject:     []string{"node"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "string",
		Doc: "NodeRenderedName is the name the provider rendering a node's own " +
			"documents reports it will give the resources it renders from source " +
			"the author supplied, empty when it reports none.\n\nIt is the name " +
			"an author's own templates render against — what a chart reads as its " +
			"release name — and it is a different value from the identity the " +
			"compiler stamps on the resources it built itself. The two are " +
			"separate facts because they answer separate questions, and a single " +
			"value answering both is what leaves a published address naming a " +
			"resource that does not exist: the two are written in different " +
			"alphabets, so for some authored identities they differ.\n\nEmptiness " +
			"is one answer rather than several, and the reason is what the rule " +
			"reading it asks. A node with no bound Recipe, a provider that " +
			"reports no such name, a provider that reports one only for a node " +
			"whose Recipe supplies source, and a binding the build could not " +
			"resolve to a provider all leave an author with no such name to read. " +
			"That is one answer to the question the fact is asked for, and " +
			"distinguishing them would put a second condition on it that the " +
			"reading cannot act on differently.\n\nThe value is composed inside " +
			"the provider's own guest and is carried here unread, so nothing " +
			"about what that target accepts in a name — its alphabet, its " +
			"grammar, its length — reaches a rule through this fact. A rule may " +
			"ask whether a name exists and may spend the string; it cannot learn " +
			"the target's naming from it.",
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
		Name:        "boundary-attachment-execution-scope",
		Stage:       "described",
		SourceField: "types.FeatureRecipeSpec.ExecutionScope",
		Subject:     []string{"boundary", "attachment"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
		},
		Returns: "string",
		Doc: "BoundaryAttachmentExecutionScope is the scope the FeatureRecipe one " +
			"attachment declared on a boundary binds states its resource generation " +
			"runs at, empty when it states none.\n\nDeclaring an attachment ON a " +
			"boundary and declaring a FeatureRecipe that runs ONCE PER boundary are " +
			"two separate decisions, made in two documents by two people, and " +
			"neither implies the other. An attachment written at a boundary whose " +
			"FeatureRecipe states no scope reaches every component that boundary " +
			"holds and is evaluated once for each of them, against the full " +
			"per-component context. So the boundary an attachment sits on does not " +
			"say which surface it runs on, and a rule about the boundary-scope " +
			"surface that assumed it would report on attachments that never reach " +
			"that surface.\n\nIt is the boundary-rooted form of the same question " +
			"the node-rooted axis already answers, and the two are separate facts " +
			"rather than one reached differently: no node-rooted key can name an " +
			"attachment written against a boundary that holds no component.\n\n" +
			"Emptiness is a real answer and is the ordinary one: a FeatureRecipe " +
			"stating no scope generates per component, which is also what an " +
			"attachment with no bound FeatureRecipe amounts to.",
	},
	{
		Name:        "authored-node-template-text",
		Stage:       "described",
		SourceField: "renderer.RecipeTemplateFields(recipe)",
		Subject:     []string{"node", "template-field"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "string",
		Doc: "AuthoredNodeTemplateText is the template text a node's own Recipe " +
			"writes for one of its inline template fields, as written and before " +
			"anything is evaluated.\n\nThe field is a coordinate rather than part of " +
			"this fact's name, and that is the whole shape rather than a detail of it. " +
			"A Recipe carries several template fields and gains more over time; naming " +
			"each in its own fact would put the field identity in structure, so a " +
			"field added later would need a fact, a coordinate, a traversal entry and " +
			"a declaration of its own — and would be silently ungated if any of the " +
			"four were forgotten. Keyed by the field, the axis is data: one more label " +
			"is enumerated and nothing else moves.\n\nOne fact serves every field " +
			"because one reference set does. Every field it reaches is evaluated " +
			"against the node's own render context, one type, so what a template on " +
			"any of them may name is one question with one answer. The fields differ " +
			"in the delimiter pair their text is parsed with rather than in what they " +
			"may name, and which pair applies follows from the field, which the reader " +
			"already holds as a coordinate.\n\nThe fact is the text and not an " +
			"analysis of it, on the reading every authored-text fact carries: a " +
			"collected reference set is a judgement whose direction decides which " +
			"rules may consume it, and handing over the text leaves that direction to " +
			"the rule that needs it.\n\nWhat it does NOT reach is stated because an " +
			"unstated bound reads as coverage. A Recipe's provides and locals entries " +
			"are absent, each having its own axis at this same stage, so a rule over " +
			"one of those entries is invoked over its own subject rather than twice. " +
			"Its assertions are absent because that surface is evaluated against a " +
			"context carrying no identity block at all. And a field whose content is " +
			"imported from a file or a library is not enumerated, because that content " +
			"is resolved when the render reaches it and there is nothing authored in " +
			"this document to hand over.",
	},
	{
		Name:        "attachment-authored-template-text",
		Stage:       "described",
		SourceField: "renderer.AttachmentTemplateFields(fr)",
		Subject:     []string{"node", "attachment", "template-field"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "string",
		Doc: "AttachmentAuthoredTemplateText is the template text the FeatureRecipe " +
			"one attachment binds on a node writes for one of its inline template " +
			"fields, as written and before anything is evaluated.\n\nThe field is a " +
			"coordinate rather than part of this fact's name, on the reading both " +
			"sibling authored-template-text facts carry: a FeatureRecipe carries " +
			"several template fields and gains more over time, so naming each in its " +
			"own fact would put the field identity in structure and a field added " +
			"later would be silently ungated if any of its declarations were " +
			"forgotten. Keyed by the field, the axis is data.\n\nIt is keyed by the " +
			"node as well as the attachment, and neither coordinate is redundant. The " +
			"attachment selects the document the text was written in; the node selects " +
			"the render context that text is judged against, because a per-component " +
			"attachment is evaluated once for each component it reaches and its " +
			"context is derived from that component's own. This is what separates it " +
			"from the boundary-rooted fact beside it, where one authored document " +
			"reaches every component the boundary holds and no single component " +
			"selects a context.\n\nThe fact is the text and not an analysis of it, on " +
			"the reading every authored-text fact carries: a collected reference set " +
			"is a judgement whose direction decides which rules may consume it, and " +
			"handing over the text leaves that direction to the rule that needs " +
			"it.\n\nWhat it does NOT reach is stated because an unstated bound reads " +
			"as coverage. The FeatureRecipe's provides and locals entries are absent, " +
			"each already refused directly by the scope those two surfaces are " +
			"evaluated against, so a reference one of them makes is reported against " +
			"its own surface rather than twice. Its inject mapping is absent because " +
			"that surface is evaluated against the consuming node's delivery pipe " +
			"rather than against this context, so a reference set derived from this " +
			"one would be the wrong set for it. And a field whose content is imported " +
			"from a file or a library is not enumerated, because that content is " +
			"resolved when the render reaches it and there is nothing authored in this " +
			"document to hand over.",
	},
	{
		Name:        "boundary-attachment-authored-template-text",
		Stage:       "described",
		SourceField: "renderer.BoundaryScopeFRTemplateFields(fr)",
		Subject:     []string{"boundary", "attachment", "template-field"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "string",
		Doc: "BoundaryAttachmentAuthoredTemplateText is the template text the " +
			"FeatureRecipe one attachment declared on a boundary writes for one of " +
			"its inline template fields, as written and before anything is " +
			"evaluated.\n\nThe field is a coordinate rather than part of this " +
			"fact's name, and that is the whole shape rather than a detail of it. " +
			"A FeatureRecipe carries several template fields and gains more over " +
			"time; naming each in its own fact would put the field identity in " +
			"structure, so a field added later would need a fact, a coordinate, a " +
			"traversal entry and a declaration of its own — and would be silently " +
			"ungated if any of the four were forgotten. Keyed by the field, the " +
			"axis is data: one more label is enumerated and nothing else " +
			"moves.\n\nOne fact serves every field because one reference set does. " +
			"Within one FeatureRecipe the render context is one type, so what a template on " +
			"any of these fields may name is one question with one answer, and a " +
			"rule reading this fact decides it the same way whichever field it was " +
			"invoked for.\n\nThe fact is the text and not an analysis of it, on the " +
			"reading every authored-text fact carries: a collected reference set is " +
			"a judgement whose direction decides which rules may read it, and " +
			"handing over the text leaves that direction to the rule that needs " +
			"it.\n\nIt covers the fields written inline and says so rather than " +
			"leaving the bound implied. A field whose content is imported from a " +
			"file or a library is not enumerated at all, because that content is " +
			"read when the render reaches it and there is nothing authored in this " +
			"document to hand over.",
	},
	{
		Name:        "boundary-authored-path-template-text",
		Stage:       "described",
		SourceField: "renderer.BoundaryPathTemplateFields(b)",
		Subject:     []string{"boundary", "path-template-field"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "string",
		Doc: "BoundaryAuthoredPathTemplateText is the template text one boundary of " +
			"a Release writes for one of its path fields, as written and before " +
			"anything is evaluated.\n\nThe field is a coordinate rather than part of " +
			"this fact's name, and that is the whole shape rather than a detail of " +
			"it. A boundary writes several path templates and may gain more; naming " +
			"each in its own fact would put the field identity in structure, so a " +
			"field added later would need a fact, a coordinate, a traversal entry " +
			"and a declaration of its own — and would be silently ungated if any of " +
			"the four were forgotten. Keyed by the field, the axis is data: one more " +
			"name is enumerated and nothing else moves.\n\nOne fact serves every " +
			"field even though the reference sets differ, and that is worth stating " +
			"because it is the reverse of the FeatureRecipe this shape was first built " +
			"on. There, one set served the whole FeatureRecipe and the fact carried the " +
			"question whole. Here the two fields are checked against two different " +
			"admitted sets over one context type — a whole-boundary template is " +
			"evaluated once for the scope, so a single component's id would make the " +
			"emitted path depend on evaluation order, while a per-component segment " +
			"is evaluated once for each component placed. The fact stays one because " +
			"it hands over TEXT: which set applies follows from the field, which the " +
			"reader already has as a coordinate.\n\nThe fact is the text and not an " +
			"analysis of it, on the reading every authored-text fact carries: a " +
			"collected reference set is a judgement whose direction decides which " +
			"rules may read it, and handing over the text leaves that direction to " +
			"the rule that needs it.\n\nIt covers what the SCOPE declares and says " +
			"so rather than leaving the bound implied. The same field may be " +
			"overridden on a component inside the scope, which no boundary key " +
			"enumerates, so that text is not answered here; and a field the operator " +
			"left empty carries nothing, because what the render evaluates in its " +
			"place is a default this compiler supplies rather than anything anybody " +
			"authored.",
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

	// The five facts below are keyed by an AUTHORED artifacts entry rather than
	// by an artifact, and the distinction is the whole reason the coordinate
	// exists. An artifact is named by the output an entry resolved to, which for
	// an entry importing a bundle is one name per file that bundle holds — so
	// the artifact coordinate exists only once the import has SUCCEEDED, and a
	// rule keyed by it is invoked no times at all for exactly the entries these
	// facts exist to refuse. The authored key is there from the moment the
	// Recipe is bound, whatever the reference resolves to or fails to.
	{
		Name:        "artifacts-entry-import-reference",
		Stage:       "described",
		SourceField: "types.RecipeSpec.Artifacts",
		Subject:     []string{"node", "artifacts-entry"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "entry", Type: "string"},
		},
		Returns: "string",
		Doc: "ArtifactsEntryImportReference is the library reference one entry of " +
			"a node's own artifacts block was authored with, as written and before " +
			"anything resolves it.\n\nIt is the bare-name " +
			"`<Library>#/artifacts/<bundle>` form and only that form. An entry " +
			"written inline answers empty, because it names no library; so does one " +
			"supplied by a file of the workspace, which names a path rather than a " +
			"library; and so does one whose reference carries a registry host, " +
			"which is refused where it is consumed as a host-bearing reference " +
			"rather than read as a library one. A rule reading this therefore asks " +
			"about the one authoring form it governs and is handed nothing for the " +
			"others, rather than having to tell them apart itself. A reference " +
			"carrying no `#/artifacts/` fragment is not a library reference at all " +
			"— the consumption site reads it as a path to open — so it answers " +
			"empty here too.\n\nIt is the " +
			"text as the author wrote it and not a parsed form, because what a rule " +
			"over it reports is the string the author has to correct, and because " +
			"the library half and the bundle half are read back out of it by the " +
			"same split the consumption site resolves it with.\n\nA node with no " +
			"bound Recipe, and a key that Recipe does not " +
			"declare, both answer empty — the keys are enumerated rather than " +
			"guessed, so a key reaching here that the document does not write names " +
			"nothing.",
	},
	{
		Name:        "artifacts-entry-library-declared",
		Stage:       "described",
		SourceField: "renderer.RenderInput.ArtifactLibraries",
		Subject:     []string{"node", "artifacts-entry"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "entry", Type: "string"},
		},
		Returns: "bool",
		Doc: "ArtifactsEntryLibraryDeclared reports whether the ArtifactLibrary " +
			"one entry of a node's own artifacts block names is in the set the " +
			"catalog resolved — the local catalog plus any lockfile-pinned " +
			"libraries bridged in at load.\n\nIt is false for an entry naming no " +
			"library at all, and false for one whose reference does not split into " +
			"the bare-name shape: neither names a library the set could hold. A " +
			"rule telling those cases apart from a library genuinely absent from " +
			"the set reads the reference beside this, which is why the two are " +
			"declared as separate facts rather than folded into one answer that " +
			"would report three situations as one.\n\nIt is answered from the same " +
			"resolution the artifacts walk expands a bundle through, so a library " +
			"this reports present is the library that walk will find. Two readings " +
			"of one set would agree the day they were written and would be free to " +
			"differ afterwards, with nothing comparing them.",
	},
	{
		Name:        "artifacts-entry-bundle-declared",
		Stage:       "described",
		SourceField: "renderer.RenderInput.ArtifactLibraries",
		Subject:     []string{"node", "artifacts-entry"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "entry", Type: "string"},
		},
		Returns: "bool",
		Doc: "ArtifactsEntryBundleDeclared reports whether the ArtifactLibrary " +
			"one entry of a node's own artifacts block names declares the bundle " +
			"that entry's reference asks for.\n\nIt is false wherever the library " +
			"itself was not found, because a library the set does not hold declares " +
			"nothing — so a rule refusing an absent bundle reads the library fact " +
			"beside this one and reports the library instead when that is what is " +
			"missing. Answering the bundle question emptily for an absent library " +
			"is what keeps the two refusals from both firing for one authoring " +
			"mistake, and reading them in that order is what puts the author in " +
			"front of the outer fault first.\n\nIt is declared apart from the " +
			"library fact rather than folded into a single resolution verdict " +
			"because the two are separate remedies: one is a library to publish or " +
			"to pin, the other a bundle name to correct inside a library that is " +
			"already there.",
	},
	{
		Name:        "artifacts-entry-bundle-file-names",
		Stage:       "described",
		SourceField: "types.ArtifactLibraryEntry.Files",
		Subject:     []string{"node", "artifacts-entry"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "entry", Type: "string"},
		},
		Returns: "[]string",
		Doc: "ArtifactsEntryBundleFileNames are the names of the files the bundle " +
			"one entry of a node's own artifacts block imports holds, in the order " +
			"that bundle declares them.\n\nDeclared order is the fact rather than " +
			"an incidental property of it: each file becomes one output nested " +
			"under the authored key, in this order, so a rule invoked over the " +
			"names reports its findings in the order the outputs are produced and " +
			"in the same order on every run.\n\nThe names come from a document the " +
			"consuming Recipe did not write, which is the whole reason a rule reads " +
			"them. A name is composed with the authored key before it becomes an " +
			"output, so a name climbing out of that key resolves somewhere the " +
			"consuming Recipe never named — and once the two halves are joined a " +
			"climbing segment reads as an ordinary interior one.\n\nIt is empty " +
			"for an entry that imports no bundle and for one whose library or " +
			"bundle was not found: there is no bundle to hold files in either case, " +
			"which is the same answer an empty bundle gives and is the ordinary " +
			"outcome rather than an approval.",
	},
	{
		Name:        "artifacts-entry-bundle-nested-import-files",
		Stage:       "described",
		SourceField: "types.ArtifactLibraryEntry.Files",
		Subject:     []string{"node", "artifacts-entry"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "entry", Type: "string"},
		},
		Returns: "[]string",
		Doc: "ArtifactsEntryBundleNestedImportFiles are the names of the files " +
			"whose own body is a field-root import, inside the bundle one entry of " +
			"a node's own artifacts block imports, in the order that bundle " +
			"declares its files.\n\nIt is a separate fact from the bundle's file " +
			"names rather than something a rule derives from them, because the two " +
			"read different halves of the bundle: the names answer what each file " +
			"will be called, and this answers what a file's body IS. A rule handed " +
			"the names alone could not ask the second question at all, and one " +
			"handed every body would be reading file content in order to ask a " +
			"yes-or-no of each — which is the same reading the consumption site " +
			"makes, and a second copy of it would be free to disagree about what a " +
			"field-root import looks like.\n\nA bundle file body is terminal " +
			"content: one file slot cannot expand into another bundle, so a body " +
			"that is itself a field-root import is refused rather than " +
			"mis-resolved, and composing across libraries is done at the " +
			"consumption site instead.\n\nIt is empty for an entry that imports no " +
			"bundle, for one whose library or bundle was not found, and — the " +
			"ordinary answer — for a bundle whose every file body is template " +
			"content.",
	},

	// The three facts below are keyed by a spec-root FIELD of a node's own
	// Recipe rather than by anything that field's content resolves to, and the
	// distinction is the whole reason the coordinate exists. A values mapping
	// supplied by a library import has no authored text at all in the Recipe
	// that consumes it — the text arrives when the render resolves the import —
	// so the template-field coordinate beside this one does not name it, and a
	// rule keyed by that coordinate is invoked no times for exactly the fields
	// these facts exist to refuse. The field name is there from the moment the
	// Recipe is bound, whatever the reference resolves to or fails to.
	{
		Name:        "recipe-values-mapping-import-reference",
		Stage:       "described",
		SourceField: "types.RecipeSpec.ValuesMapping",
		Subject:     []string{"node", "recipe-import-field"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "string",
		Doc: "RecipeValuesMappingImportReference is the library reference a node's " +
			"own Recipe supplies its values mapping by, as written and before " +
			"anything resolves it.\n\nIt answers for the values-mapping field and " +
			"for no other field the coordinate names, so a rule reading it is " +
			"handed the one field it governs and is not left telling the Recipe's " +
			"import-bearing fields apart itself.\n\nIt is the bare-name " +
			"`<Library>#/mappings/<entry>` form and only that form. A values " +
			"mapping written inline answers empty, because it names no library; so " +
			"does one supplied by a file of the workspace, which names a path " +
			"rather than a library; and so does one whose reference carries a " +
			"registry host, which is refused where it is consumed as a " +
			"host-bearing reference rather than read as a library one.\n\nIt is " +
			"the text as the author wrote it and not a parsed form, because what a " +
			"rule over it reports is the string the author has to correct, and " +
			"because the library half and the entry half are read back out of it " +
			"by the same split the consumption site resolves it with.\n\nA node " +
			"with no bound Recipe answers empty — the fields are enumerated rather " +
			"than guessed, so a coordinate reaching here that the document does " +
			"not write names nothing.",
	},
	{
		Name:        "recipe-values-mapping-library-declared",
		Stage:       "described",
		SourceField: "renderer.RenderInput.MappingLibraries",
		Subject:     []string{"node", "recipe-import-field"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "bool",
		Doc: "RecipeValuesMappingLibraryDeclared reports whether the MappingLibrary " +
			"a node's own Recipe supplies its values mapping from is in the set the " +
			"catalog resolved — the local catalog plus any lockfile-pinned " +
			"libraries bridged in at load.\n\nIt is false for a field naming no " +
			"library at all, and false for one whose reference does not split into " +
			"the bare-name shape: neither names a library the set could hold. A " +
			"rule telling those cases apart from a library genuinely absent from " +
			"the set reads the reference beside this, which is why the two are " +
			"declared as separate facts rather than folded into one answer that " +
			"would report three situations as one.\n\nIt is answered from the same " +
			"resolution the render resolves a values-mapping import through, so a " +
			"library this reports present is the library that resolution will " +
			"find. Two readings of one set would agree the day they were written " +
			"and would be free to differ afterwards, with nothing comparing them.",
	},
	{
		Name:        "recipe-values-mapping-entry-declared",
		Stage:       "described",
		SourceField: "renderer.RenderInput.MappingLibraries",
		Subject:     []string{"node", "recipe-import-field"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "bool",
		Doc: "RecipeValuesMappingEntryDeclared reports whether the MappingLibrary a " +
			"node's own Recipe supplies its values mapping from declares the entry " +
			"that reference asks for.\n\nIt is false wherever the library itself " +
			"was not found, because a library the set does not hold declares " +
			"nothing — so a rule refusing an absent entry reads the library fact " +
			"beside this one and declines when that is what is missing. Answering " +
			"the entry question emptily for an absent library is what keeps the " +
			"two questions from both being reported for one authoring mistake, and " +
			"reading them in that order is what puts the author in front of the " +
			"outer fault first.\n\nIt is declared apart from the library fact " +
			"rather than folded into a single resolution verdict because the two " +
			"are separate remedies: one is a library to publish or to pin, the " +
			"other an entry name to correct inside a library that is already " +
			"there.",
	},

	// The three facts below are the workload half of the same coordinate. They
	// are declared apart from the values-mapping three rather than answered by
	// one pair of accessors taking a field name, because a fact is one accessor
	// answering one question: the two fields are supplied from different library
	// kinds, their references carry different fragment surfaces, and a single
	// accessor spanning both would have to decide internally which set to read
	// and would answer for whichever field was asked last.
	{
		Name:        "recipe-workload-import-reference",
		Stage:       "described",
		SourceField: "types.RecipeSpec.Workload",
		Subject:     []string{"node", "recipe-import-field"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "string",
		Doc: "RecipeWorkloadImportReference is the library reference a node's own " +
			"Recipe supplies its workload by, as written and before anything " +
			"resolves it.\n\nIt answers for the workload field and for no other " +
			"field the coordinate names, so a rule reading it is handed the one " +
			"field it governs and is not left telling the Recipe's import-bearing " +
			"fields apart itself.\n\nIt is the bare-name " +
			"`<Library>#/workloads/<entry>` form and only that form. A workload " +
			"written inline answers empty, because it names no library; so does one " +
			"supplied by a file of the workspace, which names a path rather than a " +
			"library; and so does one whose reference carries a registry host, " +
			"which is refused where it is consumed as a host-bearing reference " +
			"rather than read as a library one.\n\nIt is the text as the author " +
			"wrote it and not a parsed form, because what a rule over it reports is " +
			"the string the author has to correct, and because the library half and " +
			"the entry half are read back out of it by the same split the " +
			"consumption site resolves it with.\n\nA node with no bound Recipe " +
			"answers empty — the fields are enumerated rather than guessed, so a " +
			"coordinate reaching here that the document does not write names " +
			"nothing.",
	},
	{
		Name:        "recipe-workload-library-declared",
		Stage:       "described",
		SourceField: "renderer.RenderInput.WorkloadLibraries",
		Subject:     []string{"node", "recipe-import-field"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "bool",
		Doc: "RecipeWorkloadLibraryDeclared reports whether the WorkloadLibrary a " +
			"node's own Recipe supplies its workload from is in the set the catalog " +
			"resolved.\n\nIt is false for a field naming no library at all, and " +
			"false for one whose reference does not split into the bare-name shape: " +
			"neither names a library the set could hold. A rule telling those cases " +
			"apart from a library genuinely absent from the set reads the reference " +
			"beside this, which is why the two are declared as separate facts " +
			"rather than folded into one answer that would report three situations " +
			"as one.\n\nIt is answered from the same resolution the render resolves " +
			"a workload import through, so a library this reports present is the " +
			"library that resolution will find. Two readings of one set would agree " +
			"the day they were written and would be free to differ afterwards, with " +
			"nothing comparing them.",
	},
	{
		Name:        "recipe-workload-entry-declared",
		Stage:       "described",
		SourceField: "renderer.RenderInput.WorkloadLibraries",
		Subject:     []string{"node", "recipe-import-field"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "bool",
		Doc: "RecipeWorkloadEntryDeclared reports whether the WorkloadLibrary a " +
			"node's own Recipe supplies its workload from declares the entry that " +
			"reference asks for.\n\nIt is false wherever the library itself was not " +
			"found, because a library the set does not hold declares nothing — so a " +
			"rule refusing an absent entry reads the library fact beside this one " +
			"and declines when that is what is missing. Answering the entry " +
			"question emptily for an absent library is what keeps the two questions " +
			"from both being reported for one authoring mistake, and reading them " +
			"in that order is what puts the author in front of the outer fault " +
			"first.\n\nIt is declared apart from the library fact rather than " +
			"folded into a single resolution verdict because the two are separate " +
			"remedies: one is a library to publish or to pin, the other an entry " +
			"name to correct inside a library that is already there.",
	},

	// The fact below is keyed by the source POSITION of a node's own Recipe, a
	// coordinate distinct from the import-field one above it. A Recipe's source
	// entry carries a path rather than an import, so the import axis does not name
	// it and its own contract refuses the widening: an axis narrowed to admit one
	// more rule would mean that rule rather than the document. The position exists
	// once per bound Recipe whether or not the author wrote anything at it, which
	// is what lets a fact about the entry's PRESENCE be keyed there at all.
	{
		Name:        "recipe-source-entry-written",
		Stage:       "described",
		SourceField: "types.RecipeSpec.Source",
		Subject:     []string{"node", "recipe-source-field"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "field", Type: "string"},
		},
		Returns: "bool",
		Doc: "RecipeSourceEntryWritten reports whether a node's own Recipe writes " +
			"the source entry at all, taking the presence of the key the author " +
			"typed and nothing about what that key holds.\n\nIt is presence and " +
			"never emptiness, and the two are declared apart because they are two " +
			"different questions with two different answers for one document. " +
			"Whether the entry NAMES anything is already declared, over the node, " +
			"and it answers false for an absent entry and for an entry holding an " +
			"empty string alike — which is the right reading for every rule asking " +
			"what a node renders from, and the wrong one for the single rule asking " +
			"whether the author left a key behind with nothing in it. A rule " +
			"telling those two states apart reads both facts; one fact answering " +
			"both would have to report two authoring situations as one.\n\nIt is " +
			"deliberately not the emptiness condition in a second spelling. That " +
			"condition has one home, which every reader of it reaches, and a copy " +
			"here would agree the day it was written and would be free to differ " +
			"afterwards with nothing comparing them — so this reads the entry's " +
			"presence and composes with the declared reading for the rest.\n\nA " +
			"node with no bound Recipe answers false: there is no document to have " +
			"written the entry in. A coordinate naming a field other than the " +
			"source entry answers false for the same reason the other Recipe " +
			"spec-root facts do, so a rule over this axis is handed the one " +
			"position it governs.",
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
			"happened — and the report of facts sharing a source field pairs them by " +
			"stage, source field and subject together, so it does not pair these two " +
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
		SourceField: "recipepin.Lock.ArtifactDigests",
		Subject:     []string{"node", "artifact"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "ArtifactRecordedContentPin is the content digest recorded beside a " +
			"node's Recipe for one of its artifacts, and whether the entry carries " +
			"a recorded pin at all.\n\nThe second result is load-bearing and is not " +
			"a convenience over an empty string. A Recipe no pull has pinned has " +
			"no pin recorded, an entry whose authoring form carries none records " +
			"none either, and a rule that could not tell those from a pin whose " +
			"value happened to be empty would either refuse every unpinned " +
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
		Name:        "artifact-imported-path",
		Stage:       "hydrated",
		SourceField: "types.RecipeSpec.Artifacts",
		Subject:     []string{"node", "artifact"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "string",
		Doc: "ArtifactImportedPath is the file one of a node's artifacts was " +
			"supplied from, for the one authoring form supplied from a file at " +
			"all.\n\nIt is the path as the catalog resolved it, which is absolute: " +
			"a relative reference is rewritten against the document that authored " +
			"it when that document is read, so this names a file a reader can open " +
			"without first working out which entity the entry was written in. That " +
			"is the whole of why the fact exists. An entry's coordinate is the " +
			"output name it resolves to, which is chosen independently of the file " +
			"and says nothing about where the content came from — so a reader " +
			"holding the coordinate has to go and find the file, and every " +
			"occurrence costs that search again.\n\nIt is a bare string with no " +
			"second result, and the asymmetry with the recorded pin declared beside " +
			"it is deliberate. A pin's VALUE may legitimately be empty, so presence " +
			"there is a question the value cannot answer; a path's cannot be empty, " +
			"because the only entries recorded are those whose import reference was " +
			"non-empty. The empty string therefore already says an entry was not " +
			"supplied from a file, and a second result would be a second answer to " +
			"that one question, free to disagree with it.\n\nAn entry written " +
			"inline answers empty, and so does one supplied by an entry of a " +
			"library bundle: neither names a file of the workspace, and a bundle's " +
			"own filename is a name inside a resolved artifact rather than a path " +
			"to open. A node the render did not reach answers empty too, on the " +
			"reading every fact of this stage carries.\n\nIt is declared beside " +
			"the entry's template text and keyed the same way, because a rule with " +
			"a reason to name what an entry holds has the same reason to name where " +
			"the content came from — and the two are read together or the second is " +
			"read for an entry the first never resolved.",
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
		SourceField: "recipepin.Lock.ArtifactDigests",
		Subject:     []string{"node", "attachment", "artifact"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "AttachmentArtifactRecordedContentPin is the content digest recorded " +
			"beside the FeatureRecipe one attachment binds on a node, for one of its " +
			"artifacts, and whether the entry carries a recorded pin at all.\n\n" +
			"Every reading the node-scoped form carries applies here over a " +
			"different document: the second result separates an unpinned " +
			"FeatureRecipe and an entry whose form carries no pin from a pin whose " +
			"value is empty, and a pin is recorded for exactly one authoring form, " +
			"the entry supplied by an imported file.\n\nIt is keyed by the " +
			"attachment as well as by the node for the reason the text fact beside " +
			"it is: the attachment's render walks the same producer under the HOST " +
			"component's node identity, so a pin keyed by the node alone would " +
			"measure the attachment's own content against whatever was recorded " +
			"for the host's Recipe — and two attachments on one node record " +
			"independently, with nothing making their output names distinct.",
	},
	{
		Name:        "attachment-artifact-imported-path",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.Artifacts",
		Subject:     []string{"node", "attachment", "artifact"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "string",
		Doc: "AttachmentArtifactImportedPath is the file one artifact of the " +
			"FeatureRecipe one attachment binds on a node was supplied from.\n\n" +
			"Every reading the node-scoped form carries applies here over a " +
			"different document: the value is the path the catalog resolved, which " +
			"is absolute, and an entry written inline or supplied by a library " +
			"bundle answers empty because neither names a file of the workspace.\n" +
			"\nIt is keyed by the attachment as well as by the node for the reason " +
			"the two facts declared beside it are: the attachment's render walks " +
			"the same producer under the HOST component's node identity, so a key " +
			"carrying only the node would answer with whatever file the host's own " +
			"Recipe imported under the same output name — naming the wrong file on " +
			"a finding about this FeatureRecipe's content, which is worse than " +
			"naming none. Two attachments on one node import independently, with " +
			"nothing making their output names distinct.",
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
		SourceField: "recipepin.Lock.ArtifactDigests",
		Subject:     []string{"boundary", "attachment", "artifact"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "(string, bool)",
		Doc: "BoundaryAttachmentArtifactRecordedContentPin is the content digest " +
			"recorded beside the FeatureRecipe one boundary-scope attachment binds " +
			"for one of its artifacts, and whether the entry carries a pin at all." +
			"\n\nEvery reading the two node-rooted forms carry applies here: " +
			"the second result separates an unpinned FeatureRecipe and an entry " +
			"whose form carries no pin from a pin whose value is empty, and a pin " +
			"is recorded for exactly one authoring form, the entry supplied by an " +
			"imported file.\n\nIt is rooted at a boundary because that is where the " +
			"attachment is declared, on the reading the text fact beside it gives: " +
			"such an attachment is written once and reaches every component the " +
			"boundary holds, so a component-rooted key would hold one recorded pin " +
			"under as many keys as the boundary has members and none at all for a " +
			"boundary holding none.",
	},
	{
		Name:        "boundary-attachment-artifact-imported-path",
		Stage:       "hydrated",
		SourceField: "types.FeatureRecipeSpec.Artifacts",
		Subject:     []string{"boundary", "attachment", "artifact"},
		Params: []paramDecl{
			{Name: "boundary", Type: "string"},
			{Name: "attachment", Type: "string"},
			{Name: "artifact", Type: "string"},
		},
		Returns: "string",
		Doc: "BoundaryAttachmentArtifactImportedPath is the file one artifact of " +
			"the FeatureRecipe one boundary-scope attachment binds was supplied " +
			"from.\n\nEvery reading the two node-rooted forms carry applies here: " +
			"the value is the path the catalog resolved, which is absolute, and an " +
			"entry written inline or supplied by a library bundle answers empty.\n" +
			"\nIt is rooted at a boundary because that is where the attachment is " +
			"declared, on the reading the two facts beside it give: such an " +
			"attachment is written once and reaches every component the boundary " +
			"holds, so a component-rooted key would hold one imported path under as " +
			"many keys as the boundary has members and none at all for a boundary " +
			"holding none.",
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
			"produced them.\n\nIt is the owner-keyed form of the node's own " +
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
			"feeding one group stand together. The group is the scope because a " +
			"group is what a provider assembles, so it is the widest set inside " +
			"which one contributor's name can displace another's. It is not " +
			"always the narrowest: a group whose provider folds its per-node " +
			"output into one document holds contributors whose output the render " +
			"placed beside that document rather than inside it, and each entry " +
			"carries which of the two it was.\n\nThe order is stated rather than " +
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
		Subject:     []string{"assembly-path", "provider"},
		Params: []paramDecl{
			{Name: "assemblyPath", Type: "string"},
			{Name: "provider", Type: "string"},
		},
		Returns: "[]string",
		Doc: "EmittedFilePathSet are the output paths one assembly group wrote, " +
			"in path order.\n\nThe order is stated rather " +
			"than inherited. The order in which files are placed into a group " +
			"is not a property of the input, so answering in that order would " +
			"return a different sequence for identical input; path order is " +
			"derived from something ordered and is therefore the same on every " +
			"run.\n\nIt is keyed by the group rather than by a boundary the " +
			"group was fed by, and the difference is not presentational. " +
			"Boundaries whose path declarations evaluate alike under one " +
			"provider collapse into one group, and the group is what wrote the " +
			"paths — so a set keyed per boundary answers every boundary of a " +
			"collapsed group with the SAME paths, and a rule invoked over that " +
			"keying is invoked once per boundary about output there is one copy " +
			"of. A boundary carries no path of its own once the collapse has " +
			"happened, so there is nothing narrower for this fact to " +
			"answer.\n\nNo rule reads it as a predicate, which is a real state " +
			"rather than an oversight: a fact is declared from where its " +
			"subject is produced, so it stands whether or not a rule has been " +
			"placed on it. What a rule at this stage reads it FOR is the set of " +
			"paths to ask its own question about, one at a time, since the " +
			"subject it is invoked over names the group and not any single " +
			"path.\n\nThe collision rule over assembled " +
			"output is not its predicate reader, and the reason is the shape of " +
			"the answer rather than where that rule sits. A path set says which " +
			"paths were written and cannot say who wrote one, so a predicate " +
			"over it could report that something collided and never which " +
			"contributors met — and which two to separate is the whole of what " +
			"an author needs. That rule reads the producer set declared beside " +
			"this one, which carries the attribution this one does not.",
	},
	{
		Name:        "assembled-file-content",
		Stage:       "rendered",
		SourceField: "renderer.AssembledFile.Content",
		Subject:     []string{"assembly-path", "provider"},
		Params: []paramDecl{
			{Name: "assemblyPath", Type: "string"},
			{Name: "provider", Type: "string"},
			{Name: "path", Type: "string"},
		},
		Returns: "[]byte",
		Doc: "AssembledFileContent is the bytes standing at one output path " +
			"of one assembly group.\n\nIt is keyed by the " +
			"path as well as by the group, where the " +
			"path set over the same group is keyed by the group alone " +
			"and enumerates the path. The two answer different " +
			"questions: one is which paths were written, this is what " +
			"stands at one of them, and neither is derivable from the " +
			"other.\n\nA rule scanning assembled output reads one path's " +
			"bytes at a time rather than receiving the assembly, so no " +
			"rule can walk from one path to another's content.",
	},
	{
		Name:        "assembled-file-producer-set",
		Stage:       "rendered",
		SourceField: "renderer.AssemblyContribution.PathProducers",
		Subject:     []string{"assembly-path", "provider"},
		Params: []paramDecl{
			{Name: "assemblyPath", Type: "string"},
			{Name: "provider", Type: "string"},
			{Name: "path", Type: "string"},
		},
		Returns: "[]string",
		Doc: "AssembledFileProducerSet are the contributors that placed a file " +
			"at one output path of one assembly group, " +
			"deduplicated, in producer order.\n\nSeveral contributors meet in " +
			"one assembly — each component the group holds, each feature " +
			"attached to a boundary the group serves, and the assembly's own " +
			"merged output — and each chooses the names it writes under " +
			"independently of the others. So more than one entry here means two " +
			"of them landed on one output path, which is a state only the whole " +
			"set makes visible: from either contributor alone the path looks " +
			"unclaimed.\n\nOne entry is the ordinary answer, and the ordinary " +
			"answer is not the empty one: a path no contributor placed is a path " +
			"the group did not write, so the coordinate does not exist and no " +
			"rule is invoked for it.\n\nIt is keyed by the group, and that " +
			"keying is what makes one collision one answer. The contributors " +
			"that met are a property of the group's own output, so a set keyed " +
			"per boundary answers every boundary a collapsed group was fed by " +
			"with the SAME contributors — including boundaries none of those " +
			"contributors sat in, which a set keyed that way cannot tell " +
			"apart.\n\nIt is the contributors and not the " +
			"bytes, and that is the whole of what this fact carries. What " +
			"separates two contributors meeting on one path from one contributor " +
			"writing it once is which contributors arrived, so a rule reading " +
			"this needs no content — and a rule that could reach the bytes could " +
			"compare them, which would let two contributors agreeing by accident " +
			"pass as though the path had been claimed once.\n\nEach contributor " +
			"is named in authoring terms: the kind of thing it is together with " +
			"the name its author wrote. A rule reporting one therefore sends a " +
			"reader to something they can find in their own input rather than to " +
			"an identity the compilation assigned.\n\nThe order is stated rather " +
			"than inherited. The order in which contributors place their files " +
			"is not a property of the input, so answering in that order would " +
			"return a different sequence for identical input; producer order is " +
			"derived from something ordered and is therefore the same on every " +
			"run.\n\nThe path is the one the file is written to, complete with " +
			"the position an assembly nested it under. Two contributors emitting " +
			"one filename that an assembly keeps apart write two paths and meet " +
			"at neither, so a set keyed by the name a contributor emitted would " +
			"report a collision the output does not have. The names each render " +
			"emitted before anything was grouped are a separate fact at an " +
			"earlier stage, and neither is derivable from the other.",
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
