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
			"prune set and per-node resolved parameters",
		// Flattening loads every sub-aggregate the Aggregate names, which is a
		// further authored document reached through the same catalog.
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
			"resolve to, loaded and self-described: the declared capabilities, the " +
			"exports, and the input and output schemas each export declares, before " +
			"any step runs",
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
			"standing beside them — the resolved source, the parsed workload, and " +
			"each artifact entry's template text before it is evaluated — all of it " +
			"state that exists before any artifact bytes",
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
		Name:    "rendered",
		Extends: "hydrated",
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
			{Name: "AuthoredSocketNames", Type: "[]string", Doc: "AuthoredSocketNames are the socket names the attachment's own dependency block declares, in author order."},
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
		SourceField: "resolver.LintUnreadParameters(rawAggregate)",
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
		Name:        "per-edge-target",
		Stage:       "normalized",
		SourceField: "types.VerboseUsesEntry.Target",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "string",
		Doc: "PerEdgeTarget is the target an edge names, as authored — empty " +
			"when the author named none.\n\nThe emptiness is reported " +
			"rather than resolved. Two rules in the tree read the same empty " +
			"target and mean different things by it, one taking it as naming " +
			"the socket's own alias and the other as declining to create an " +
			"edge; a fact that silently applied either rule would make one of " +
			"them wrong somewhere else.",
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
			"attachment's own dependency block declares, in author order.\n" +
			"\nThese name sockets on the attached Feature, a separate " +
			"declaration surface from the component's own sockets, which is why " +
			"the fact is keyed per attachment as well as per node.",
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
			"block delivers through one mechanism, in author order.",
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
		Name:        "slot-sensitive",
		Stage:       "normalized",
		SourceField: "types.AttachmentSlot.Sensitive",
		Subject:     []string{"node", "slot"},
		Params: []paramDecl{
			{Name: "componentType", Type: "string"},
			{Name: "slot", Type: "string"},
		},
		Returns: "bool",
		Doc: "SlotSensitive reports whether an attachment slot declares that " +
			"whatever binds it handles secret material.\n\nIt is a marker and not " +
			"a constraint. No fill is accepted or rejected because of it and no " +
			"rendered output changes; what it does is make one declaration " +
			"readable — it is carried onto the slot's projected validation " +
			"surface as an extension keyword, and it is read when deriving " +
			"whether the attachment binding the slot handles sensitive " +
			"material.\n\nFalse is the answer for a slot that says nothing on the " +
			"point as well as for one that declares the marker off, and the two " +
			"are deliberately not distinguished: what a rule over this fact asks " +
			"is whether the slot claims secret material, and a slot silent on it " +
			"claims none.",
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
		Name:        "release-per-edge-target",
		Stage:       "normalized",
		SourceField: "types.ReleaseUsesEntry.Target",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "string",
		Doc: "ReleasePerEdgeTarget is the target a Release's per-edge entry names " +
			"for one socket on one node, as authored — empty when the Release " +
			"named none.\n\nIt is a separate fact from the target the Aggregate " +
			"authored, and the pair is what makes the population question " +
			"answerable at all. Both documents can bind one socket: the " +
			"component's own dependency block, and the Release's per-edge " +
			"surface, which may wire a socket the architecture declared and " +
			"nothing bound to a component that already exists. A rule reading " +
			"either surface alone reports a socket as unpopulated that the build " +
			"wires.\n\nThe emptiness is reported rather than resolved, and here " +
			"it carries the distinction the operator surface rests on: an entry " +
			"naming a target creates the edge, while an entry naming none " +
			"configures an edge some other document already drew and so binds " +
			"nothing.\n\nThe fact is placed where both documents are in view. It " +
			"is keyed by an edge, whose identity is a node the other document " +
			"declares, and a stage holding one authored document at a time " +
			"cannot answer for a pair.",
	},
	{
		Name:        "release-per-edge-infra",
		Stage:       "normalized",
		SourceField: "types.ReleaseUsesEntry.Infra",
		Subject:     []string{"edge"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "socket", Type: "string"},
		},
		Returns: "types.OrderedMap[any]",
		Doc: "ReleasePerEdgeInfra is the operator's configuration for one socket " +
			"on one node as it was written, carried as an opaque ordered " +
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
			"is keyed by is identified by a node the other document declares.",
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
			"entity that carries it, and it is why the fact is the authored " +
			"document rather than a compiled form of it: compiling can fail for " +
			"reasons that have nothing to do with the author's input, so a " +
			"compiled result would make an absent fact mean two different " +
			"things. A rule needing the compiled form compiles these bytes " +
			"itself and reports its own failure.\n\nIt is the document as " +
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
			"to prefer over it.",
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
			"whole contract without receiving the entity that carries it, and it " +
			"is why the fact is the authored document rather than a compiled form " +
			"of it: compiling can fail for reasons that have nothing to do with " +
			"the author's input, so a compiled result would make an absent fact " +
			"mean two different things. A rule needing the compiled form compiles " +
			"these bytes itself and reports its own failure.\n\nEmptiness is one " +
			"answer rather than two. A socket that declares no contract for this " +
			"pillar and a socket the catalog declares no entry for both leave an " +
			"edge's configuration for it unvalidated, and the question a rule " +
			"over this fact asks is whether there is a contract to evaluate at " +
			"all.\n\nIt covers the developer's pillar alone. The operator's " +
			"per-edge pillar is a separate authored field with its own fact, and " +
			"the two are separately omittable — a socket may declare a contract " +
			"for one and none for the other — so one fact spanning both could not " +
			"report which of them was written.",
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
			"opaque by type, and they are the authored document rather than a " +
			"compiled form of it: compiling can fail for reasons that have " +
			"nothing to do with the author's input, so a compiled result would " +
			"make an absent fact mean two different things. A rule needing the " +
			"compiled form compiles these bytes itself and reports its own " +
			"failure.\n\nEmptiness is one answer rather than two, exactly as it " +
			"is for the developer's pillar: a socket declaring no contract and a " +
			"socket the catalog declares no entry for both leave an edge's " +
			"operator configuration unvalidated, and what a rule asks is whether " +
			"there is a contract to evaluate at all.",
	},

	// ---------------------------------------------------------------- topology

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
		},
		Returns: "string",
		Doc: "PerEdgeResolvedTarget is the node an edge resolves to.\n\nIt " +
			"is a separate fact from the target as authored rather than the same " +
			"fact at a later moment. An authored target may be empty and mean " +
			"the socket's own alias, so the resolved form is the only one a rule " +
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
			"makes the report the same on every run.",
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
		Name:        "step-export-input-schema",
		Stage:       "described",
		SourceField: "wasm.PluginExport.Input",
		Subject:     []string{"node", "pipeline-step"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "step", Type: "string"},
		},
		Returns: "[]byte",
		Doc: "StepExportInputSchema is the schema document the export a pipeline " +
			"step dispatches to declares for the step-local input a pipeline " +
			"entry supplies, as opaque bytes — empty when the export declares " +
			"no input contract.\n\nThe bytes are opaque by type, so nothing " +
			"about the declared shape is legible from the fact beyond the " +
			"document itself. That is what lets a conformance rule receive a " +
			"whole schema without receiving the self-report carrying it.\n" +
			"\nIt is a separate fact from the declared output schema rather " +
			"than the same fact in two directions. The two are independently " +
			"absent and are evaluated against different values — one against " +
			"what a pipeline entry supplies, the other against what the step " +
			"returns — so a rule may legitimately ask about either without the " +
			"other.",
	},
	{
		Name:        "step-export-output-schema",
		Stage:       "described",
		SourceField: "wasm.PluginExport.Output",
		Subject:     []string{"node", "pipeline-step"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "step", Type: "string"},
		},
		Returns: "[]byte",
		Doc: "StepExportOutputSchema is the schema document the export a pipeline " +
			"step dispatches to declares for the outputs it returns, as opaque " +
			"bytes — empty when the export declares no output contract.\n" +
			"\nThe bytes are opaque by type, on the same reasoning as the " +
			"declared input schema: a conformance rule receives the whole " +
			"document without receiving the self-report carrying it.\n\nIt " +
			"is deliberately the second of a pair rather than one fact over " +
			"both directions of the contract. An export may declare either " +
			"without the other, and the two are evaluated at different moments " +
			"— one before the step runs, one after — so a single fact spanning " +
			"them would be absent for a step declaring only the half a rule " +
			"needed.",
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

	// ---------------------------------------------------------------- hydrated

	{
		Name:        "declared-locals-keys",
		Stage:       "hydrated",
		SourceField: "types.RecipeSpec.Locals",
		Subject:     []string{"node", "locals-key"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "DeclaredLocalsKeys are the local names a node's Recipe declares, " +
			"in author order.\n\nAuthor order is the fact, not an " +
			"incidental property of it: locals are evaluated in the order they " +
			"are declared, so a rule about what a local may reference is a rule " +
			"about this order.",
	},
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

	// ---------------------------------------------------------------- rendered

	{
		Name:        "pipeline-step-input-shape",
		Stage:       "rendered",
		SourceField: "types.RecipeSpec.Pipeline",
		Subject:     []string{"node", "pipeline-step"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "step", Type: "string"},
		},
		Returns: "types.OrderedMap[any]",
		Doc: "PipelineStepInputShape is the input a pipeline step was rendered " +
			"with, carried as an opaque ordered payload.",
	},
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
			"within one boundary, in emission order.\n\nSeveral distinct " +
			"collision rules read this one fact at different points of " +
			"assembly. They stay distinct rules over one fact rather than " +
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
