// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// describeArgs is the describe tool's argument shape. Exactly one of name or
// release selects the subject.
type describeArgs struct {
	Name          string   `json:"name"`
	Release       string   `json:"release"`
	WorkspaceRoot string   `json:"workspace_root"`
	CatalogDirs   []string `json:"catalog_dirs"`

	// ResponseFormat is a pointer so that a value a caller sent is
	// distinguishable from one it left out. Every value outside the declared set
	// is refused, the blank string among them, and only an absent argument takes
	// the default — so a plain string would make a refused value and an absent one
	// the same bytes at exactly the point the two have to be told apart.
	ResponseFormat *string `json:"response_format"`
}

// describeEntityPayload is the structured payload for a catalog entity subject:
// its kind, its identity, what it says about itself, where it publishes to, the
// authored document behind it, and its resolved definition.
//
// Kind is whichever kind the name resolved to, read off the resolved entity
// rather than assumed from the request — a caller that names a Recipe is
// answered about a Recipe.
//
// Description is what the entity says about itself, omitted where it declares
// none. It is on the payload rather than only in the summary line, because a
// caller reading the structured answer should not have to parse prose to learn
// what a building block is for.
//
// PublishScope is the segment the entity's published location is scoped by — a
// Recipe's or FeatureRecipe's capability, a Plugin's plugin type — and is omitted
// for every kind that carries none.
//
// It is not called `scope` in the payload, and the longer name is deliberate: an
// author already writes `scope` on a Plugin's well-known annotations, where it
// says which render paths an annotation reaches. One word carrying two unrelated
// meanings across entity-facing surfaces is the confusion a caller cannot resolve
// from the payload alone, so this one names what it scopes.
//
// File is the document the entity was declared in, named the way the entity
// listing names one, so a caller reads one field for one question whichever tool
// it asked. It is reported for every kind, and its spelling follows the one rule
// every reported path follows: relative to the workspace the call named for a
// document inside it, and the absolute path for one outside. Both are paths the
// caller can open, and the second is the same spelling the command line prints for
// that file. See [pathscope.ReportedName], which is where that rule lives — so one
// resolution reports one document the same way whichever surface is asked.
//
// A caller reaching a listener is the exception, because it can open neither
// spelling of a document outside the workspace this server serves: such a file
// is absent for it and marked by FileWithheld, which is omitted where nothing was
// kept back. A document inside the workspace reaches it by the same relative name.
//
// It is empty in one case, and that case is a statement about the entity rather
// than a gap in the answer: an entity resolved from a registry lives in a
// cache slot, which is not a document anybody authored, so there is none to
// name.
//
// AuthoredLength is the length in bytes of the authored text the resolution
// retained, and only the Aggregate is served with one — it is the kind whose text
// is read back as a document, by the surface that renders one. It is not the length
// of the file File names, which is why the two are reported independently: a caller
// is told an authored text was retained where one was, and File answers where the
// entity was declared either way.
//
// The authored bytes themselves are deliberately not carried. Definition already
// answers what the entity IS, with inheritance applied, and a second copy of the
// one file that declared it would spend the caller's payload on a document the
// path lets it read for itself.
type describeEntityPayload struct {
	Kind           string          `json:"kind"`
	Name           string          `json:"name"`
	Version        string          `json:"version"`
	Description    string          `json:"description,omitempty"`
	PublishScope   string          `json:"publish_scope,omitempty"`
	File           *string         `json:"file"`
	FileWithheld   bool            `json:"file_withheld,omitempty"`
	AuthoredLength int             `json:"authored_length"`
	Definition     json.RawMessage `json:"definition"`
}

