// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// The two requests here answer questions the daemon's positional surfaces
// cannot be asked. Every other entity question this daemon answers starts from
// a cursor in a document the client holds open: put the cursor on a reference
// and be told what it points at. That presumes the client has already found
// somewhere the name is written. These two start from the name and from the
// Release instead, so a client that holds neither a cursor nor a document can
// still ask what an entity is and what a deployment depends on.

// entityDefinitionParams is the request shape for `aggrega/entityDefinition`.
//
// Name is the entity's identity, optionally carrying a version suffix, exactly
// as it is written where one entity references another. It is required: the
// answer is the entity that name resolves to, so there is nothing to fall back
// on and nothing sensible to guess.
type entityDefinitionParams struct {
	Name string `json:"name"`
}

// entityDefinitionResult is the `aggrega/entityDefinition` response: the kind
// the name resolved to, that entity's own identity, and its resolved
// definition.
//
// Kind and Name are read off the resolved entity rather than off the request,
// so a name carrying a version suffix answers with the identity the entity
// declares. A client with several requests outstanding therefore attributes an
// answer from the answer itself.
//
// Definition is the entity as the daemon resolved it, not as any one file
// declares it: a Component or Feature arrives with its extends chain already
// merged, which is what makes this an answer about what the entity IS rather
// than about what one document says.
//
// It is a document, in the language entities are authored in, rather than a
// structure inside this response. Two reasons, and the first is a correctness
// one. A pillar block is carried through resolution as the parsed document node
// it was authored as, and re-encoding that node into this response's own
// encoding does not describe the pillar — it describes the parser, emitting the
// node's kind, style, anchor and source coordinates in place of the content.
// Handing back the document avoids that entirely, because the document is what
// the node is a parse of.
//
// The second is that the pillar has several authoring shapes — content written
// out, a reference to a shared document, or the marker that says the content
// comes from a base — and the document reproduces whichever one resolution
// arrived at, in the author's own key order, rather than flattening them into a
// shape none of them has.
type entityDefinitionResult struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Definition string `json:"definition"`
}

// dispatchEntityDefinition answers with the definition the named entity
// resolves to, across every kind the catalog holds.
//
// It is the by-name counterpart to the positional surfaces, and the difference
// is the coordinate rather than the answer: `aggrega/hover` and
// `aggrega/resolve` are told a file and a position and work out which entity is
// meant, while this one is told the entity outright. A client that already
// knows what it is looking for should not have to find a place where the name
// happens to be written in order to ask about it.
//
// # Three outcomes, and the two failures are told apart structurally
//
// A name that resolves is answered with the definition. A name that does not
// resolve is REFUSED rather than answered with an empty document, for the same
// reason the candidacy surfaces refuse rather than returning an empty array: an
// empty answer reports a typo as an entity with nothing to say about itself.
//
// The two refusals are distinguished by what the envelope carries rather than
// by the wording of its message. A name nothing declares carries no source
// document, because there is none. A name a workspace document DOES declare,
// which the catalog nonetheless did not resolve, carries that document in the
// envelope's file field — so a client branches on the field's presence and
// never on the prose. The second case is the one worth telling apart: the
// entity is written down and something refused it, which is a state of the
// workspace a person repairs, where a name nothing declares is usually a
// mistyped request. Its message carries the catalog-skip annotation for that
// reason, since a file the tolerant enumeration refused is the usual cause.
//
// It resolves through the session's catalog view and reaches no registry
// beyond what that view's own tiers reach, writes nothing, and publishes no
// diagnostics — so it disturbs no layer another request wrote into the client's
// Problems panel.
func (s *Server) dispatchEntityDefinition(ctx context.Context, req *jsonRPCRequest) error {
	var params entityDefinitionParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: name must be the entity's identity, as it is written where one entity "+
				"references another; the request named none")
	}

	result, errEnvelope := s.entityDefinition(ctx, name)
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// entityDefinition resolves name across every catalog kind and marshals the
// match, or produces the refusal for a name that resolved to nothing.
func (s *Server) entityDefinition(ctx context.Context, name string) (entityDefinitionResult, *jsonRPCError) {
	// No pins: this request names no Release, so there is no pin set for one.
	// The view is still request-scoped, so a resolution here cannot perturb a
	// concurrent request that does hold pins.
	view := s.lazyCatalog.ForRequest(nil)

	match, ok := resolveEntityByName(view, name)
	if !ok {
		return entityDefinitionResult{}, s.unresolvedEntityEnvelope(ctx, view, name)
	}
	definition, err := yaml.Marshal(match.entity)
	if err != nil {
		return entityDefinitionResult{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: fmt.Sprintf("render %s %q: %v", match.kind, name, err),
			cause:   err,
		}
	}
	return entityDefinitionResult{
		Kind:       match.kind,
		Name:       match.name,
		Version:    match.version,
		Definition: string(definition),
	}, nil
}

