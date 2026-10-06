// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// pluginWriteScope states how far a plugin mutation reached.
//
// Every plugin-mutating response carries one, for the reason the registry
// methods' scope carries one: a request names one workspace, so an answer
// with no scope on it reads as being about that workspace — and for a
// command-extension plugin that reading is wrong by default, since a command
// extension is machine-wide tooling and defaults to the global configuration file
// every workspace on this machine resolves through.
//
// MachineWide is derived from the file the write actually resolved to rather than
// from the request, so a response cannot describe a machine-wide change as a
// local one by consulting the wrong input.
type pluginWriteScope struct {
	// ConfigFile is the configuration file the write landed in.
	ConfigFile string `json:"configFile"`
	// MachineWide is true when that file is the global one, which every workspace
	// on this machine resolves through.
	MachineWide bool `json:"machineWide"`
	// WorkspaceRoot is the workspace this session was opened over, so a caller
	// reading a workspace-scoped write knows which workspace it was.
	WorkspaceRoot string `json:"workspaceRoot"`
}

// pluginScope projects a plugin operation's resolved write target onto the wire
// shape, adding the workspace this session holds.
func (s *Server) pluginScope(configPath string, machineWide bool) pluginWriteScope {
	return pluginWriteScope{
		ConfigFile:    configPath,
		MachineWide:   machineWide,
		WorkspaceRoot: s.workspaceRoot,
	}
}

// workspacePath resolves a path confined to the workspace this session opened,
// and refuses every other form.
//
// 🔴 It is deliberately NOT the canonical-file resolution the document methods
// use. That one accepts an absolute value and leaves it standing, which is right
// for a document the client already holds open and names by whatever spelling it
// holds.
//
// 🔴 What this confines without exception is where an operation WRITES. A
// destination of the request's own choosing would let a session create or
// overwrite files in a tree it was never opened over, and no request here may
// name one: each outputDir, the build's outputParent, the entity a pin writes its
// lock beside and the files the plugin alignment edits in place are judged here.
// The structured document edit, whose file the edit layer resolves, is held there
// by the same judgement, so the edit has one home for it on every surface.
//
// A path an operation only READS is a separate question, decided per parameter
// rather than by one rule over all of them, because a file the person driving the
// client selected is not a workspace-scoped path at all — those parameters go
// through selectedPath instead. A read input stays here until that case
// has actually been asked for on the request that carries it, so confinement is
// this surface's default rather than a claim that a selection could not be
// admitted: the alignment method's module path is confined for that reason and
// not because reading it anywhere would be unsound.
//
// Resolution runs through the one home for workspace-scoped paths, so an
// absolute value, a value climbing out of the root, and an empty value are all
// refused by the same rule every other workspace-scoped path is judged by, rather
// than by a comparison written here.
//
// That rule is lexical, so a value it accepts is then judged with its links
// resolved, by the one judgement every path a request names is held to
// (leadsOutOfWorkspace): the deepest part of the path that exists is resolved,
// and a value written inside the root whose deepest existing part lies outside it
// is refused, so nothing is written outside the workspace through a link — into a
// linked directory that leads out, or into a directory not made yet beneath one.
// The refusal quotes the value as it was sent and says nothing of where the link
// leads. The path returned is still the one the value spells.
func (s *Server) workspacePath(what, raw string) (string, error) {
	resolved, ok := pathscope.Resolve(s.workspaceRoot, raw)
	if !ok {
		return "", fmt.Errorf("%s must name a path inside the workspace this session opened (%s), "+
			"relative to its root; %q is absolute or climbs out of it", what, s.workspaceRoot, raw)
	}
	if leadsOutOfWorkspace(s.workspaceRoot, resolved, nil) {
		return "", fmt.Errorf("%s must name a path inside the workspace this session opened (%s), "+
			"relative to its root; %q leads out of it through a link", what, s.workspaceRoot, raw)
	}
	return resolved, nil
}

