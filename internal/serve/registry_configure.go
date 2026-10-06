// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
)

// configWriteScope states how far a write to a configuration file reached.
//
// Every response whose operation resolved a configuration write target carries
// one, and it is the whole answer to a question a caller cannot otherwise ask: a
// request names one workspace, so an answer with no scope on it reads as
// being about that workspace — and for an operation that defaults to the global
// file that reading is wrong. Registering a registry and seeding a configuration
// both default there, because a registry and the identities trusted on it are
// machine-wide tooling, so the reach is stated rather than left to be inferred
// from the absence of a flag the caller did not send.
//
// MachineWide is derived from the file the write actually resolved to, not from
// the request, so a response cannot describe a machine-wide change as a local
// one by consulting the wrong input.
type configWriteScope struct {
	// ConfigFile is the configuration file the write landed in.
	ConfigFile string `json:"configFile"`
	// MachineWide is true when that file is the global one, which every workspace
	// on this machine resolves through.
	MachineWide bool `json:"machineWide"`
	// WorkspaceRoot is the workspace this session was opened over, so a caller
	// reading a workspace-scoped write knows which workspace it was.
	WorkspaceRoot string `json:"workspaceRoot"`
}

// scopeOfWriteTarget projects the shared layer's write target onto the wire
// shape, adding the workspace this session holds.
func (s *Server) scopeOfWriteTarget(t registryops.WriteTarget) configWriteScope {
	return configWriteScope{
		ConfigFile:    t.Path,
		MachineWide:   t.Scope == registryops.ScopeMachine,
		WorkspaceRoot: s.workspaceRoot,
	}
}

// registryAddParams is what aggrega/registryAdd takes.
//
// The spellings are the ones the command line uses, so a caller that learned the
// options on one surface knows them on the other. Every field except host is
// optional and narrows or shapes the trust policy; `global` and `workspace` are
// the write-target overrides, and sending both is refused exactly as the command
// line refuses it.
//
// keyPath and trustedRoot name files: an absolute value is one a person picked,
// read where it names; a relative one is a file inside the workspace this session
// opened, resolved against its root and held to it (pickedOrWorkspacePath).
//
// fetchTrust is a pointer so an absent key and an explicit false are
// distinguishable. The command line's flag defaults to true, and a request that
// says nothing about the fetch must get that same default rather than silently
// getting the zero value, which would turn every unqualified registration into
// one that pins nothing.
type registryAddParams struct {
	Host              string   `json:"host"`
	Name              string   `json:"name"`
	Identities        []string `json:"identities"`
	KeyPath           string   `json:"keyPath"`
	TrustedRoot       string   `json:"trustedRoot"`
	RequireSignatures *bool    `json:"requireSignatures"`
	Advisory          bool     `json:"advisory"`
	FetchTrust        *bool    `json:"fetchTrust"`
	Global            bool     `json:"global"`
	Workspace         bool     `json:"workspace"`
}

// registryAddResult is the aggrega/registryAdd response.
//
// Trust reports what the advertised-trust fetch did, as distinguished outcomes
// rather than as a line of prose: pinned, not advertised, the built-in baseline
// pinned, or skipped — and, beside not advertised, whether the registry could
// not be reached at all. A caller deciding whether the entry it just wrote is
// usable needs to know which, and a registry that advertises nothing is not a
// failure.
// The value is the shared registration's own, carried unchanged, so the answer
// reports exactly the outcome the command line prints its notice from.
type registryAddResult struct {
	Host  string                        `json:"host"`
	Scope configWriteScope              `json:"scope"`
	Trust registryops.TrustFetchOutcome `json:"trust"`
}

// registryRemoveParams is what aggrega/registryRemove takes: the registry
// addressed by host or friendly alias, with the same write-target overrides a
// registration takes.
type registryRemoveParams struct {
	Registry  string `json:"registry"`
	Global    bool   `json:"global"`
	Workspace bool   `json:"workspace"`
}

// registryRemoveResult is the aggrega/registryRemove response.
type registryRemoveResult struct {
	Host  string           `json:"host"`
	Scope configWriteScope `json:"scope"`
}

// registryUpdateParams narrows an index refresh. An absent registry refreshes
// every configured one; naming one refreshes only that registry and makes a
// failure there loud rather than noted.
type registryUpdateParams struct {
	Registry string `json:"registry"`
	Upgrade  bool   `json:"upgrade"`
}