// describeReleasePayload is the structured payload for a Release subject: the
// resolved Release/Manifest/Aggregate identities, everything its catalog closure
// reaches, and what is wrong with it.
//
// The two listings are separate because they are different populations answering
// different questions — what the Release is built from, and which shared
// documents it draws on — and a caller that wants one of them should not have to
// filter a merged list to find it.
//
// Each listing carries a count as well as its members, because the concise format
// answers with counts and no members: the count is the only thing that reports
// what a listing reached there, so a listing added here owes one too.
//
// Release is the Release's location on this host, spelled as the caller may be
// told it; one the caller is not told is absent and marked by ReleaseWithheld,
// which is omitted where nothing was kept back.
type describeReleasePayload struct {
	Release         *string      `json:"release"`
	ReleaseWithheld bool         `json:"release_withheld,omitempty"`
	Aggregate       string       `json:"aggregate"`
	Manifest        string       `json:"manifest"`
	Environment     string       `json:"environment"`
	EntityCount     int          `json:"entity_count"`
	LibraryCount    int          `json:"library_count"`
	Entities        []closureRef `json:"entities"`
	// Libraries names the shared documents the closure reaches, each entry carrying
	// the kind of library it is. They are joined across the axes rather than
	// reported one field per axis: the kind is on the entry, so nothing is lost by
	// joining them, and a caller meets one entry shape rather than one per axis.
	//
	// It carries no omission tag, matching the entity listing beside it: the two are
	// the same population class, so a caller reading both meets one presence rule
	// rather than one per listing. The finding sets below do carry one, which is the
	// split a reader can act on — a listing is always answered, a finding only
	// appears when there is one.
	Libraries []closureRef `json:"libraries"`
	// MappingDiagnostics names each Manifest mapping whose target recipe does not
	// implement the mapped type. This tool reports on a Release rather than
	// producing anything from it, so a fault is described alongside the closure
	// rather than replacing the description with a refusal: a caller asking what a
	// Release contains is answered, and told what is wrong with it.
	MappingDiagnostics []string `json:"mapping_diagnostics,omitempty"`
	// Deprecations names each pinned reference carrying a recorded deprecation
	// marker, from the shared report the build reads — so a caller asking what a
	// Release depends on is told which of those dependencies has been deprecated,
	// rather than having to build it to find out. This tool describes rather than
	// produces, so the entries are informational and never a refusal.
	Deprecations []string `json:"deprecations,omitempty"`
}

// describePublishedPayload is the structured payload for a registry reference
// subject: the entity published at that coordinate, exactly as it was published.
//
// It is a shape of its own rather than the entity payload with fields left
// empty, because it answers a different question. The entity payload is what a
// catalog RESOLVED a name to, inheritance applied; this is what a publisher PUT
// at a coordinate. Document is therefore the published entity document itself —
// unpacked from the archive where the coordinate carries one, with nothing
// merged into it — and it is carried as the text it was published as rather than
// re-encoded, so a caller reading it reads the publisher's own document.
//
// Archive says whether the coordinate's bytes were an archive the document was
// unpacked from. MediaType is the artifact type the manifest declares, and
// ManifestDigest is that manifest's digest: what to name to read the same bytes
// again, and what the fetched content was checked against.
type describePublishedPayload struct {
	Reference      string `json:"reference"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Description    string `json:"description,omitempty"`
	MediaType      string `json:"media_type"`
	Archive        bool   `json:"archive"`
	ManifestDigest string `json:"manifest_digest"`
	Document       string `json:"document"`
}

var describeToolSpec = toolSpec{
	kinds:   []string{"component", "feature", "release"},
	summary: "Describes a catalog entity, an entity file, or everything a Release reaches.",
	description: "Describe an Aggrega subject in structured form, rendering nothing and writing nothing. Two modes: pass `name` to resolve a " +
		"catalog entity of any kind by its type URI and get its fully-resolved definition (schema, uses-schema, provides, " +
		"labels) with inheritance applied; or pass `release` (a Release path) to get its dependency closure — what a " +
		"Release is built FROM, not its shape — the resolved Aggregate and Manifest identities plus every catalog " +
		"entity and shared library it reaches, with resolved versions. Use `name` to understand a building block " +
		"before wiring it into an Aggregate, and `release` to see what a Release depends on before building it. " +
		"`name` may instead be a registry reference, oci://<host>/<repository>:<tag> or @<digest>, which reads the " +
		"entity exactly as it was published there — reaching that registry with the login the machine this server " +
		"runs on holds for it, checking the bytes against the digest the artifact's own manifest records and against " +
		"nothing else, and writing nothing. Over a listener, a reference is read only from a registry the served " +
		"workspace is configured with — its default registry, or one listed under `registries` — and a reference to " +
		"any other host is refused before that host is contacted. " +
		"`response_format` shapes the `release` mode: detailed (every reference) or concise (identity and counts).",
	workspaceSelectionNote: "Optional `workspace_root` and `catalog_dirs` override discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "minLength": 1,
      "description": "A catalog entity type URI (any kind) to resolve and describe, e.g. core/compute/api-server — or a registry reference, e.g. oci://hub.aggrega.ai/component/core/compute/api-server:1.0.0, to read the entity published there. Mutually exclusive with release."
    },
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "A Release path whose dependency closure to report. Mutually exclusive with name."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories, prepended (first-match-wins) to the workspace catalog search stack."
    },
    "response_format": {
      "type": "string",
      "enum": ["detailed", "concise"],
      "description": "detailed returns every closure reference; concise returns identity and counts. Release mode only; defaults to detailed."
    }
  },
  "anyOf": [
    {"required":["name"],"not":{"required":["release"]}},
    {"required":["release"],"not":{"required":["name"]}}
  ]
}`,
	invoke: describeInvoke,
}

func describeInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a describeArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("describe", err)
		}
	}
	// Read ahead of the subject, and for both subjects alike: the declared set the
	// argument carries is the tool's, not one scope's, so a value outside it is a
	// value this tool does not accept whichever subject the call went on to name.
	format, refusal := parseResponseFormat("describe", a.ResponseFormat, deliveryResponseFormatDefault)
	if refusal != "" {
		return errorResult(refusal)
	}
	name := strings.TrimSpace(a.Name)
	release := strings.TrimSpace(a.Release)
	switch {
	case name != "" && release != "":
		return errorResult("describe takes exactly one of 'name' or 'release', not both")
	case name == "" && release == "":
		return errorResult("describe requires either a 'name' (catalog entity) or a 'release' (path)")
	case compilerops.IsRegistryReference(name):
		return s.describePublishedEntity(ctx, a, name)
	case name != "":
		return s.describeCatalogEntity(a, name)
	default:
		return s.describeRelease(ctx, a, release, format)
	}
}

// describeCatalogEntity resolves a name by its type URI through the
// request-scoped view — with extends inheritance applied — and returns the
// fully-resolved definition of whichever kind it matched.
//
// The resolution is the shared one every by-name surface runs, so a name that
// resolves at a command line or over the daemon's protocol resolves here too, to
// the same entity and the same kind. A name that resolves to nothing is refused
// rather than answered with an empty definition: an empty answer would report a
// mistyped name as an entity with nothing to say about itself.
func (s *server) describeCatalogEntity(a describeArgs, name string) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for the catalog lookup.", err, "component"))
	}
	// A name concerns no one Release, so it resolves over the workspace's own stack.
	view := tw.newView("", nil)

	match, ok := compilerops.ResolveEntity(view, name)
	if !ok {
		return errorResult(explainDiagnostic(fmt.Sprintf(
			"No catalog entity named %q resolved, in any kind (%s). Check the type URI and that the "+
				"catalog directories cover it.",
			name, strings.Join(compilerops.EntityKinds(), ", ")), "component"))
	}
	def, mErr := json.Marshal(match.Definition)
	if mErr != nil {
		return nil, &rpcError{Code: codeInternalError,
			Message: "marshal " + strings.ToLower(match.Kind) + ": " + mErr.Error()}
	}
	payload := describeEntityPayload{
		Kind: match.Kind, Name: match.Name, Version: match.Version,
		Description: match.Description, PublishScope: match.Scope,
		AuthoredLength: len(match.Bytes),
		Definition:     def,
	}
	// Spelled first the way every surface spells a reported document, then told
	// the way this call's caller may be told it: over a listener a document
	// outside the workspace is kept back rather than named absolutely.
	payload.File, payload.FileWithheld = tw.reportedLocationField(pathscope.ReportedName(tw.root, match.Source))
	return tw.answer(textResult(renderDescribeEntityText(payload), payload))
}

// describePublishedEntity reads the entity a registry reference publishes, through
// the reading every surface showing a published entity shares, and answers with it
// as it was published.
//
// The read reaches the registry the reference names, with the login the user this
// process runs as holds for that registry, and takes no credential from the call.
// Over a listener it reaches only a registry the served workspace is configured
// with, which publishedReadRefusal decides after the workspace resolves and before
// anything is contacted.
//
// A failure is answered as guidance the caller can act on: what could not be read,
// the short code of the condition that names why where the failure carries one,
// and what to do about it.
func (s *server) describePublishedEntity(ctx context.Context, a describeArgs, reference string) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace whose network posture the read runs under.", err, ""))
	}
	if refusal := tw.publishedReadRefusal(reference); refusal != "" {
		return errorResult(refusal)
	}
	entity, err := compilerops.ReadPublishedEntity(ctx, tw.cfg, reference)
	if err != nil {
		return errorResult(publishedReadFailure(reference, err))
	}
	payload := describePublishedPayload{
		Reference: entity.Reference, Kind: entity.Kind, Name: entity.Name, Version: entity.Version,
		Description: entity.Description, MediaType: entity.MediaType, Archive: entity.Archive,
		ManifestDigest: entity.Digest, Document: string(entity.Document),
	}
	return textResult(renderDescribePublishedText(payload), payload)
}