// selectedPath resolves a path the person driving the client chose, which may
// name anywhere on this machine.
//
// 🔴 It confines nothing, and that is the contract rather than a hole in it. Its
// subject is a file someone picked out of a dialog — a module built under a
// toolchain's own output directory, a description exported by another tool — and
// such a file sits wherever they put it, which is frequently outside the
// workspace a session was opened over. Judging it by the workspace-scoped rule
// would refuse the request for the shape it exists to serve, and would refuse it
// for no gain: this process runs as the person who selected the path and already
// reads whatever they can read.
//
// It is reached only by a parameter an operation READS. Where an operation
// writes, workspacePath decides, and a write is confined whatever the
// caller sends — so a request may point a read at a file it chose and can still
// not point a write out of the workspace.
//
// The refusals belong to the value rather than to its location: an empty value
// names no file, and a relative value with no workspace to resolve against would
// silently address whatever directory this process happens to be sitting in.
func (s *Server) selectedPath(what, raw string) (string, error) {
	resolved, ok := pathscope.ResolveSelected(s.workspaceRoot, raw)
	if !ok {
		return "", fmt.Errorf("%s must name a file to read: either an absolute path, or one relative "+
			"to the root of the workspace this session opened (%s); %q names no file",
			what, s.workspaceRoot, raw)
	}
	return resolved, nil
}

// pickedOrWorkspacePath resolves a path a request names for a file to read that a
// person may have picked in a dialog, and that a request may equally write
// relative to the workspace: a registration's public-key file and its trust root.
//
// An absolute value is a dialog's pick, and is read where it names, as selectedPath
// reads one. A relative value is the request's own spelling of a file inside the
// workspace this session opened, so it is anchored to that workspace's root —
// never to the directory this process happens to run in, which the request did
// not name and cannot see — and held there by workspacePath, the one judgement
// every path a request names is held to: a value climbing out of the root, or
// leading out of it through a link, is refused before anything is read.
//
// An empty value names nothing, and is answered with nothing: these parameters are
// optional.
func (s *Server) pickedOrWorkspacePath(what, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if filepath.IsAbs(raw) {
		return s.selectedPath(what, raw)
	}
	return s.workspacePath(what, raw)
}

// pluginAddParams is what aggrega/pluginAdd takes.
//
// The spellings are the ones the command line uses, so a caller that learned the
// options on one surface knows them on the other. `global` and `workspace` are
// the write-target overrides, and sending both is refused exactly as the command
// line refuses it.
//
// acceptCapabilities is how the command-extension type's capability decision
// arrives here, and it is a required decision rather than a default. The command
// line obtains it by asking at a terminal; this surface has no terminal, so the
// client asks whoever is driving it and sends the answer. A request that does not
// consent is refused with what to send named, rather than installing on a consent
// nobody gave.
//
// dryRun does everything the plain request does except write: it answers with
// the resulting configuration as content instead of putting it on disk. Which of
// the two a caller wants is a property of the caller rather than of this
// surface — a client that keeps its own document model puts the content where the
// author can read it before it is saved — so it is a request field. It defaults
// to writing, because the operation's product is a configuration file and a
// surface that produces a document owes the document. Sent with the cli type it is
// refused rather than ignored, for the reason the shared layer states: a silently
// ignored argument reads to a caller as one that was honoured.
type pluginAddParams struct {
	Type               string `json:"type"`
	Query              string `json:"query"`
	Capability         string `json:"capability"`
	Role               string `json:"role"`
	Global             bool   `json:"global"`
	Workspace          bool   `json:"workspace"`
	AcceptCapabilities bool   `json:"acceptCapabilities"`
	DryRun             bool   `json:"dryRun"`
}

// pluginAddResult is the aggrega/pluginAdd response.
//
// Exactly one of identity, uri and reference is present, and which one says what
// kind of attachment landed: a socket type pins the bare version-pinned entity
// identity, a provider binding carries the plugin's oci:// artifact identity, and
// a command-extension install reports the pinned reference whose commands were
// registered. A caller reading the answer can tell which without re-deriving it
// from the type it sent.
//
// Written is what a client branches on: under a write it names a file now on disk
// that the client can open, and under a dry run it is false and Content carries
// the document instead. The two are never both meaningful — the file Content is
// for is Scope.ConfigFile, so nothing here carries a second copy of the path.
//
// Retraction is absent on an ordinary attachment and present when the withdrawal
// screen had something to say about the version that was pinned. A retraction is a
// withdrawal, frequently for a safety reason, so a caller handed an older version
// than the newest published one is owed the reason — the command line states it on
// the terminal, and this is where it travels for a caller that answers with values.
type pluginAddResult struct {
	Type       string            `json:"type"`
	Identity   string            `json:"identity,omitempty"`
	URI        string            `json:"uri,omitempty"`
	Capability string            `json:"capability,omitempty"`
	Reference  string            `json:"reference,omitempty"`
	Scope      pluginWriteScope  `json:"scope"`
	Written    bool              `json:"written"`
	Content    string            `json:"content,omitempty"`
	Retraction *pluginRetraction `json:"retraction,omitempty"`
}