// registryUpdateResult is the aggrega/registryUpdate response.
//
// It is the one registry method here that walks registries, and the one whose
// write is workspace-local: IndexRoot names the directory under this workspace's
// own configured cache the indexes were written to, which is what says the
// refresh changed nothing another project on the machine resolves through.
//
// Each host carries what its walk found, including the repositories it could not
// read and the deprecation markers it met. A caller shown only a record count
// would read a partial walk as a complete one.
type registryUpdateResult struct {
	Hosts                []registryops.HostUpdate `json:"hosts"`
	Failed               []string                 `json:"failed"`
	IndexRoot            string                   `json:"indexRoot"`
	RegistriesConfigured bool                     `json:"registriesConfigured"`
	// ReachedRegistries is stated because it is the property that separates this
	// method from every other registry method here: the two inventories answer
	// from configuration and from an offline snapshot, and this one goes out to
	// each registry. It is therefore true on every answer; whether a given
	// registry was in fact reached is that host's own Unreachable.
	ReachedRegistries bool `json:"reachedRegistries"`
}

// dispatchRegistryAdd registers a registry and its signature trust policy.
//
// 🔴 It WRITES, and by default the file it writes is the global one every
// workspace on this machine resolves through — so a registration a session
// performs changes what resolves for projects this session has never heard of.
// The response says so on every answer rather than only where the caller asked
// for the global file, because the default is the machine-wide one and a caller
// that sent no scope flag is exactly the caller most likely to assume otherwise.
//
// It is carried here on the same rule every other writing method runs under: a
// caller that sent this has asked for the registration, exactly as a caller
// sending a build has asked for artifacts. What stays out of bounds is a write
// performed while validating or inspecting, where nobody asked, and this is not
// that.
//
// Where the registration takes the registry's advertised trust policy it makes
// one unauthenticated request to that registry's own origin, over the client
// the shared registration takes from the network-egress gate — the one the
// command line's registration takes — so an offline workspace refuses it in the
// same sentence. Nothing is prompted for: a registry that advertises nothing is
// reported as advertising nothing and the registration proceeds on what the
// request supplied. Offline, a registry other than the official hub is refused
// unless the request supplies identities or a key, in the sentence the command
// line prints, with the condition's short code ahead of it; the hub is written on
// its built-in baseline, answered as the baseline pinned with the offline refusal
// as the reason.
func (s *Server) dispatchRegistryAdd(ctx context.Context, req *jsonRPCRequest) error {
	var params registryAddParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.Host == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"registry add: host is required (the registry to register, such as registry.example.com)")
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to resolve a registry write target against")
	}

	// The key file and the trust root are files a person may have picked in a
	// dialog, or that the request names inside the workspace: a relative value is
	// anchored to the workspace root and held to it, and an absolute one is read
	// where it names. The trust root is recorded as resolved, so the entry names the
	// file the request named whichever directory later reads it.
	keyPath, err := s.pickedOrWorkspacePath("registry add: keyPath", params.KeyPath)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
	}
	trustedRoot, err := s.pickedOrWorkspacePath("registry add: trustedRoot", params.TrustedRoot)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
	}

	// An absent fetchTrust takes the command line's own default rather than the
	// zero value, so an unqualified registration pins the advertised policy on
	// both surfaces.
	fetchTrust := true
	if params.FetchTrust != nil {
		fetchTrust = *params.FetchTrust
	}
	opts := registryops.AddOptions{
		Host:        params.Host,
		Name:        params.Name,
		Identities:  params.Identities,
		KeyPath:     keyPath,
		TrustedRoot: trustedRoot,
		Advisory:    params.Advisory,
		FetchTrust:  fetchTrust,
		Global:      params.Global,
		Workspace:   params.Workspace,
	}
	if params.RequireSignatures != nil {
		opts.RequireSignatures = true
		opts.RequireValue = *params.RequireSignatures
	}

	res, err := registryops.Add(ctx, s.resolvedWorkspace, opts)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	// projection-omits internal/registryops.AddResult.Content: the registration
	// fills that document only under a reporting delivery, and this method takes
	// no delivery — its request declares none and the options built above set
	// none — so the delivery is always the write and the field is empty on every
	// answer this method can give. Carrying it would put a key on the wire that
	// no request can populate, which reads as a capability rather than as the
	// absence of one.
	return s.writeResult(ctx, req.ID, registryAddResult{
		Host:  res.Host,
		Scope: s.scopeOfWriteTarget(res.Target),
		Trust: res.Trust,
	})
}