// publishedReadRefusal reports the corrective text for a registry reference this
// session does not read, or the empty string when the read may go ahead.
//
// A client that launched this server over its own streams is at this machine's
// keyboard, so it reads from whatever registry it names with its own login — the
// footing the command line stands on — and nothing is decided here.
//
// A caller reaching a listener is somewhere else, and the login the read spends
// is this host's. So it reads only from a registry the served workspace is
// configured with, as the configuration's own answer to that question decides,
// and asked about the host the read itself would contact. That closes both things
// a caller-chosen destination would open: reading through this host's login from
// a registry the workspace does not use, and pointing this host at an address of
// the caller's choosing, a service on this machine included. What stays open is
// deliberate: a read-scoped caller reads what this host's login can read on a
// registry the workspace is configured with, which a person at this host's
// terminal can do as well.
//
// The workspace is the served one — a caller on a listener cannot name another —
// so the set a call is held to is one the operator configured. A reference whose
// host cannot be read is refused with the reading's own sentence, since there is
// no host to admit; nothing has been contacted at either refusal.
func (tw *toolWorkspace) publishedReadRefusal(reference string) string {
	if tw.transport != transportHTTP {
		return ""
	}
	host, err := compilerops.RegistryReferenceHost(reference)
	if err != nil {
		return publishedReadFailure(reference, err)
	}
	if tw.cfg.IsConfiguredRegistry(host) {
		return ""
	}
	return fmt.Sprintf("describe did not read %s: %s is not a registry this workspace is configured with, and "+
		"nothing was contacted. Over a listener, a registry reference is read only from the served workspace's "+
		"default registry or a registry listed under `registries` in its configuration. Describe the entity by its "+
		"catalog name instead; whoever runs this server can add the registry to the served workspace with "+
		"`aggrega registry add %s --workspace` run there, or read the reference at a terminal on its host with "+
		"`aggrega inspect %s`.", reference, host, host, reference)
}

// publishedReadFailure is the text a read that could not go ahead is answered
// with: what could not be read, the short code of the condition that names why
// where the failure carries one, and the guidance that condition carries.
func publishedReadFailure(reference string, err error) string {
	text := err.Error()
	if short, ok := cerrors.ShortCodeOf(err); ok {
		text = short + ": " + text
	}
	return "Could not read the entity published at " + reference + ".\n\n" + explainDiagnostic(text, "")
}

// renderDescribePublishedText renders the human-readable summary of a published
// entity: the identity it declares, where it was read from, and the document
// itself, which is the answer a caller asking about a coordinate wants to read.
func renderDescribePublishedText(p describePublishedPayload) string {
	var b strings.Builder
	line := fmt.Sprintf("%s %s (version %s), published at %s", p.Kind, p.Name, p.Version, p.Reference)
	if strings.TrimSpace(p.Description) != "" {
		line += ": " + p.Description
	}
	b.WriteString(line)
	fmt.Fprintf(&b, "\nmanifest: %s\n\n", p.ManifestDigest)
	b.WriteString(strings.TrimRight(p.Document, "\n"))
	return b.String()
}

// describeRelease resolves a Release's catalog closure and returns a summary of
// the entities it reaches.
//
// It takes the resolved response format rather than reading the argument again:
// the value was read once, where a value outside the declared set is refused, and
// a second read here would be a second reading of one argument.
func (s *server) describeRelease(
	ctx context.Context, a describeArgs, release, format string,
) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for the Release.", err, "release"))
	}
	releasePath, refusal := tw.workspacePath("describe", "release", release)
	if refusal != "" {
		return errorResult(refusal)
	}
	lf := loadReleaseLockfile(releasePath)
	view := tw.newView(releasePath, lf)

	resolved, cerr := compilerops.ResolveClosure(ctx, compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   tw.root,
		WorkspaceConfig: tw.cfg,
		Lockfile:        lf,
		Augment:         true,
	})
	if cerr != nil {
		return tw.answer(errorResult(explainToolError(
			"Could not resolve the Release closure to describe it.", cerr, closureStageKind(cerr.Stage))))
	}

	detail := format == responseFormatDetailed
	// Collected whatever the format, and reported only at the detailed one. The
	// count is the length of the listing rather than a second traversal, so the two
	// cannot come to disagree about how many references were reached.
	libraries := describeLibraryRefs(resolved.Closure)
	payload := describeReleasePayload{
		Aggregate:    resolved.Aggregate.Metadata.Name,
		Manifest:     resolved.Manifest.Metadata.Name,
		Environment:  resolved.Release.Metadata.Environment,
		EntityCount:  len(resolved.Closure.Entities),
		LibraryCount: len(libraries),
	}
	payload.Release, payload.ReleaseWithheld = tw.reportedLocationField(releasePath)
	// Both finding sets are prose the compiler composed, so an entry may name a
	// place on this host; each is told the way the rest of this answer is.
	for _, f := range resolved.ManifestConsistencyFindings {
		payload.MappingDiagnostics = append(payload.MappingDiagnostics, tw.reportedText(f.Error()))
	}
	for _, f := range lf.DeprecationFindings() {
		payload.Deprecations = append(payload.Deprecations, tw.reportedText(f.Notice()))
	}
	if detail {
		for _, e := range resolved.Closure.Entities {
			// The entity axis mixes kinds, so it states none and each reference
			// carries its own hint. The kind is rendered as an author writes it
			// rather than as the walk recorded it, through the shared projection
			// every tool here reports a resolved reference through — so one entry
			// reads the same whichever tool returned it.
			payload.Entities = append(payload.Entities, closureRefOf(e.URI, e.ResolvedVersion, "", e.KindHint))
		}
		payload.Libraries = libraries
	}
	return tw.answer(textResult(renderDescribeReleaseText(payload), payload))
}

