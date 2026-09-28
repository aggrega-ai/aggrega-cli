// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The two requests here answer questions the daemon's positional surfaces
// cannot be asked. Every other entity question this daemon answers starts from
// a cursor in a document the client holds open: put the cursor on a reference
// and be told what it points at. That presumes the client has already found
// somewhere the name is written. These two start from the name and from the
// Release instead, so a client that holds neither a cursor nor a document can
// still ask what an entity is and what a Release depends on.

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
//
// Description is what the resolved entity says about itself, omitted where it
// declares none. It is carried as its own field rather than left inside the
// definition document, because a client showing a one-line summary beside a name
// would otherwise have to parse the document to find it.
//
// PublishScope is the segment the entity's published location is scoped by — a
// Recipe's or FeatureRecipe's capability, a Plugin's plugin type — and is omitted
// for every kind that carries none. It is the resolution's own reading of the
// typed entity, so a client reporting where an entity publishes to does not
// re-derive it from the name a person wrote.
//
// It is not called `scope` on the wire, and the longer name is deliberate: an
// author already writes `scope` on a Plugin's well-known annotations, where it
// says which render paths an annotation reaches. One word carrying two unrelated
// meanings across entity-facing surfaces is the confusion a client author cannot
// resolve from the payload alone, so this one names what it scopes.
//
// File is the document the entity was declared in, named the way every other
// answer on this protocol names one, so a client reads one field for one question
// wherever it asked. It is reported for every kind, and its spelling follows the
// one rule every reported path here follows: relative to the workspace root for a
// document inside it, and the absolute path for one outside. Both are paths the
// client can open, and the second is the same spelling the command line prints
// for that file. See [pathscope.ReportedName], which is where that rule lives.
//
// It is empty in one case, and that case is a statement about the entity rather
// than a gap in the answer: an entity resolved from a registry lives in a
// cache slot, which is not a document anybody authored, so there is none to
// name.
//
// AuthoredLength is the length in bytes of the authored text the resolution
// retained, and only the Aggregate is served with one — it is the kind whose text
// is read back as a document, by the surface that renders one. It is not the
// length of the file File names, which is why the two are reported independently:
// a client is told an authored text was retained where one was, and File answers
// where the entity was declared either way.
//
// The authored bytes themselves are deliberately not carried. Definition is the
// answer to what the entity IS, and a second document beside it — the one file
// that happened to declare it — would answer a question this request was not
// asked, at the cost of the payload.
//
// Published is what a registry reference read, and it is present on exactly the
// answers read from one. Its presence is therefore what tells a client the
// definition came from a coordinate rather than from a catalog resolution, and a
// name resolved by the catalog never carries it.
type entityDefinitionResult struct {
	Kind           string             `json:"kind"`
	Name           string             `json:"name"`
	Version        string             `json:"version"`
	Description    string             `json:"description,omitempty"`
	PublishScope   string             `json:"publishScope,omitempty"`
	File           string             `json:"file"`
	AuthoredLength int                `json:"authoredLength"`
	Definition     string             `json:"definition"`
	Published      *publishedArtifact `json:"published,omitempty"`
}