// pluginRetractionExclusion is one published version the withdrawal screen removed
// from selection, with the reason recorded against it. A record is not required to
// carry prose, so reason may be absent.
type pluginRetractionExclusion struct {
	Version string `json:"version"`
	Reason  string `json:"reason,omitempty"`
}

// pluginRetractionProvenance says where a withdrawal screen's facts came from and
// how current they are.
//
// Source is the screen's own spelling for where it read them, carried across
// unchanged rather than restated here, so this surface cannot come to name a source
// the screen would name differently.
//
// LastUpdated is when the snapshot behind those facts was taken, and it is absent
// rather than zero whenever there is no such time to report — a reading taken during
// the operation has none to report at all, and a snapshot may carry no build time.
// It is the spelling a searched registry's freshness already travels under on this
// surface, so one concept keeps one name across the answers that carry it.
type pluginRetractionProvenance struct {
	Source      string `json:"source"`
	LastUpdated string `json:"lastUpdated,omitempty"`
}

// pluginRetraction is what the withdrawal screen concluded about the version an
// attachment pinned.
//
// It carries the screen's own conclusion rather than the sentence the command line
// composes from it, on the rule this surface applies to every other answer: a
// structured fact the shared operation computed travels as values, and how those
// values are worded is each surface's own. A caller rendering them is free to say
// it in the words its reader is reading, and cannot fall out of step with a
// sentence composed somewhere else.
//
// Excluded with Selected says the screen ran and withdrew the versions named,
// pinning Selected in their place. Unscreened says the opposite thing and is the
// reason the two are separate fields: the withdrawal records could not be read at
// all, so nothing was screened and no claim is made about whether the pinned
// version has since been withdrawn. A caller must not read an empty Excluded as
// "nothing was withdrawn" without looking at Unscreened first.
//
// Provenance qualifies whichever of those two arrived, and is absent when selection
// ran with no screen — where nothing was consulted there is no source to report. A
// caller that acts on a withdrawal needs it: a reading taken from the registry while
// the operation ran speaks to now, while a snapshot can be older than the withdrawal
// it is being asked about, and only the second leaves a caller anything to do about
// the gap.
type pluginRetraction struct {
	Reference  string                      `json:"reference"`
	Selected   string                      `json:"selected,omitempty"`
	Excluded   []pluginRetractionExclusion `json:"excluded,omitempty"`
	Unscreened string                      `json:"unscreened,omitempty"`
	Provenance *pluginRetractionProvenance `json:"provenance,omitempty"`
}

// pluginRetractionOf projects the operation's advisory onto this surface's wire
// shape, and returns nil when there was none.
//
// It is a projection rather than an emission of the shared type because the shared
// type carries the command line's own field spellings; emitting it directly would
// put capitalised keys on a surface whose every other field is lowercase. Nothing
// is recomputed here — each value is carried across unchanged — so this surface
// cannot disagree with the screen about what was withdrawn.
func pluginRetractionOf(advisory *pluginops.RetractionAdvisory) *pluginRetraction {
	if advisory == nil {
		return nil
	}
	out := &pluginRetraction{
		Reference:  advisory.Reference,
		Selected:   advisory.Selected,
		Unscreened: advisory.Unscreened,
		Provenance: pluginRetractionProvenanceOf(advisory.Provenance),
	}
	for _, x := range advisory.Excluded {
		out.Excluded = append(out.Excluded, pluginRetractionExclusion{Version: x.Version, Reason: x.Reason})
	}
	return out
}

// pluginRetractionProvenanceOf projects a screen's provenance onto this surface's
// wire shape, and returns nil when the advisory rests on no screen.
//
// An empty source is how a selection that was never screened states that it has no
// evidence to name, so it answers with nothing rather than with a shape whose source
// is the empty string — a caller reading an object with a blank source cannot tell it
// from a source this surface failed to fill.
//
// The observation time is rendered by the provenance itself rather than formatted
// here, so the instant this answer quotes and the instant a terminal reader is shown
// are one text produced once.
func pluginRetractionProvenanceOf(provenance registryindex.RetractionProvenance) *pluginRetractionProvenance {
	if provenance.Source == "" {
		return nil
	}
	return &pluginRetractionProvenance{
		Source:      string(provenance.Source),
		LastUpdated: provenance.ObservedAtStated(),
	}
}