// describeLibraryRefs renders every shared document the closure reaches into the
// reported shape, joined across the axes in the order they are declared.
//
// It ranges over the closure's own declared axis set rather than naming the axes
// here. That set is the single enumeration of them, so an axis added to it is
// reported by construction — whereas a listing that named the axes itself would go
// on reporting the old set, with nothing failing to say an axis had stopped being
// reported. A library axis fixes the kind its members publish under, so the kind
// comes off the declaration and no reference carries a hint.
//
// An axis with no members contributes nothing, so the result describes what this
// closure reached rather than the shape of the axis set.
func describeLibraryRefs(cl closure.Closure) []closureRef {
	var out []closureRef
	for _, axis := range closure.LibraryAxes() {
		for _, l := range axis.Libraries(&cl) {
			out = append(out, closureRefOf(l.URI, l.ResolvedVersion, axis.Kind, ""))
		}
	}
	return out
}

// renderDescribeEntityText renders the human-readable one-line summary of a
// catalog entity, with its description when it carries one.
//
// It reads the description off the payload rather than taking it separately, so
// the summary and the structured answer cannot come to disagree about what the
// entity says of itself. The summary states less than the payload — it names
// neither the publishing scope nor the authored document — which is a difference
// in how one line is written for a reader, not in what the answer carries.
func renderDescribeEntityText(p describeEntityPayload) string {
	line := fmt.Sprintf("%s %s (version %s)", p.Kind, p.Name, p.Version)
	if strings.TrimSpace(p.Description) != "" {
		line += ": " + p.Description
	}
	return line
}

// renderDescribeReleaseText renders the human-readable Release closure summary.
func renderDescribeReleaseText(p describeReleasePayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Release %s\n", reportedLocationWords(p.Release))
	fmt.Fprintf(&b, "  aggregate: %s\n", p.Aggregate)
	fmt.Fprintf(&b, "  manifest: %s\n", p.Manifest)
	if p.Environment != "" {
		fmt.Fprintf(&b, "  environment: %s\n", p.Environment)
	}
	fmt.Fprintf(&b, "  closure entities: %d\n", p.EntityCount)
	for _, e := range p.Entities {
		fmt.Fprintf(&b, "    - %s\n", describeClosureRefLine(e))
	}
	// The shared documents follow the entities and are omitted where the closure
	// reaches none, so a Release drawing on no library reads the same as it did.
	if p.LibraryCount > 0 {
		fmt.Fprintf(&b, "  shared documents: %d\n", p.LibraryCount)
		for _, l := range p.Libraries {
			fmt.Fprintf(&b, "    - %s\n", describeClosureRefLine(l))
		}
	}
	// The mapping faults follow the closure they were found in rather than
	// preceding it, so the description the caller asked for arrives first and is
	// not mistaken for an error report.
	for _, d := range p.MappingDiagnostics {
		fmt.Fprintf(&b, "  mapping: %s\n", d)
	}
	for _, d := range p.Deprecations {
		fmt.Fprintf(&b, "  deprecation: %s\n", d)
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeClosureRefLine renders one reached reference for the human-readable
// summary: its identity, and the version it resolved to where it resolved to one.
//
// A reference that resolved to no version prints its identity alone rather than an
// empty pair of brackets, because a reference resolving to nothing and one
// resolving to the empty string are different statements and only one is true.
// Both listings render through this, so an entry reads the same whichever listing
// it came from.
func describeClosureRefLine(r closureRef) string {
	if r.Version == "" {
		return r.URI
	}
	return fmt.Sprintf("%s (%s)", r.URI, r.Version)
}