// publishedArtifact is what an `aggrega/entityDefinition` request naming a
// registry reference read: the coordinate, how it held the entity, and the
// manifest it was read through.
//
// Every field is set on every answer that carries it. Archive false is a
// statement rather than an absence — the coordinate held the entity document
// itself — which is why these facts sit in an object of their own rather than
// beside the by-name fields, where an omitted false would read the same as a
// name the catalog resolved.
//
// MediaType is the artifact type the manifest declares, and ManifestDigest is
// that manifest's digest: the value to name, after `@`, to read the same bytes
// again exactly.
type publishedArtifact struct {
	Reference      string `json:"reference"`
	Archive        bool   `json:"archive"`
	MediaType      string `json:"mediaType"`
	ManifestDigest string `json:"manifestDigest"`
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
// A name resolves through the session's catalog view and reaches no registry
// beyond what that view's own tiers reach. A registry reference sent as the
// name is read from the registry that publishes it instead — see
// publishedEntityDefinition. Either way it writes nothing and publishes no
// diagnostics, so it disturbs no layer another request wrote into the client's
// Problems panel.
func (s *Server) dispatchEntityDefinition(ctx context.Context, req *jsonRPCRequest) error {
	var params entityDefinitionParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: name must be the entity's identity, as it is written where one entity "+
				"references another; the request named none")
	}
	// A reference that is not one the reading accepts is a malformed request,
	// corrected by whoever composed it, rather than a state of the registry — so
	// it is refused in the band a client corrects rather than the one a user acts
	// on, before anything is fetched.
	if compilerops.IsRegistryReference(name) {
		if err := compilerops.CheckRegistryReference(name); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
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
	if compilerops.IsRegistryReference(name) {
		return s.publishedEntityDefinition(ctx, name)
	}
	// No pins: this request names no Release, so there is no pin set for one.
	// The view is still request-scoped, so a resolution here cannot perturb a
	// concurrent request that does hold pins.
	view := s.lazyCatalog.ForRequest(nil)

	// The resolution is the shared one every by-name surface runs, so a name
	// resolves to the same entity here as it does at a command line or a tool
	// call. Each accessor behind it is still this view's own, which is what keeps
	// the tier cascade, the extends merge and the miss bookkeeping in the one
	// place that implements them: a Component arrives merged because the view
	// merges it.
	match, ok := compilerops.ResolveEntity(view, name)
	if !ok {
		return entityDefinitionResult{}, s.unresolvedEntityEnvelope(ctx, view, name)
	}
	definition, err := yaml.Marshal(match.Definition)
	if err != nil {
		return entityDefinitionResult{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: fmt.Sprintf("render %s %q: %v", match.Kind, name, err),
			cause:   err,
		}
	}
	file := pathscope.ReportedName(s.workspaceRoot, match.Source)
	return entityDefinitionResult{
		Kind:           match.Kind,
		Name:           match.Name,
		Version:        match.Version,
		Description:    match.Description,
		PublishScope:   match.Scope,
		File:           file,
		AuthoredLength: len(match.Bytes),
		Definition:     string(definition),
	}, nil
}