// dispatchPluginAdd searches the configured registry for a plugin of the named
// type and adds it to the configuration.
//
// 🔴 It WRITES, and for the command-extension type the file it writes is by
// default the global one every workspace on this machine resolves through — so an
// addition a session performs changes what resolves for projects this session has
// never heard of. The response says so on every answer rather than only where the
// caller asked for the global file, because for that type the default is the
// machine-wide one and a caller that sent no scope flag is exactly the caller most
// likely to assume otherwise.
//
// It reaches a registry, and it is carried here on the same rule every other
// reaching-and-writing method runs under: a caller that sent this has asked for
// the addition, exactly as a caller sending a build has asked for artifacts. What
// stays out of bounds is a write, or a reach for a registry, performed while
// validating or inspecting, where nobody asked, and this is not that.
//
// Nothing is prompted for. The one decision the command line obtains by asking at
// a terminal — whether the caller accepts a command-extension plugin's declared
// capabilities — arrives as a request field, so the client asks whoever is driving
// it and this surface never blocks on a prompt no one can answer.
//
// 🔴 It WRITES unless the request asks for a dry run instead, and the delivery is
// a request field rather than a property of this surface, because a caller at
// either end may want either. Under a dry run nothing is created or modified and
// the answer carries the configuration document the write would have produced,
// addressed to the file it would have gone to — which for a --global attachment is
// the machine-wide one, so the scope on the answer is what tells a client how far
// the content it now holds would reach. It still reaches a registry under a dry
// run, because the identity in that document is the identity a write would have
// pinned — a dry run differs from the write in exactly one respect, and that is
// the write itself.
func (s *Server) dispatchPluginAdd(ctx context.Context, req *jsonRPCRequest) error {
	var params pluginAddParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.Type == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin add: type is required (the plugin socket to add for, one of "+pluginops.KindsHint()+")")
	}
	if params.Query == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin add: query is required (a substring matched against the plugin names of that type, resolving to exactly one)")
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to resolve a plugin write target against")
	}

	// The type-specific required fields are checked here, on this surface's own
	// spellings, rather than left to the shared layer. The layer names the command
	// line's flags in its refusals, and a refusal naming a flag a caller never sent
	// tells them to fix something they cannot reach from here.
	isCLI := params.Type == string(wasm.PluginTypeCLI)
	if params.Type == pluginops.ProviderSocket && params.Capability == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin add: the provider type requires capability, naming the capability the binding serves")
	}
	// The role is tested with the predicate the shared layer refuses on, so an
	// unknown role is refused here, in this surface's terms, exactly as a missing
	// one is, and no role the layer would accept is refused.
	if _, known := plugintype.GovernanceRoleTier(plugintype.Role(params.Role)); !known && strings.EqualFold(params.Type, "governance") {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin add: the governance type requires role, naming the attachment role ("+pluginops.GovernanceRolesHint()+")")
	}
	if isCLI {
		if !params.AcceptCapabilities {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
				"plugin add: installing a cli plugin requires acceptCapabilities, confirming its declared capabilities; "+
					"this surface has no terminal to ask on, so the decision is the caller's to obtain and send")
		}
		if s.installCLIPlugin == nil {
			return s.writeError(ctx, req.ID, jsonRPCInternalError,
				"plugin add: this daemon was started without a command-extension installer, so it cannot install a cli plugin")
		}
	}

	// The request is mapped onto a delivery through the one mapper every surface
	// routes through, and the answer's Written field is read back off it through the
	// one predicate — so this surface expresses the choice nowhere in its own terms
	// and needs no second field tracking whether a write happened.
	delivery := authoredcontent.DeliveryFor(params.DryRun)
	res, err := pluginops.Add(ctx, s.resolvedWorkspace, pluginops.AddOptions{
		Socket:             params.Type,
		Query:              params.Query,
		Capability:         params.Capability,
		Role:               params.Role,
		Global:             params.Global,
		Workspace:          params.Workspace,
		AcceptCapabilities: params.AcceptCapabilities,
		Delivery:           delivery,
		InstallCLIPlugin:   s.installCLIPlugin,
		// The prose written here is for a person watching a terminal, and this surface
		// has none, so it is discarded and the facts are read off the result instead:
		// a version substitution travels on Retraction, and the install path's account
		// of what it registered is the pinned reference the result already names.
		//
		// 🔴 It is a discarding writer rather than no writer, and the difference is
		// load-bearing. Version selection is screened for withdrawals only when there
		// is a writer at all — the operation leaves it unscreened otherwise, rather
		// than skipping a withdrawn version with nobody to tell — so passing nothing
		// here would change which version an attachment pins.
		Notices: io.Discard,
		In:      strings.NewReader(""),
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	return s.writeResult(ctx, req.ID, pluginAddResult{
		Type:       res.Socket,
		Identity:   res.Identity,
		URI:        res.URI,
		Capability: res.Capability,
		Reference:  res.Reference,
		Scope:      s.pluginScope(res.ConfigPath, res.ConfigIsMachineWide),
		Written:    !delivery.Reports(),
		Content:    string(res.Content),
		Retraction: pluginRetractionOf(res.Retraction),
	})
}

// pluginRemoveParams is what aggrega/pluginRemove takes: the plugin type the
// entry was attached under, the query identifying the entry, and the same
// write-target overrides an addition takes.
type pluginRemoveParams struct {
	Type      string `json:"type"`
	Query     string `json:"query"`
	Global    bool   `json:"global"`
	Workspace bool   `json:"workspace"`
}

// pluginRemoveResult is the aggrega/pluginRemove response.
type pluginRemoveResult struct {
	Type  string           `json:"type"`
	Query string           `json:"query"`
	Scope pluginWriteScope `json:"scope"`
}

// dispatchPluginRemove drops a configured plugin's entry from the configuration.
//
// 🔴 It WRITES, and for the command-extension type it writes the machine-wide
// file by default, so the response names the file it removed from and whether that
// file is the shared one.
//
// It reaches no registry: a removal operates on the configuration text alone. A
// query that matches nothing fails rather than reporting success over an unchanged
// configuration, so a caller is never told a plugin stopped participating when it
// did not.
func (s *Server) dispatchPluginRemove(ctx context.Context, req *jsonRPCRequest) error {
	var params pluginRemoveParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.Type == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin remove: type is required (the plugin socket the entry was added for, one of "+pluginops.KindsHint()+")")
	}
	if params.Query == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin remove: query is required (the plugin's short name, its pinned reference, or — for the provider type — the capability the binding serves)")
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to resolve a plugin write target against")
	}

	res, err := pluginops.Remove(s.resolvedWorkspace, pluginops.RemoveOptions{
		Socket:    params.Type,
		Query:     params.Query,
		Global:    params.Global,
		Workspace: params.Workspace,
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	return s.writeResult(ctx, req.ID, pluginRemoveResult{
		Type:  res.Socket,
		Query: res.Query,
		Scope: s.pluginScope(res.ConfigPath, res.ConfigIsMachineWide),
	})
}

// pluginSyncParams is what aggrega/pluginSync takes: the Plugin entity file whose
// machine-owned records are aligned, and the module whose imports and self-report
// determine what they are aligned to.
//
// Both are required and neither has a default. The records are derived from the
// module, so an alignment with no module named has nothing to align to.
type pluginSyncParams struct {
	Entity string `json:"entity"`
	Plugin string `json:"plugin"`
}

// pluginSyncResult is the aggrega/pluginSync response.
//
// Changed is what separates a write from an alignment that found the records
// already correct, and it is the field a caller acts on: an unchanged file was
// left byte-identical, so a client holding that document has nothing to reload.
//
// The commands travel as values rather than as the line a terminal prints, so a
// caller can show the mirror its own way.
type pluginSyncResult struct {
	Entity        string                  `json:"entity"`
	HostFunctions []string                `json:"hostFunctions"`
	Commands      []wasm.PluginCLICommand `json:"commands"`
	Changed       bool                    `json:"changed"`
}

// dispatchPluginSync aligns a plugin's machine-owned records in its entity file
// with its module: the host-function set derived from the module's import
// section, and the command mirror read from the module's self-report.
//
// 🔴 It WRITES, and what it writes is a file the author owns and goes on editing.
// That is admitted on the same rule the other writing methods run under — a caller
// that sent this has asked for the alignment — and it is narrow in a way worth
// stating: only the two machine-owned blocks change, every other byte carries
// through verbatim, and a file already recording what the module implies is left
// byte-identical and reported as unchanged. The write is atomic, so a client
// reading the file concurrently never observes a partial document.
//
// It loads the guest to read its self-report, which is something this daemon
// already does on every build, and it reaches no registry.
func (s *Server) dispatchPluginSync(ctx context.Context, req *jsonRPCRequest) error {
	var params pluginSyncParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.Entity == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin sync: entity is required (the Plugin entity file whose machine-owned records are aligned)")
	}
	if params.Plugin == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin sync: plugin is required (the .wasm whose imports and self-report determine the recorded host functions and commands)")
	}

	entity, err := s.workspacePath("plugin sync: entity", params.Entity)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	plugin, err := s.workspacePath("plugin sync: plugin", params.Plugin)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}

	res, err := pluginops.Sync(ctx, pluginops.SyncOptions{EntityPath: entity, PluginPath: plugin})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	return s.writeResult(ctx, req.ID, pluginSyncResult{
		Entity:        res.EntityPath,
		HostFunctions: res.HostFunctions,
		Commands:      res.Commands,
		Changed:       res.Changed,
	})
}