// dispatchRegistryRemove drops a registry's entry from the configuration.
//
// 🔴 It WRITES, and it is the sharpest of these: removing an entry changes what
// every project on this machine resolves through, and re-adding it is not the
// same act — the trust policy that was pinned is gone, and a later registration
// pins whatever the registry advertises then. The response names the file it
// removed from and whether that file is the machine-wide one.
//
// A token that matches nothing fails rather than reporting success over an
// unchanged configuration, so a caller is never told the machine's resolution
// changed when it did not.
func (s *Server) dispatchRegistryRemove(ctx context.Context, req *jsonRPCRequest) error {
	var params registryRemoveParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.Registry == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"registry remove: registry is required (the host or friendly alias to remove)")
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to resolve a registry write target against")
	}

	res, err := registryops.Remove(s.resolvedWorkspace, registryops.RemoveOptions{
		Token:     params.Registry,
		Global:    params.Global,
		Workspace: params.Workspace,
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	return s.writeResult(ctx, req.ID, registryRemoveResult{
		Host:  res.Host,
		Scope: s.scopeOfWriteTarget(res.Target),
	})
}

// dispatchRegistryUpdate refreshes the local searchable index of the configured
// registries.
//
// 🔴 It WALKS REGISTRIES — the only method here that does — and it writes what
// it finds under this workspace's own configured cache directory. Those two
// properties pull in opposite directions and the response states both: the reach
// is machine-external, and the write is workspace-local, so a refresh changes
// nothing another project on this machine resolves through.
//
// Nothing is prompted for. Identity resolves from a supplied or ambient
// credential source, and a registry that needs a credential none of them carries
// produces a refusal naming what it wanted rather than a wait nobody can answer.
//
// The walk's running commentary is discarded here rather than folded into the
// response. It is prose written for a person watching a terminal, and this
// surface answers with the facts themselves: what each host indexed, which
// repositories it could not read, and which deprecation markers it met.
//
// Whether a registry was reached is one of those facts, on both shapes of the
// answer: each host that failed carries Unreachable where it could not be
// reached, and a refused request naming one registry carries the same key in
// its error data. The message is the walk's own error — led by its condition's
// short code where it names one, as every refusal naming a condition is — so a
// client reads the key to name the registry as not reached and shows the message
// as the reason.
func (s *Server) dispatchRegistryUpdate(ctx context.Context, req *jsonRPCRequest) error {
	var params registryUpdateParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to refresh a registry index for")
	}

	res, err := registryops.Update(ctx, s.resolvedWorkspace, registryops.UpdateOptions{
		Registry: params.Registry,
		Upgrade:  params.Upgrade,
	}, io.Discard)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil && (params.Registry != "" || len(res.Failed) == 0) {
		// Nothing was answered: the request named one registry and that registry
		// failed, or the token resolved to nothing at all. There is no partial walk
		// to hand back, so the failure is the whole response.
		return s.writeErrorEnvelope(ctx, req.ID, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: err.Error(),
			Data:    registryUpdateRefusalData(params.Registry, res),
			cause:   err,
		})
	}
	// A walk over every configured registry ANSWERS, whether some of them failed or
	// all, rather than reporting a failure that would take the answer with it. The
	// registries refreshed were indexed and their records written; `failed` names
	// the ones that were not, and each host carries its own error. A caller reads
	// `failed` to decide what is missing — the command line reports the same walk
	// as a non-zero exit because a shell has no other channel, and this surface
	// does.
	return s.writeResult(ctx, req.ID, registryUpdateResultOf(res))
}

// registryUpdateRefusalData is the data a refused refresh of one named registry
// carries: the key saying that registry could not be reached, read off the
// walk's own row for it rather than worked out again from the error, so the
// refusal and the row a whole-registry refresh would have answered with cannot
// disagree. The shared walk keeps that row on its result when it returns the
// failure, and a request naming one registry walks exactly one.
//
// Every other refusal carries no data: a registry that answered and failed, a
// token naming no configured registry, an offline workspace, and any refusal of
// a refresh that named no registry.
func registryUpdateRefusalData(named string, res registryops.UpdateResult) *errorData {
	if named == "" || len(res.Hosts) != 1 || !res.Hosts[0].Unreachable {
		return nil
	}
	return &errorData{Unreachable: true}
}

// registryUpdateResultOf projects the shared layer's walk onto the wire shape,
// with every collection an array rather than a null so a client iterates
// unconditionally.
func registryUpdateResultOf(res registryops.UpdateResult) registryUpdateResult {
	hosts := res.Hosts
	if hosts == nil {
		hosts = []registryops.HostUpdate{}
	}
	for i := range hosts {
		if hosts[i].Skipped == nil {
			hosts[i].Skipped = []registryops.SkippedRepo{}
		}
		if hosts[i].Deprecations == nil {
			hosts[i].Deprecations = []registryops.DeprecationNote{}
		}
	}
	failed := res.Failed
	if failed == nil {
		failed = []string{}
	}
	return registryUpdateResult{
		Hosts:                hosts,
		Failed:               failed,
		IndexRoot:            res.IndexRoot,
		RegistriesConfigured: !res.NoRegistriesConfigured,
		ReachedRegistries:    true,
	}
}
