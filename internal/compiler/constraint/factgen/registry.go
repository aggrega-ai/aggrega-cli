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
		Holds: "per-node resolved locals and provides — state that exists before " +
			"any artifact bytes",
		// Locals and provides are evaluated as templates in the host, over
		// values the graph already carries, so the resolved state is computed
		// rather than reached for.
		HostPowers: noHostPowers,
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