// pluginGenCatalogParams is what aggrega/pluginGenCatalog takes.
//
// fromEmbedded and plugin are the two selection modes and exactly one applies:
// every guest embedded in this binary, or one guest at a named path. overlay is a
// directory in the first mode and a single file in the second.
//
// dryRun does everything the plain request does except write: it hands the
// generated content back instead of putting it on disk. It defaults to false,
// which writes — the same default the command line takes — so a caller that says
// nothing gets the same behaviour on both surfaces. It changes nothing else: the
// same guests are read and the same overlays matched, so a selection the write
// would refuse is refused here too.
//
// outputDir is required under both deliveries. Under a dry run nothing is
// written, but each returned document still names the file it is for, and that
// path is composed from this directory: a dry run changes where the content goes,
// not which file it is content for.
//
// 🔴 The three paths are not judged by one rule, and the split is read/write
// rather than per-field taste. plugin and overlay are READ, and both are files
// the author selected — a module sits under whatever directory its toolchain
// builds into, and its overlay is authored beside it — so each is accepted as an
// absolute path naming wherever it actually is. outputDir is WRITTEN to and stays
// confined to the workspace this session opened, so a generation cannot create
// entities in a tree the session was never opened over.
type pluginGenCatalogParams struct {
	FromEmbedded bool   `json:"fromEmbedded"`
	Plugin       string `json:"plugin"`
	OutputDir    string `json:"outputDir"`
	Overlay      string `json:"overlay"`
	DryRun       bool   `json:"dryRun"`
}

