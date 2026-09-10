// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
)

// resolveDependenciesParams is the request shape for
// `aggrega/resolveDependencies`. The release field is a workspace-relative path
// to the Release YAML, under the same contract every Release-naming parameter
// here carries.
//
// Upgrade discards the recorded pins and re-resolves, floating each dependency to
// the highest available version; targets narrows that to the named references,
// holding every other pin. Verify checks the recorded resolution against what
// resolves today and writes nothing at all.
//
// There is no catalog-directory parameter, and its absence is the contract rather
// than an omission. The daemon resolves against the workspace the request named
// and the search stack that workspace configures, so a caller cannot point it at
// a directory the workspace does not name. Naming the workspace is what selects
// that search stack, not a way around it: the stack is the named workspace's own
// configuration, read the way the command line reads it.
type resolveDependenciesParams struct {
	Release string   `json:"release"`
	Upgrade bool     `json:"upgrade,omitempty"`
	Targets []string `json:"targets,omitempty"`
	Verify  bool     `json:"verify,omitempty"`
}

// dependencyResolutionReport is what a completed dependency resolution reports
// on this surface, and it is the only projection of that result here.
//
// It reports what was written rather than only that something was: Lockfile is
// the file the run wrote or verified, and the three axes name every reference it
// pinned with the version each resolved to. A caller that asked for a resolution
// is asking exactly that question, and answering with a bare acknowledgement
// would leave it to re-read the file to find out.
//
// Verified reports that the run only compared and wrote nothing, and Toolchain
// then carries what the recorded file says produced it — reported for audit and
// never compared against this binary.
//
// It is one type rather than one per request because a resolution reached as a
// build's own step and a resolution asked for directly are the same resolution,
// so a caller that can read one report can read the other. Two shapes assembled
// separately would agree until they did not.
type dependencyResolutionReport struct {
	Lockfile        string           `json:"lockfile"`
	Verified        bool             `json:"verified,omitempty"`
	Entities        []ClosureEntity  `json:"entities"`
	Libraries       []ClosureEntity  `json:"libraries"`
	Plugins         []ClosureEntity  `json:"plugins"`
	SourceArtifacts int              `json:"sourceArtifacts"`
	TrustedPlugins  int              `json:"trustedPlugins"`
	Toolchain       *toolchainReport `json:"toolchain,omitempty"`

	// ExternalInputs is how many external inputs the lockfile pins after the
	// run, across the ones carried forward and the ones the run discovered.
	ExternalInputs int `json:"externalInputs"`

	// IncompleteExternalInputs is set when the run pinned its dependencies but
	// could not finish discovering which external inputs the Release consumes,
	// and carries what stopped the discovery.
	//
	// It rides on a SUCCESSFUL answer rather than turning the request into a
	// failure, and that is the shape of the outcome rather than a softening of
	// it: the dependency pins are complete and on disk, so reporting the whole
	// request as failed would tell the caller to redo work that is already
	// correct. What is owed instead is saying so, which is what this field is.
	IncompleteExternalInputs string `json:"incompleteExternalInputs,omitempty"`
}

// dependencyResolutionReportOf projects a completed resolution onto the wire.
//
// It is the one place the resolution's result is read, so every request that
// reports a resolution reports the same facts — a build's own fill step included.
// The lockfile is rendered as the caller's own coordinate, relative to the
// workspace root the request named, on the same contract every path here carries.
//
// It takes the workspace root rather than the server because the root is the only
// server state a projection of a resolution needs, and naming it is what keeps
// this a mapping from a result to a wire shape rather than a method with the whole
// session in reach.
func dependencyResolutionReportOf(res pullops.Result, workspaceRoot string) dependencyResolutionReport {
	out := dependencyResolutionReport{
		Lockfile:        workspaceRelative(workspaceRoot, res.LockfilePath),
		Verified:        res.Verified,
		Entities:        closureEntities(res.Closure),
		Libraries:       closureLibraries(res.Closure),
		Plugins:         closurePlugins(res.Closure),
		SourceArtifacts: len(res.Closure.SourceArtifacts),
		TrustedPlugins:  res.TrustedPlugins,
		ExternalInputs:  res.ExternalInputs,
	}
	if res.ExternalInputDiscovery != nil {
		out.IncompleteExternalInputs = res.ExternalInputDiscovery.Error()
	}
	if res.Toolchain != nil {
		out.Toolchain = &toolchainReport{CompilerVersion: res.Toolchain.CompilerVersion}
		for _, p := range res.Toolchain.CorePlugins {
			out.Toolchain.CorePlugins = append(out.Toolchain.CorePlugins,
				toolchainCorePluginRow{Plugin: p.Plugin, Version: p.Version})
		}
	}
	return out
}

// resolveDependenciesResult is the response shape for
// `aggrega/resolveDependencies`: the resolution's report, plus the account it
// wrote of itself.
//
// Notices carry the resolution's own diagnostic lines, in the order it raised
// them: the pin-reuse notices, the signature-verification warnings, the
// withdrawal findings and the retraction skips. They reach no document position,
// so they travel here rather than on the positioned-diagnostic channel.
type resolveDependenciesResult struct {
	dependencyResolutionReport
	Notices []string `json:"notices,omitempty"`
}

