// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
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

// workspacePath resolves a path a plugin request names, inside the workspace this
// session opened, and refuses every other form.
//
// 🔴 It is deliberately NOT the canonical-file resolution the document methods
// use. That one accepts an absolute value and leaves it standing, which is right
// for a document the client already holds open and names by whatever spelling it
// holds. These paths are different: the operation READS and WRITES the file they
// name, so accepting a location of the request's own choosing is exactly the
// addressing this surface declines — it is the ground on which running a guest
// from a caller-named path is not carried here at all. Accepting it for these
// would decline that capability for a property these methods then had too.
//
// Resolution runs through the one home for the relative-to-absolute direction, so
// an absolute value, a value climbing out of the root, and an empty value are all
// refused by the same rule every other workspace-scoped path is judged by, rather
// than by a comparison written here.
func (s *Server) workspacePath(what, raw string) (string, error) {
	resolved, ok := pathscope.Resolve(s.workspaceRoot, raw)
	if !ok {
		return "", fmt.Errorf("%s must name a path inside the workspace this session opened (%s), "+
			"relative to its root; %q is absolute or climbs out of it", what, s.workspaceRoot, raw)
	}
	return resolved, nil
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
type pluginAddParams struct {
	Type               string `json:"type"`
	Query              string `json:"query"`
	Capability         string `json:"capability"`
	Role               string `json:"role"`
	Global             bool   `json:"global"`
	Workspace          bool   `json:"workspace"`
	AcceptCapabilities bool   `json:"acceptCapabilities"`
}

// pluginAddResult is the aggrega/pluginAdd response.
//
// Exactly one of identity, uri and reference is present, and which one says what
// kind of attachment landed: a socket type pins the bare version-pinned entity
// identity, a provider binding carries the plugin's oci:// artifact identity, and
// a command-extension install reports the pinned reference whose commands were
// registered. A caller reading the answer can tell which without re-deriving it
// from the type it sent.
type pluginAddResult struct {
	Type       string           `json:"type"`
	Identity   string           `json:"identity,omitempty"`
	URI        string           `json:"uri,omitempty"`
	Capability string           `json:"capability,omitempty"`
	Reference  string           `json:"reference,omitempty"`
	Scope      pluginWriteScope `json:"scope"`
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
func (s *Server) dispatchPluginAdd(ctx context.Context, req *jsonRPCRequest) error {
	var params pluginAddParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
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
	if params.Role == "" && strings.EqualFold(params.Type, "governance") {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin add: the governance type requires role, naming the attachment role (label-transform, ir-verdict, or render-verdict)")
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

	res, err := pluginops.Add(ctx, s.resolvedWorkspace, pluginops.AddOptions{
		Socket:             params.Type,
		Query:              params.Query,
		Capability:         params.Capability,
		Role:               params.Role,
		Global:             params.Global,
		Workspace:          params.Workspace,
		AcceptCapabilities: params.AcceptCapabilities,
		InstallCLIPlugin:   s.installCLIPlugin,
		// The install path reports the commands it registered on this writer, and a
		// version substitution is announced on it too. Both are prose for a person
		// watching a terminal; this surface answers with the values instead, which
		// travel on the result.
		Notices: io.Discard,
		In:      strings.NewReader(""),
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, err.Error())
	}
	return s.writeResult(ctx, req.ID, pluginAddResult{
		Type:       res.Socket,
		Identity:   res.Identity,
		URI:        res.URI,
		Capability: res.Capability,
		Reference:  res.Reference,
		Scope:      s.pluginScope(res.ConfigPath, res.ConfigIsMachineWide),
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
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
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
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, err.Error())
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
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
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
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, err.Error())
	}
	plugin, err := s.workspacePath("plugin sync: plugin", params.Plugin)
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, err.Error())
	}

	res, err := pluginops.Sync(ctx, pluginops.SyncOptions{EntityPath: entity, PluginPath: plugin})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, err.Error())
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
// report hands the generated content back instead of writing it. It defaults to
// false, which writes — the same default the command line takes — so a caller
// that says nothing gets the same behaviour on both surfaces.
//
// outputDir is required under both deliveries. Under report nothing is written,
// but each reported document still names the file it is for, and that path is
// composed from this directory: reporting changes where the content goes, not
// which file it is content for.
type pluginGenCatalogParams struct {
	FromEmbedded bool   `json:"fromEmbedded"`
	Plugin       string `json:"plugin"`
	OutputDir    string `json:"outputDir"`
	Overlay      string `json:"overlay"`
	Report       bool   `json:"report"`
}

// pluginGenCatalogResult is the aggrega/pluginGenCatalog response.
//
// Exactly one half is filled, and which one says which mode ran. Documents
// carries the generated entities and the file each is for, and appears only in
// report mode — where nothing was created or modified, so a client puts the
// content in its own document model, where it is visible before it is saved and
// can be amended or undone. Notices carries what the generation said about the
// writes it performed, and appears only when it wrote.
type pluginGenCatalogResult struct {
	Documents []pluginGeneratedDocument `json:"documents,omitempty"`
	Notices   string                    `json:"notices,omitempty"`
	Reported  bool                      `json:"reported"`
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
// Regenerating over unchanged inputs yields byte-identical files, so a client may
// run it freely without producing a diff it has to explain.
func (s *Server) dispatchPluginGenCatalog(ctx context.Context, req *jsonRPCRequest) error {
	var params pluginGenCatalogParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
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
	// directory — so a report without it would answer with documents belonging to
	// nowhere.
	if params.OutputDir == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"plugin gen-catalog: outputDir is required (the directory the generated entities are written under, "+
				"and — under report — the directory each reported document names as its own)")
	}

	opts := pluginops.GenCatalogOptions{
		FromEmbedded: params.FromEmbedded,
		Report:       params.Report,
	}
	for _, f := range []struct {
		what string
		raw  string
		dst  *string
	}{
		{"plugin gen-catalog: plugin", params.Plugin, &opts.PluginPath},
		{"plugin gen-catalog: outputDir", params.OutputDir, &opts.OutputDir},
		{"plugin gen-catalog: overlay", params.Overlay, &opts.OverlayPath},
	} {
		if f.raw == "" {
			continue
		}
		resolved, err := s.workspacePath(f.what, f.raw)
		if err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, err.Error())
		}
		*f.dst = resolved
	}

	// The generation's prose goes nowhere: this surface answers with the values,
	// and the result carries whatever the generation said.
	res, err := pluginops.GenCatalog(ctx, opts, nil)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, err.Error())
	}
	return s.writeResult(ctx, req.ID, pluginGenCatalogResult{
		Documents: generatedDocuments(res.Documents),
		Notices:   res.Notices,
		Reported:  params.Report,
	})
}
