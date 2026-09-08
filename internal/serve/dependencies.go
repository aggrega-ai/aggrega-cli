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

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
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

// resolveDependenciesResult is the response shape for
// `aggrega/resolveDependencies`.
//
// It reports what was written rather than only that something was: Lockfile is
// the file this run wrote or verified, and the three axes name every reference it
// pinned with the version each resolved to. A caller that asked for a resolution
// is asking exactly that question, and answering with a bare acknowledgement
// would leave it to re-read the file to find out.
//
// Verified reports that the run only compared and wrote nothing, and Toolchain
// then carries what the recorded file says produced it — reported for audit and
// never compared against this binary.
//
// Notices carry the resolution's own diagnostic lines, in the order it raised
// them: the pin-reuse notices, the signature-verification warnings, the
// withdrawal findings and the retraction skips. They reach no document position,
// so they travel here rather than on the positioned-diagnostic channel.
type resolveDependenciesResult struct {
	Lockfile        string           `json:"lockfile"`
	Verified        bool             `json:"verified,omitempty"`
	Entities        []ClosureEntity  `json:"entities"`
	Libraries       []ClosureEntity  `json:"libraries"`
	Plugins         []ClosureEntity  `json:"plugins"`
	SourceArtifacts int              `json:"sourceArtifacts"`
	TrustedPlugins  int              `json:"trustedPlugins"`
	Toolchain       *toolchainReport `json:"toolchain,omitempty"`
	Notices         []string         `json:"notices,omitempty"`
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

// acquireExternalInputsParams is the request shape for
// `aggrega/acquireExternalInputs`: the Release whose render's external inputs are
// pinned, under the same workspace-relative contract every Release-naming
// parameter here carries.
type acquireExternalInputsParams struct {
	Release string `json:"release"`
}

// acquireExternalInputsResult is the response shape for
// `aggrega/acquireExternalInputs`.
//
// Pinned names every external input this run newly pinned, in the order the
// render requested them, and is empty when the run pinned nothing — which is the
// ordinary outcome for a Release whose inputs are already recorded. The pins are
// already merged into the lockfile the operation names, so reporting them is what
// tells a caller which of its inputs moved.
type acquireExternalInputsResult struct {
	Lockfile string                `json:"lockfile"`
	Pinned   []pinnedExternalInput `json:"pinned"`
	Notices  []string              `json:"notices,omitempty"`
}

// pinnedExternalInput is one newly-pinned external input: the URL the render
// requested, the content digest it was pinned at, and the size of the bytes
// stored.
//
// SizeText is that size written in the binary-unit vocabulary every surface
// spells sizes in, carried beside the number rather than instead of it, so a
// caller displaying what was pinned does not have to reproduce the rounding rule
// to spell it the way the rest of the product does.
type pinnedExternalInput struct {
	URL      string `json:"url"`
	Sha256   string `json:"sha256"`
	Size     int64  `json:"size"`
	SizeText string `json:"sizeText"`
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

	out := resolveDependenciesResult{
		Lockfile:        s.workspaceRelative(res.LockfilePath),
		Verified:        res.Verified,
		Entities:        closureEntities(res.Closure),
		Libraries:       closureLibraries(res.Closure),
		Plugins:         closurePlugins(res.Closure),
		SourceArtifacts: len(res.Closure.SourceArtifacts),
		TrustedPlugins:  res.TrustedPlugins,
		Notices:         noticeLines(notices.String()),
	}
	if res.Toolchain != nil {
		out.Toolchain = &toolchainReport{CompilerVersion: res.Toolchain.CompilerVersion}
		for _, p := range res.Toolchain.CorePlugins {
			out.Toolchain.CorePlugins = append(out.Toolchain.CorePlugins,
				toolchainCorePluginRow{Plugin: p.Plugin, Version: p.Version})
		}
	}
	return out, nil
}

// dispatchAcquireExternalInputs unmarshals params and answers the request.
func (s *Server) dispatchAcquireExternalInputs(ctx context.Context, req *jsonRPCRequest) error {
	var params acquireExternalInputsParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	progress := s.progressFor(ctx)
	defer progress.done()
	result, errEnvelope := s.handleAcquireExternalInputs(ctx, params, progress)
	if ctx.Err() != nil {
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// handleAcquireExternalInputs runs the Release's render in acquisition mode, so
// every external input the render requests is fetched, pinned into the
// content-addressed store and recorded in the lockfile, and reports the pins this
// run added.
//
// It reaches the network and writes for the same reason the resolution above
// does, under the same rule: a caller sending this has asked for the inputs to be
// pinned. The rendered artifacts are discarded — the shared operation points the
// render at a temporary directory it removes — so the workspace's own output tree
// is untouched by an acquisition, on this surface exactly as on the command line.
//
// The dependency fill the run may need is the resolution operation itself,
// supplied here rather than reimplemented: a build that fills its own lockfile
// runs the same resolution every other surface runs, so a workspace configured to
// fill automatically behaves identically whichever surface asked. The fill is
// told that this run's own render acquires, so it resolves the dependencies and
// leaves the external inputs to that render rather than rendering the Release a
// second time to discover them.
// It reports what it is doing while it does it, on the same terms the
// resolution above does: the acquisition's own account of itself reaches a
// request that asked to be reported on, and the answer's notices are the same
// stream read a second time.
func (s *Server) handleAcquireExternalInputs(ctx context.Context, params acquireExternalInputsParams, progress *progressRun) (acquireExternalInputsResult, *jsonRPCError) {
	releasePath, paramErr := resolveReleaseParam(s.workspaceRoot, params.Release)
	if paramErr != nil {
		return acquireExternalInputsResult{}, paramErr
	}
	progress.begin("Acquiring external inputs")

	// The options this acquisition's own render runs under, named here rather than
	// left implicit, because the fill below has to be told whether that render
	// acquires and the answer is read from the one place that decides it.
	runOpts := buildops.RunOptions{}

	var notices bytes.Buffer
	acquired, err := buildops.Acquire(buildops.Config{
		Workspace:       s.resolvedWorkspace,
		ReleasePath:     releasePath,
		CompilerVersion: s.compilerVersion,
		Options:         runOpts,
		DiagOut:         io.MultiWriter(&notices, progress.writer()),
		Out:             io.Discard,
		Pull: func(upgrade, _ bool, pullDiagOut, _ io.Writer) error {
			_, perr := pullops.Run(pullops.Config{
				Workspace:       s.resolvedWorkspace,
				ReleasePath:     releasePath,
				Upgrade:         upgrade,
				CompilerVersion: s.compilerVersion,
				Toolchain:       s.toolchain,
				DiagOut:         pullDiagOut,
				// Where this run's own render acquires, the fill stands its
				// external-input discovery render down: the render immediately after
				// it reaches the same references and pins them, so discovering here
				// would render this Release twice and leave that render nothing to
				// pin. Where a render is frozen instead, the fill is the only thing
				// that can pin what it will ask for, so it discovers.
				CallerRendersAndAcquires: runOpts.AcquiresDuringRender(),
			})
			return perr
		},
	})
	if err != nil {
		return acquireExternalInputsResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: err.Error(),
			cause:   err,
		}
	}

	pinned := make([]pinnedExternalInput, 0, len(acquired))
	for _, rec := range acquired {
		pinned = append(pinned, pinnedExternalInput{
			URL:      rec.URL,
			Sha256:   rec.Sha256,
			Size:     rec.Size,
			SizeText: cacheops.HumanSize(rec.Size),
		})
	}
	return acquireExternalInputsResult{
		Lockfile: s.workspaceRelative(lockfile.PathFor(releasePath)),
		Pinned:   pinned,
		Notices:  noticeLines(notices.String()),
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
func (s *Server) workspaceRelative(path string) string {
	if s.workspaceRoot == "" {
		return path
	}
	rel, err := filepath.Rel(s.workspaceRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}