// pluginGenCatalogResult is the aggrega/pluginGenCatalog response.
//
// Exactly one half is filled, and DryRun says which. Documents carries the
// generated entities and the file each is for, and appears only under a dry run —
// where nothing was created or modified, so a client puts the content in its own
// document model, where it is visible before it is saved and can be amended or
// undone. Notices carries what the generation said about the writes it performed,
// and appears only when it wrote.
//
// DryRun answers with the same word the request selects the delivery with, so one
// concept carries one name in both directions and a client reads back what it
// asked for. It states the delivery outright rather than leaving it to be
// inferred from which half is filled, so a client branches on what ran instead of
// on what arrived.
type pluginGenCatalogResult struct {
	Documents []pluginGeneratedDocument `json:"documents,omitempty"`
	Notices   string                    `json:"notices,omitempty"`
	DryRun    bool                      `json:"dryRun"`
}

// pluginGeneratedDocument is one reported entity on the wire: the file it is for,
// and its content.
//
// 🔴 It exists because the shared document type is declared for the REPORTED FORM
// rather than for this protocol, and carries only that form's field tags. Emitting
// it directly serializes its Go field names, which arrive capitalised — every
// other shape on this surface is lowercase, so a caller would meet one field
// convention in one response and another everywhere else. Projecting here keeps
// the wire contract this surface's own, and leaves the shared type answering to
// the form it was declared for.
type pluginGeneratedDocument struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// generatedDocuments projects the generated entities onto the wire shape.
//
// It returns nil for an empty set rather than an empty slice, so the field is
// omitted entirely under the writing delivery — where no document was reported —
// instead of appearing as an empty list beside the notices.
func generatedDocuments(docs []authoredcontent.Document) []pluginGeneratedDocument {
	if len(docs) == 0 {
		return nil
	}
	out := make([]pluginGeneratedDocument, len(docs))
	for i, d := range docs {
		out[i] = pluginGeneratedDocument{Path: d.Path, Content: d.Content}
	}
	return out
}