// toolchainReport is the toolchain stamp a verified lockfile records: the
// compiler version that wrote the file, and the version of each core plugin that
// compiler carried.
//
// It is reported and never compared against this binary. The toolchain is not
// part of the pinned closure, so a difference between the recorded stamp and the
// running binary is not drift, and checking it here would pin the compiler by
// proxy.
type toolchainReport struct {
	CompilerVersion string                   `json:"compilerVersion"`
	CorePlugins     []toolchainCorePluginRow `json:"corePlugins,omitempty"`
}

// toolchainCorePluginRow is one core plugin's identity and version in the
// recorded stamp.
type toolchainCorePluginRow struct {
	Plugin  string `json:"plugin"`
	Version string `json:"version"`
}

// dispatchResolveDependencies unmarshals params and answers the request.
func (s *Server) dispatchResolveDependencies(ctx context.Context, req *jsonRPCRequest) error {
	var params resolveDependenciesParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	progress := s.progressFor(ctx)
	defer progress.done()
	result, errEnvelope := s.handleResolveDependencies(ctx, params, progress)
	if ctx.Err() != nil {
		// Cancelled mid-resolution (shutdown). Drop the response; the dispatcher
		// exits via the drain timer.
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// handleResolveDependencies resolves the Release's dependencies and reports what
// it pinned.
//
// # Why this method reaches a registry and writes, when the rest of the daemon does not
//
// The daemon's no-write, no-network posture is about inspection. Every request
// that answers a question about a workspace answers it from the workspace and
// what is already cached, because reaching the network or changing the workspace
// to answer a question would surprise a caller who only asked. This is not a
// question: a caller sending it has asked for the dependencies to be resolved,
// exactly as a caller sending a build has asked for artifacts.
//
// So the rule that governs is when an operation runs, not which surface runs it:
// a registry-reaching or state-writing operation runs on explicit invocation and
// never as a side effect of validating or inspecting. It is stated over the
// shared layer's whole invocation path rather than per method — see
// [pullops] — and this method is one caller of it.
//
// # What it writes, which is intended and reported
//
// The per-Release lockfile, the content-addressed cache, the trust record beside
// the lockfile, and the resolved content digests frozen into each writable recipe
// file. Every one of those is the operation's product rather than a side effect,
// and what this surface owes is that a caller can see it happened: the result
// names the lockfile and every reference pinned into it.
//
// # It never prompts
//
// Nobody is watching a protocol stream, so the shared layer is given no way to
// ask a person anything: identity resolves from a supplied or ambient credential
// source, and a fork that would have been put to a person halts loudly naming
// what to supply. A hang would be the observable failure otherwise, and a caller
// on this surface has no way to answer.
// # It reports what it is doing while it does it
//
// The resolution writes its own account of itself as it works — which pin was
// reused, which signature was checked, which version was skipped — and that
// stream is what fills the answer's notices. A request that asked to be reported
// on gets each of those lines as it is raised, on the progress channel, in the
// resolution's own words. Nothing is composed here: the answer's notices and the
// reported lines are one stream read twice, so the two cannot disagree.
func (s *Server) handleResolveDependencies(ctx context.Context, params resolveDependenciesParams, progress *progressRun) (resolveDependenciesResult, *jsonRPCError) {
	releasePath, paramErr := resolveReleaseParam(s.workspaceRoot, params.Release)
	if paramErr != nil {
		return resolveDependenciesResult{}, paramErr
	}
	progress.begin("Resolving dependencies")

	var notices bytes.Buffer
	res, err := pullops.Run(pullops.Config{
		Workspace:       s.resolvedWorkspace,
		ReleasePath:     releasePath,
		Upgrade:         params.Upgrade,
		UpgradeTargets:  params.Targets,
		Verify:          params.Verify,
		CompilerVersion: s.compilerVersion,
		Toolchain:       s.toolchain,
		DiagOut:         io.MultiWriter(&notices, progress.writer()),
		// No prompt is supplied, and that is the posture rather than an omission:
		// see the method documentation above.
	})
	if err != nil {
		return resolveDependenciesResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: err.Error(),
			cause:   err,
		}
	}

	return resolveDependenciesResult{
		dependencyResolutionReport: dependencyResolutionReportOf(res, s.workspaceRoot),
		Notices:                    noticeLines(notices.String()),
	}, nil
}

// noticeLines splits a captured diagnostic stream into the lines a client
// reports, dropping the leading indentation the stream uses to nest a notice
// under the step that raised it and dropping empty lines.
//
// The indentation is a terminal layout: it groups notices visually under a phase
// a reader can see. A protocol caller has no such phase display, so carrying the
// spaces would hand it whitespace to strip rather than a message to show.
func noticeLines(stream string) []string {
	var out []string
	for _, line := range strings.Split(stream, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// workspaceRelative renders a path the daemon wrote as the caller's own
// coordinate: relative to the workspace root, which is the form every
// Release-naming parameter uses.
//
// A path outside the root, and a request carrying no root, are returned as they
// are. Neither can arise for a file this daemon wrote — every write lands inside
// the workspace the request named — so the fallback exists to keep the answer
// honest rather than to serve a case any request can reach.
func workspaceRelative(workspaceRoot, path string) string {
	if workspaceRoot == "" {
		return path
	}
	rel, err := filepath.Rel(workspaceRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}