// entityMatch is one resolved entity plus the identity to report it under.
type entityMatch struct {
	kind    string
	name    string
	version string
	entity  any
}

// resolveEntityByName resolves name against the catalog view across every kind,
// returning the first match.
//
// The kind order is the one the command line resolves in, and it is shared
// rather than re-chosen here: two surfaces asked for the same ambiguous name
// must not answer with different entities. The order only decides an ambiguity
// — one name declared by two kinds — which is a workspace nobody should author,
// but which has to resolve to something rather than to whichever kind happened
// to be tried first on the day.
//
// Every accessor here is the view's own, so the tier cascade, the extends merge
// and the miss bookkeeping stay in the one place that implements them. Nothing
// is re-derived: a Component arrives merged because the view merges it.
func resolveEntityByName(view *catalog.LazyLookup, name string) (entityMatch, bool) {
	if c, ok := view.Component(name); ok {
		return entityMatch{"Component", c.Metadata.Name, c.Metadata.Version, c}, true
	}
	if p, ok := view.PluginByURI(name); ok {
		return entityMatch{"Plugin", p.Metadata.Name, p.Metadata.Version, p}, true
	}
	if f, ok := view.Feature(name); ok {
		return entityMatch{"Feature", f.Metadata.Name, f.Metadata.Version, f}, true
	}
	if r, ok := view.RecipeByURI(name); ok {
		return entityMatch{"Recipe", r.Metadata.Name, r.Metadata.Version, r}, true
	}
	if fr, ok := view.FeatureRecipeByURI(name); ok {
		return entityMatch{"FeatureRecipe", fr.Metadata.Name, fr.Metadata.Version, fr}, true
	}
	if a, ok := view.AggregateLookup()(name); ok && a.Aggregate != nil {
		return entityMatch{"Aggregate", a.Aggregate.Metadata.Name, a.Aggregate.Metadata.Version, a.Aggregate}, true
	}
	if lib, ok := view.SchemaLibrary(name); ok {
		return entityMatch{"SchemaLibrary", lib.Metadata.Name, lib.Metadata.Version, lib}, true
	}
	if lib, ok := view.MappingLibrary(name); ok {
		return entityMatch{"MappingLibrary", lib.Metadata.Name, lib.Metadata.Version, lib}, true
	}
	if lib, ok := view.WorkloadLibrary(name); ok {
		return entityMatch{"WorkloadLibrary", lib.Metadata.Name, lib.Metadata.Version, lib}, true
	}
	if lib, ok := view.ArtifactsLibrary(name); ok {
		return entityMatch{"ArtifactsLibrary", lib.Metadata.Name, lib.Metadata.Version, lib}, true
	}
	return entityMatch{}, false
}

// unresolvedEntityEnvelope builds the refusal for a name the catalog resolved
// nothing for, in the shape that tells the two failures apart.
//
// It asks the workspace whether any document declares the name before deciding
// which refusal to send, because those two states need different things from
// the reader. A document that declares it means the entity exists and something
// refused it — so the envelope names that document, and the message carries the
// skips that could explain the refusal. Nothing declaring it means the name
// reached the daemon and matched nothing anywhere, which is the request to
// correct.
//
// The workspace question is asked only on the failing path. The catalog view is
// the authority on what resolves, so a name that resolved needs no second
// opinion, and the traversal — which reads the parse cache and then walks the
// workspace — is not run for it.
func (s *Server) unresolvedEntityEnvelope(ctx context.Context, view *catalog.LazyLookup, name string) *jsonRPCError {
	// A reference may carry a version suffix; a document's metadata.name never
	// does. The comparison is against the identity, so the suffix is stripped
	// the same way every other reference-shaped lookup here strips it.
	identity, _, _ := catalog.SplitVersionedURI(name)
	if file, kind, ok := s.workspaceDeclarationOf(ctx, identity); ok {
		return &jsonRPCError{
			Code: errEntityNotResolvable,
			Message: s.skipContext(view, fmt.Sprintf(
				"%q is declared as a %s by %s, but the catalog resolved no entity for it",
				name, kind, file)),
			Data: newErrorData(file, 0, 0),
		}
	}
	return &jsonRPCError{
		Code: errEntityNotResolvable,
		Message: fmt.Sprintf(
			"%q matched no entity of any kind; no document in the workspace declares that name and no "+
				"catalog tier resolves it", name),
	}
}