// dispatchPluginGenCatalog generates one Plugin entity per selected guest,
// merging the machine-derived contract fields from the guest's self-report with an
// authored per-plugin overlay.
//
// 🔴 It WRITES unless the request asks it to report instead, and what it writes
// are catalog documents the author owns. Both postures are admitted on the same
// rule: a caller that sent this has asked for the generation. The reporting posture
// is the one a client holding its own document model wants, and it is a request
// field rather than a property of this surface, because a caller at either end may
// want either.
//
// 🔴 Where it writes is confined and what it reads is not. The output directory
// resolves inside the workspace this session opened, so the entities land in a
// tree the session was opened over whatever the request names. The module and its
// overlay are inputs the author selected, and both are accepted as absolute
// paths: a module built by a toolchain sits under that toolchain's own output
// directory, which is routinely outside the workspace, and an operation that
// refused to read it there could not serve the case it exists for. Reading a
// selected module means this process loads a guest from a path the caller named
// in order to read its self-report — deliberately, because the self-report is the
// ground truth the generation exists to transcribe, and the caller naming the
// path is the person the process is already running as.
//
// Regenerating over unchanged inputs yields byte-identical files, so a client may
// run it freely without producing a diff it has to explain.
func (s *Server) dispatchPluginGenCatalog(ctx context.Context, req *jsonRPCRequest) error {
	var params pluginGenCatalogParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.FromEmbedded == (params.Plugin != "") {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin gen-catalog: name exactly one selection — fromEmbedded for every guest this binary carries, "+
				"or plugin naming one .wasm")
	}
	if params.Overlay == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin gen-catalog: overlay is required (a directory of overlays with fromEmbedded, a single overlay file with plugin)")
	}
	// outputDir is required under BOTH deliveries, and reporting does not excuse
	// it. Under the reporting delivery nothing is written, but each reported
	// document still names the file it is FOR, and that path is composed from this
	// directory — so a dry run without it would answer with documents belonging to
	// nowhere.
	if params.OutputDir == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin gen-catalog: outputDir is required (the directory the generated entities are written under, "+
				"and — under dryRun — the directory each returned document names as its own)")
	}

	opts := pluginops.GenCatalogOptions{
		FromEmbedded:  params.FromEmbedded,
		DryRun:        params.DryRun,
		WorkspaceRoot: s.workspaceRoot,
	}
	// The three paths are resolved by TWO rules, and which rule a path takes
	// follows from whether the generation reads it or writes to it. The module and
	// its overlay are read, and both are files the author selected — a module
	// arrives under whatever output directory its toolchain writes to, and the
	// overlay is authored beside it — so each is resolved as a selection. The
	// output directory is written to, so it stays confined to the workspace this
	// session opened whatever the request sends.
	//
	// They are resolved by three separate calls rather than by a table walked with
	// a per-row resolver, because a resolver held as a value is invisible to a
	// walk over the source: the guard that keeps a selection out of a
	// workspace-scoped parameter matches the resolver at its call site, and a
	// method value carries no call for it to match.
	if params.Plugin != "" {
		plugin, err := s.selectedPath("plugin gen-catalog: plugin", params.Plugin)
		if err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
		}
		opts.PluginPath = plugin
	}
	overlay, err := s.selectedPath("plugin gen-catalog: overlay", params.Overlay)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	opts.OverlayPath = overlay
	outputDir, err := s.workspacePath("plugin gen-catalog: outputDir", params.OutputDir)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	opts.OutputDir = outputDir

	// The generation's prose goes nowhere: this surface answers with the values,
	// and the result carries whatever the generation said.
	res, err := pluginops.GenCatalog(ctx, opts, nil)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	return s.writeResult(ctx, req.ID, pluginGenCatalogResult{
		Documents: generatedDocuments(res.Documents),
		Notices:   res.Notices,
		DryRun:    params.DryRun,
	})
}
