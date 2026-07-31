// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// This file is the fact registry: the single place a constraint fact is
// declared. Everything the framework knows about placement is derived from it —
// the view ladder, the subject-kind set, the placement table — so adding,
// moving or removing a fact is a one-line edit here whose consequences appear
// as a diff in generated output rather than as a judgement buried inside a
// check.
//
// The registry grows. It stays one file while it does, because a second file
// would be a second place to look for the answer to "is this fact already
// declared", which is the question the registry exists to make cheap.

// rungs declares the view ladder, root first. The order in this slice is not
// the ladder — the Extends relation is — and the generator refuses a relation
// that is not a single chain.
var rungs = []rungDecl{
	{
		Name:    "document",
		Extends: "",
		Holds: "one authored document as the loader hands it over: parsed and " +
			"envelope-checked, neither normalized nor parameter-interpolated",
	},
	{
		Name:    "normalized",
		Extends: "document",
		Holds: "both authoring documents normalized and parameter-interpolated, " +
			"with the catalog reachable",
	},
	{
		Name:    "topology",
		Extends: "normalized",
		Holds: "the flattened node set, including sub-aggregate nodes, with the " +
			"prune set and per-node resolved parameters",
	},
	{
		Name:    "graph",
		Extends: "topology",
		Holds: "the finalised graph after the whole attachment cascade and " +
			"annotation-driven expansion",
	},
	{
		Name:    "hydrated",
		Extends: "graph",
		Holds: "per-node resolved locals and provides — state that exists before " +
			"any artifact bytes",
	},
	{
		Name:    "rendered",
		Extends: "hydrated",
		Holds:   "artifact bytes and the emitted file set",
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
// The set declared here realizes the full derived ladder and the full derived
// subject-kind set: every rung is occupied and every subject kind is keyed, so
// both derived sets are complete and a consumer building one traversal per kind
// has every kind in front of it. Further facts sharing an already-realized rung
// and kind are added by the change that needs them; each is a declaration, not
// a design act, so the registry grows one line at a time.
var facts = []factDecl{
	// ---------------------------------------------------------------- document

	{
		Name:        "raw-aggregate-text",
		Rung:        "document",
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
			"would make that question unanswerable and would put two rungs out " +
			"of order with each other.",
	},
	{
		Name:        "declared-parameter-keys",
		Rung:        "document",
		SourceField: "types.AggregateSpec.Parameters",
		Subject:     []string{"document", "parameter"},
		Params:      []paramDecl{{Name: "document", Type: "string"}},
		Returns:     "[]string",
		Doc: "DeclaredParameterKeys are the parameter names the Aggregate's " +
			"contract declares, in author order.",
	},
	{
		Name:        "entity-assertion-name-set",
		Rung:        "document",
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
		Rung:        "normalized",
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
		Rung:        "normalized",
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
		Rung:        "normalized",
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
		Rung:        "normalized",
		SourceField: "types.ComponentInstance.Features",
		Subject:     []string{"node", "attachment"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "AuthoredAttachmentNames are the attachment names an instance " +
			"declares, in author order.",
	},
	{
		Name:        "attachment-authored-uses-socket-names",
		Rung:        "normalized",
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
		Rung:        "normalized",
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
		Rung:        "normalized",
		SourceField: "types.ComponentInstance.Inject",
		Subject:     []string{"node", "mechanism"},
		Params:      []paramDecl{{Name: "node", Type: "string"}},
		Returns:     "[]string",
		Doc: "AuthoredInjectMechanismNames are the delivery mechanisms an " +
			"instance's injection block declares, in author order.",
	},
	{
		Name:        "authored-inject-delivery-keys",
		Rung:        "normalized",
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
		Rung:        "normalized",
		SourceField: "types.ComponentInstance.Values",
		Subject:     []string{"node", "pillar", "leaf"},
		Params: []paramDecl{
			{Name: "node", Type: "string"},
			{Name: "pillar", Type: "string"},
		},
		Returns: "types.OrderedMap[any]",
		Doc: "AuthoredValuesPillar is one configuration pillar as the author " +
			"wrote it, carried as an opaque ordered payload.\n\nThe " +
			"payload is opaque by type: its entries are whatever the author " +
			"wrote, so nothing about the authoring surface is readable from it " +
			"beyond the keys and values themselves. That is what lets a " +
			"conformance rule receive a whole pillar without receiving a " +
			"document.",
	},
	{
		Name:        "authored-attachment-block",
		Rung:        "normalized",
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

	// ---------------------------------------------------------------- topology

	{
		Name:        "resolved-parameter",
		Rung:        "topology",
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
		Rung:        "graph",
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

	// ---------------------------------------------------------------- hydrated

	{
		Name:        "declared-locals-keys",
		Rung:        "hydrated",
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
		Rung:        "hydrated",
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

	// ---------------------------------------------------------------- rendered

	{
		Name:        "pipeline-step-input-shape",
		Rung:        "rendered",
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
		Rung:        "rendered",
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
		Name:        "artifact-projection-content",
		Rung:        "rendered",
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