// workspaceDeclarationOf reports the workspace document declaring identity as
// its metadata.name, and the kind that document declares, or ok false when none
// does.
//
// The path is workspace-relative, matching every other path this daemon reports
// to a client. A document outside the workspace root — the parse cache holds
// whatever file the editor last sent, which need not be a workspace file — is
// skipped, because reporting it under a workspace-relative path would misstate
// where it came from and reporting the host path would disclose one.
func (s *Server) workspaceDeclarationOf(ctx context.Context, identity string) (file, kind string, ok bool) {
	s.rangeWorkspaceDocs(ctx, func(doc workspaceDoc) bool {
		name, _ := metadataNameOf(doc.node)
		if name != identity {
			return true
		}
		rel, inside := relInsideRoot(s.workspaceRoot, doc.path)
		if !inside {
			return true
		}
		file, kind, ok = rel, scalarAtPathOnDoc(doc.node, []string{"kind"}), true
		return false
	})
	return file, kind, ok
}

// releaseClosureParams is the request shape for `aggrega/releaseClosure`.
//
// Release is a path relative to the workspace root naming a Release inside it —
// the same value `aggrega/irGraph` takes, refused in the same shapes, because
// the two answer about the same subject and a client should not have to spell
// it two ways.
type releaseClosureParams struct {
	Release string `json:"release"`
}

// ClosureEntity is one entry in a Release's dependency-closure listing: what
// was reached, and at which version.
//
// Version is the version the reference resolved to, which is not necessarily
// the version any document asked for — a reference that names no version
// resolves to one, and that resolved value is what a deployment is actually
// built from. It is empty for a reference that resolved to no version at all.
//
// Kind is present only where the reference site recorded one. A reference is
// collected from a position in a document, and some positions say what kind
// they point at while others do not, so the field is reported where it is known
// and omitted where it is not — rather than guessed from the shape of the name.
type ClosureEntity struct {
	URI     string `json:"uri"`
	Version string `json:"version"`
	Kind    string `json:"kind,omitempty"`
}

// releaseClosureResult is the `aggrega/releaseClosure` response: what the
// Release resolved to, and everything reached from it.
//
// Release repeats the value the request sent, so a client with several requests
// outstanding attributes an answer from the answer itself. Aggregate and
// Manifest are the identities the Release's two pointers resolved to, which is
// the fact a reader wants and not the paths they were written as.
//
// The three collections are always arrays on the wire, empty rather than null,
// so a client iterates them without a null check. They are separate because
// they are different populations answering to different questions — what the
// deployment is built from, which shared documents it draws on, and which
// guests shape its output — and a client that wants one of the three should not
// have to filter a merged list to find it.
type releaseClosureResult struct {
	Release     string          `json:"release"`
	Aggregate   string          `json:"aggregate"`
	Manifest    string          `json:"manifest"`
	Environment string          `json:"environment"`
	Entities    []ClosureEntity `json:"entities"`
	Libraries   []ClosureEntity `json:"libraries"`
	Plugins     []ClosureEntity `json:"plugins"`
}