// publishedEntityDefinition answers a name that is a registry reference with the
// entity published at that coordinate, read through the reading every surface
// showing a published entity shares.
//
// The request named the reference, so reaching the registry is what it asked
// for — the same footing every other request here that goes out to a registry
// stands on. It reaches it with this workspace's egress posture and the
// credentials of the user this daemon runs as, writes nothing, and publishes no
// diagnostics.
//
// The answer is the by-name one's shape, filled from what was published rather
// than from what a catalog resolved:
//
//   - Definition is the entity document exactly as it was published — unpacked
//     from an archive where its kind publishes as one, with no inheritance
//     applied. It is what the publisher put at the coordinate, which is the
//     question a reference asks; a catalog resolution is the question a name
//     asks.
//   - AuthoredLength is that document's length, because the definition here IS
//     an authored document rather than a rendering of a resolved one — which is
//     the distinction the field exists to let a client draw.
//   - File is empty: an entity read from a registry has no document on this
//     machine to name.
//   - PublishScope is omitted. The request already holds the coordinate, which
//     is where the entity is published, so a second statement of that read out
//     of its content would say the same thing another way.
//   - Published carries what was read: the reference, whether its bytes were
//     an archive the entity was unpacked from, the media type the manifest
//     declares, and that manifest's digest.
//
// A reference nothing is published under, a credential the registry refuses, an
// artifact that cannot be unpacked or holds no entity, and an offline workspace
// are each refused with entity-not-resolvable. The message leads with the short
// code of the condition that names the failure, where the failure carries one,
// and ends with what to do about it.
func (s *Server) publishedEntityDefinition(ctx context.Context, reference string) (entityDefinitionResult, *jsonRPCError) {
	entity, err := compilerops.ReadPublishedEntity(ctx, s.wsConfig, reference)
	if err != nil {
		message := "entityDefinition: " + err.Error()
		if short, ok := cerrors.ShortCodeOf(err); ok {
			message = "entityDefinition: " + short + ": " + err.Error()
		}
		return entityDefinitionResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: message,
			cause:   err,
		}
	}
	return entityDefinitionResult{
		Kind:           entity.Kind,
		Name:           entity.Name,
		Version:        entity.Version,
		Description:    entity.Description,
		AuthoredLength: len(entity.Document),
		Definition:     string(entity.Document),
		Published: &publishedArtifact{
			Reference:      entity.Reference,
			Archive:        entity.Archive,
			MediaType:      entity.MediaType,
			ManifestDigest: entity.Digest,
		},
	}, nil
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
// Where exactly one document the catalog refused declares the name, that
// refusal leads the message, stated once: the document by the path this daemon
// reports it at, then the fault it carries — the constraint and the line — in the
// words `aggrega inspect <name>` states the same refusal in, since both render it
// through compilerops.RefusedDocument. The consequence follows it, and every
// other refusal is listed after that. The file singled out is the one a build
// singles out for the same reference, because both ask compilerops.SoleRefusal;
// where it cannot single one out, the declaration leads and every refusal is
// listed, exactly as with no refusal at all.
//
// The workspace question is asked only on the failing path. The catalog view is
// the authority on what resolves, so a name that resolved needs no second
// opinion, and the traversal — which reads the parse cache and then walks the
// workspace — is not run for it.
func (s *Server) unresolvedEntityEnvelope(ctx context.Context, view *catalog.LazyLookup, name string) *jsonRPCError {
	// A reference may carry a version suffix; a document's metadata.name never
	// does. The comparison is against the identity, so the suffix is stripped
	// the same way every other reference-shaped lookup here strips it.
	identity, _, _ := resolver.SplitVersionedURI(name)
	if file, kind, ok := s.workspaceDeclarationOf(ctx, identity); ok {
		skipped := view.SkippedEntities()
		hint := compilerops.KindHintFor(kind)
		var message string
		if refusal, found := compilerops.SoleRefusal(skipped, name, hint); found {
			message = s.skipContextBesides(view, fmt.Sprintf("%s; note: %s, and %q resolved to no entity",
				compilerops.RefusedDocumentLine(pathscope.ReportedName(s.workspaceRoot, refusal.Path), refusal.Err),
				compilerops.RefusedDocumentNote, name), refusal.Path)
		} else {
			message = s.skipContext(view, fmt.Sprintf(
				"%q is declared as %s %s by %s, but the catalog resolved no entity for it",
				name, indefiniteArticle(kind), kind, file))
		}
		return &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: s.localCatalogContext(message, skipped, name, hint),
			Data:    newErrorData(file, 0, 0),
		}
	}
	return &jsonRPCError{
		Code: errEntityNotResolvable,
		Message: fmt.Sprintf(
			"%q matched no entity of any kind; no document in the workspace declares that name and no "+
				"catalog tier resolves it", name),
	}
}

// localCatalogContext appends the workspace's own account of where it reads
// local entities from, and what to change so the one that was named is read,
// where that account is the remedy for the name that resolved to nothing.
//
// It answers the half of "the catalog resolved nothing" that naming the
// document cannot: a reader looking at a file the catalog does not read still
// has no statement of why it was not read, and the directories a catalog search
// covers are the fact a reader driving this daemon over a connection has least
// access to.
//
// Whether it is the remedy is decided by compilerops.LocalCatalogAdvice, the one
// predicate every surface carrying the account reads, over the refusals this
// request's view recorded. A document the catalog read and refused declaring the
// name was found where the catalog reads, so moving it is the wrong remedy, and
// nothing is appended.
//
// The sentences are the workspace's rather than this surface's. The one place
// that composes them is asked for them, so a reader here is told what a reader
// at a terminal is told, and the empty answer a workspace gives when its reader
// declared it has no local catalog arrives as an empty string and appends
// nothing — a diagnostic that argued with that declaration would be worse than
// one that said nothing. Asking is safe for a request whose subject is not a
// workspace: the composition answers empty for an absent one.
//
// Only the framing is this surface's. The composed account opens with a line
// break and an indent, which is the shape an error printed to a terminal wants;
// an envelope message is one line, so it is folded on the way the skips already
// are.
func (s *Server) localCatalogContext(msg string, skipped []catalog.SkippedEntity, name, kindHint string) string {
	note := strings.TrimSpace(compilerops.LocalCatalogAdvice(
		s.resolvedWorkspace.LocalCatalogAdviceNote(""), skipped, name, kindHint))
	if note == "" {
		return msg
	}
	return msg + "; " + note
}

// indefiniteArticle is the article an entity kind takes, so a refusal naming a
// kind reads as a sentence rather than as a slot with a value dropped into it.
//
// Two unexported helpers elsewhere in this module answer the same question for
// their own callers, and neither is reachable from here: one belongs to the
// constraint-fact generator, the other to the assistant-facing protocol, where
// it answers for the decoder's type names rather than for entity kinds. So one
// rule has three implementations, and that is recorded here rather than left to
// be rediscovered — the duplication is a reachability fact, not an accident.
func indefiniteArticle(kind string) string {
	if kind == "" {
		return "a"
	}
	switch kind[0] {
	case 'A', 'E', 'I', 'O', 'U', 'a', 'e', 'i', 'o', 'u':
		return "an"
	}
	return "a"
}

// workspaceDeclarationOf reports the workspace document declaring identity as
// its metadata.name, and the kind that document declares, or ok false when none
// does.
//
// The path is workspace-relative, matching every other path this daemon reports
// for a document inside the root. A document outside it — the parse cache holds
// whatever file the editor last sent, which need not be a workspace file — is
// skipped, because the question this asks is whether the WORKSPACE declares the
// name, and a buffer the editor happened to send is not the workspace declaring
// anything. The skip is therefore about membership rather than about the path,
// and the other refusal is the honest answer for it.
func (s *Server) workspaceDeclarationOf(ctx context.Context, identity string) (file, kind string, ok bool) {
	s.rangeWorkspaceDocs(ctx, func(doc workspaceDoc) bool {
		name, _ := metadataNameOf(doc.node)
		if name != identity {
			return true
		}
		rel, inside := pathscope.Relative(s.workspaceRoot, doc.path)
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
// resolves to one, and that resolved value is what a Release is actually
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
// Every collection is always an array on the wire, empty rather than null, so a
// client iterates them without a null check. The three listings are separate
// because they are different populations answering to different questions — what
// the Release is built from, which shared documents it draws on, and which
// guests shape its output — and a client that wants one of the three should not
// have to filter a merged list to find it.
//
// MappingDiagnostics and Deprecations are what is wrong with the closure just
// listed, and they are reported rather than turned into a refusal: this request
// reports on a Release rather than producing anything from it, so a client asking
// what a Release depends on is answered AND told what is wrong with it. A
// listing that carried neither would report a closure as sound that another reader
// of the same facts reports as broken.
//
// They are kept apart because they are decided at different moments and by
// different things — one by this resolution, one by the record of pinned versions
// it was seeded with — so a client acting on either knows which it is holding.
type releaseClosureResult struct {
	Release     string          `json:"release"`
	Aggregate   string          `json:"aggregate"`
	Manifest    string          `json:"manifest"`
	Environment string          `json:"environment"`
	Entities    []ClosureEntity `json:"entities"`
	Libraries   []ClosureEntity `json:"libraries"`
	Plugins     []ClosureEntity `json:"plugins"`
	// MappingDiagnostics names each Manifest mapping whose target does not
	// implement the mapped type, as the resolution reported it.
	MappingDiagnostics []string `json:"mappingDiagnostics"`
	// Deprecations names each pinned reference a withdrawal marker was recorded
	// against, from the shared report — so the set of references named here is the
	// set every reader of that report sees, and only the framing differs.
	Deprecations []string `json:"deprecations"`
}

// dispatchReleaseClosure answers with the dependency closure of the Release the
// request names: which entities it reaches, and at which versions.
//
// # This is not the graph, and a client holding the graph does not have it
//
// The two answers are derived from the same resolution and describe different
// things. The graph says what the composition looks like — the nodes, their
// boundaries, what is wired to what. The closure says what was pulled in to
// build it, and at which resolved versions. A reader wanting to know why a
// version moved, or what a change to a shared document would reach, is asking
// this question; a reader wanting to see the composition is asking the other.
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
// It publishes nothing of what it finds. A drift verdict and the render
// advisories belong to the surface that asked for them — publishing is a
// whole-layer replacement, so a request that published here would erase the
// layer the graph request wrote. A caller wanting the drift verdict asks for the
// graph.
//
// It carries the same lockfile precondition the graph request carries, and for
// the same reason: the versions this listing reports are the ones the Release
// resolved to, and a workspace nobody has prepared has not resolved them yet.
// A listing produced without one would report what the catalog happens to hold
// today, which is a different question from what this Release depends on.
// Crossing that precondition is also why a listing that finds the lockfile gone
// withdraws the drift finding standing on it: the gate does that for whichever
// request crossed it, and requireLockfile says why.
func (s *Server) dispatchReleaseClosure(ctx context.Context, req *jsonRPCRequest) error {
	var params releaseClosureParams
	if err := decodeParams(req, &params); err != nil {
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
// closure it resolved, plus the findings decided about it, onto the wire shape.
//
// The findings are read from the same computation the listing is read from — the
// resolution's own mapping verdict, and the shared withdrawal report over the
// record that computation was seeded with — so nothing here re-derives a fact that
// already has a producer.
func (s *Server) handleReleaseClosure(ctx context.Context, params releaseClosureParams) (releaseClosureResult, *jsonRPCError) {
	preview, errEnvelope := s.previewRelease(ctx, params.Release, pipeline.Options{SkipRender: true})
	if errEnvelope != nil {
		return releaseClosureResult{}, errEnvelope
	}
	return releaseClosureResult{
		Release:            params.Release,
		Aggregate:          preview.resolved.Aggregate.Metadata.Name,
		Manifest:           preview.resolved.Manifest.Metadata.Name,
		Environment:        preview.resolved.Release.Metadata.Environment,
		Entities:           closureEntities(preview.resolved.Closure),
		Libraries:          closureLibraries(preview.resolved.Closure),
		Plugins:            closurePlugins(preview.resolved.Closure),
		MappingDiagnostics: closureMappingDiagnostics(preview.resolved.ManifestConsistencyFindings),
		Deprecations:       closureDeprecations(preview.lockfile),
	}, nil
}

// closureMappingDiagnostics renders the resolution's mapping verdict onto the wire
// shape, in the order the resolution reported it.
//
// The result is an empty slice rather than nil for a sound Manifest, matching every
// other collection in this response: a client iterates it without a null check, and
// an absent finding set and an empty one are the same statement here.
func closureMappingDiagnostics(findings []error) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Error())
	}
	return out
}

// closureDeprecations renders the withdrawal markers recorded against the pinned
// references onto the wire shape.
//
// It reads the shared report rather than the record's rows, so a reference reported
// here is reported by every reader of that report and the wording of the notice has
// one home.
//
// The report is nil-safe by contract, so no guard is needed here for a nil record.
// What that means differs by why the record is nil, and only one of the two cases is
// free: a workspace with no record has no recorded marker either, while a record
// present but unreadable has markers this answer cannot report. The second is the
// same degradation the surrounding computation already takes on an unreadable record
// — it proceeds with no pins — rather than one introduced here, and the answer says
// nothing about it either way.
func closureDeprecations(lf *lockfile.Lockfile) []string {
	findings := lf.DeprecationFindings()
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Notice())
	}
	return out
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
		out = append(out, ClosureEntity{URI: e.URI, Version: e.ResolvedVersion, Kind: compilerops.EntityKindForHint(e.KindHint)})
	}
	return out
}

// closureLibraries projects the shared-document axes onto one collection, each
// entry carrying the kind of library it is.
//
// They are joined rather than reported as one field per axis because a client
// asking what shared documents a Release draws on is asking one question; the kind
// is carried on the entry, so nothing is lost by joining them.
//
// It ranges over the closure's own declared axis set rather than naming the axes
// here. That set is the single enumeration of them, so an axis added to it is
// carried into this answer by construction — whereas a projection that named the
// axes itself would go on reporting the old set, with nothing failing to say an
// axis had stopped being reported. The kind comes off the declaration for the same
// reason: spelled here, it would be a second spelling free to disagree with it.
//
// The axes are appended in the declared order, which is what makes that order one
// statement rather than one per projection.
func closureLibraries(cl closure.Closure) []ClosureEntity {
	axes := closure.LibraryAxes()
	total := 0
	for _, axis := range axes {
		total += len(axis.Libraries(&cl))
	}
	out := make([]ClosureEntity, 0, total)
	for _, axis := range axes {
		for _, l := range axis.Libraries(&cl) {
			out = append(out, ClosureEntity{URI: l.URI, Version: l.ResolvedVersion, Kind: axis.Kind})
		}
	}
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