// dispatchReleaseClosure answers with the dependency closure of the Release the
// request names: which entities it reaches, and at which versions.
//
// # This is not the graph, and a client holding the graph does not have it
//
// The two answers are derived from the same resolution and describe different
// things. The graph says what the deployment looks like — the nodes, their
// boundaries, what is wired to what. The closure says what was pulled in to
// build it, and at which resolved versions. A reader wanting to know why a
// version moved, or what a change to a shared document would reach, is asking
// this question; a reader wanting to see the deployment is asking the other.
// One is not recoverable from the other: an entity can be reached without
// appearing as a node, and a node does not carry the version its type resolved
// to.
//
// # It computes no second closure
//
// The daemon already resolves exactly one closure per Release-scoped
// inspection, inside the one preview computation the graph request runs, and
// the drift verdict is rendered over that same resolution rather than over a
// second one. This handler joins that computation instead of building one
// beside it — the same posture, the same inputs, the same resolved value — so
// there is nothing here that could come to disagree with what the graph
// request resolved for the same Release.
//
// The pipeline options are this request's own, which is what the shared
// computation leaves to its callers: a listing of what was reached has no use
// for the rendering stage, so it asks for none and no rendering guest is
// loaded.
//
// # What it deliberately does not do
//
// It publishes nothing. A drift verdict and the render advisories belong to the
// surface that asked for them — publishing is a whole-layer replacement, so a
// request that published here would erase the layer the graph request wrote.
// A caller wanting the drift verdict asks for the graph.
//
// It carries the same lockfile precondition the graph request carries, and for
// the same reason: the versions this listing reports are the ones the Release
// resolved to, and a workspace nobody has prepared has not resolved them yet.
// A listing produced without one would report what the catalog happens to hold
// today, which is a different question from what this deployment depends on.
func (s *Server) dispatchReleaseClosure(ctx context.Context, req *jsonRPCRequest) error {
	var params releaseClosureParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleReleaseClosure(ctx, params)
	if ctx.Err() != nil {
		// Cancelled mid-resolution (shutdown). Drop the response; the
		// dispatcher exits via the drain timer.
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// handleReleaseClosure runs the shared Release inspection and projects the
// closure it resolved onto the wire shape.
func (s *Server) handleReleaseClosure(ctx context.Context, params releaseClosureParams) (releaseClosureResult, *jsonRPCError) {
	preview, errEnvelope := s.previewRelease(ctx, params.Release, pipeline.Options{SkipRender: true})
	if errEnvelope != nil {
		return releaseClosureResult{}, errEnvelope
	}
	return releaseClosureResult{
		Release:     params.Release,
		Aggregate:   preview.resolved.Aggregate.Metadata.Name,
		Manifest:    preview.resolved.Manifest.Metadata.Name,
		Environment: preview.resolved.Release.Metadata.Environment,
		Entities:    closureEntities(preview.resolved.Closure),
		Libraries:   closureLibraries(preview.resolved.Closure),
		Plugins:     closurePlugins(preview.resolved.Closure),
	}, nil
}

// closureEntities projects the catalog-entity axis onto the wire shape, in the
// order the closure holds it.
//
// The order is the resolution's own and is not re-sorted here. It is already a
// function of the Release's content rather than of anything ambient — the same
// property that lets the same resolution be written to a lockfile and compared
// against one — so imposing a second order here would be a second statement of
// the same thing, free to disagree with the first.
func closureEntities(cl closure.Closure) []ClosureEntity {
	out := make([]ClosureEntity, 0, len(cl.Entities))
	for _, e := range cl.Entities {
		out = append(out, ClosureEntity{URI: e.URI, Version: e.ResolvedVersion, Kind: entityKindForHint(e.KindHint)})
	}
	return out
}

// closureLibraries projects the four shared-document axes onto one collection,
// each entry carrying the kind of library it is.
//
// They are joined rather than reported as four fields because a client asking
// what shared documents a Release draws on is asking one question; the kind is
// carried on the entry, so nothing is lost by joining them. The axes are
// appended in the order the resolution declares them, which is the order every
// other surface reports them in.
func closureLibraries(cl closure.Closure) []ClosureEntity {
	out := make([]ClosureEntity, 0,
		len(cl.MappingLibraries)+len(cl.SchemaLibraries)+len(cl.WorkloadLibraries)+len(cl.ArtifactsLibraries))
	appendAxis := func(kind string, libs []closure.ResolvedLibrary) {
		for _, l := range libs {
			out = append(out, ClosureEntity{URI: l.URI, Version: l.ResolvedVersion, Kind: kind})
		}
	}
	appendAxis("MappingLibrary", cl.MappingLibraries)
	appendAxis("SchemaLibrary", cl.SchemaLibraries)
	appendAxis("WorkloadLibrary", cl.WorkloadLibraries)
	appendAxis("ArtifactsLibrary", cl.ArtifactsLibraries)
	return out
}

// closurePlugins projects the guest axis onto the wire shape. Every entry is a
// Plugin, so the kind is stated rather than derived from a hint the axis does
// not carry.
func closurePlugins(cl closure.Closure) []ClosureEntity {
	out := make([]ClosureEntity, 0, len(cl.Plugins))
	for _, p := range cl.Plugins {
		out = append(out, ClosureEntity{URI: p.URI, Version: p.ResolvedVersion, Kind: "Plugin"})
	}
	return out
}

// entityKindForHint renders the kind a closure entry was collected under as the
// kind an author writes, or the empty string when the collection site recorded
// none.
//
// The translation is deliberate rather than incidental. The recorded hint is a
// note the walk keeps for itself about where a reference came from; the kind a
// reader recognises is the one they type at the head of a document. A client
// displaying the hint verbatim would be showing a word that appears nowhere in
// anything the author wrote.
func entityKindForHint(hint string) string {
	switch hint {
	case "recipe":
		return "Recipe"
	case "feature_recipe":
		return "FeatureRecipe"
	case "aggregate":
		return "Aggregate"
	}
	return ""
}
