// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// pullArgs is the pull tool's argument shape.
//
// There is deliberately no argument that re-resolves every pin at once, and its
// absence is the contract rather than an omission. That form discards the whole
// lockfile and re-resolves from current registry state; the command line gates it
// behind a person who can be asked, and a tool call has nobody to ask. Naming the
// targets is therefore the only upgrade this surface can express, so the wholesale
// form cannot be composed here rather than being accepted and then refused.
//
// UpgradeTargets doubles as the switch and the subject: a resolution upgrades
// exactly the references it names, so there is no second flag beside it that could
// disagree with it about whether an upgrade was asked for.
type pullArgs struct {
	Release        string   `json:"release"`
	WorkspaceRoot  string   `json:"workspace_root"`
	CatalogDirs    []string `json:"catalog_dirs"`
	Verify         bool     `json:"verify"`
	UpgradeTargets []string `json:"upgrade_targets"`
}

// pullCorePlugin is one core plugin's recorded version in a lockfile's toolchain
// stamp.
type pullCorePlugin struct {
	Plugin  string `json:"plugin"`
	Version string `json:"version"`
}

// pullToolchain is the toolchain a recorded lockfile says produced it: the
// compiler version that wrote the file, and the version of each core plugin that
// compiler carried.
//
// It is reported and never compared against this binary. The toolchain is not part
// of the pinned dependency set, so a difference between the recorded stamp and the
// running binary is not drift, and checking it here would pin the compiler by
// proxy.
type pullToolchain struct {
	CompilerVersion string           `json:"compiler_version"`
	CorePlugins     []pullCorePlugin `json:"core_plugins,omitempty"`
}

// pullResultPayload is the pull tool's structured payload.
//
// It reports what was resolved rather than only that something was: Lockfile is
// the file the run wrote or compared against, and Resolved names every reference
// it reached with the version each resolved to. A caller that asked for a
// resolution is asking exactly that, and a bare acknowledgement would leave it to
// read the file back to find out.
//
// # It is returned on a refusal too
//
// A refused run answers in this same shape, with the failure flagged on the result
// envelope rather than by a field here. Resolved is populated whenever the run got
// far enough to compute the resolution — which includes every refusal past that
// point, a drift verdict among them — because a refusal is not a reason to drop to
// a lower fidelity, and the caller holding the refusal is the one that has to act
// on the detail. That is why the fields below are filled before the verdict is read
// rather than after it.
//
// The counts are zero on a refusal, and they are NOT a signal a caller may branch
// on: they carry no omit option, so they are encoded whatever the outcome, and a
// zero therefore cannot distinguish a run that refused from one that completed and
// pinned none. What distinguishes them is the failure flag on the envelope, which
// is the protocol's own discriminator and not something this shape restates.
type pullResultPayload struct {
	Release string `json:"release"`

	// Lockfile is the per-Release lockfile this call wrote, or compared against.
	// It is derived before the run and so is reported on a refusal as well, since a
	// verify that refuses on drift is a caller asking which file drifted.
	Lockfile string `json:"lockfile"`

	// Verified reports that the run only compared and wrote nothing at all.
	Verified bool `json:"verified,omitempty"`

	// Resolved names every reference the resolution reached, across the catalog
	// entities, the shared documents and the guests, in the order the resolution
	// produced them. It is always an array, empty rather than absent, so a client
	// iterates it without a null check.
	Resolved []closureRef `json:"resolved"`

	SourceArtifacts int `json:"source_artifacts"`
	ExternalInputs  int `json:"external_inputs"`
	TrustedPlugins  int `json:"trusted_plugins"`

	// Toolchain is what the recorded lockfile says produced it, filled only by a
	// comparison run — which is the call that has the file loaded and is the one a
	// caller reaches to find out what a lockfile says.
	Toolchain *pullToolchain `json:"toolchain,omitempty"`

	// Diagnostics carries the resolution's own account of itself, in the order it
	// raised each line: which pin was reused, which signature was checked, which
	// version was skipped as withdrawn. A caller with no terminal has no other way
	// to receive them, and a finding nobody is told about is a finding that was not
	// made.
	Diagnostics []string `json:"diagnostics,omitempty"`

	// IncompleteExternalInputs is set when the run pinned its dependencies but
	// could not finish discovering which external inputs the Release consumes.
	//
	// It rides on a SUCCESSFUL result rather than turning the call into a failure,
	// and that is the shape of the outcome rather than a softening of it: the
	// dependency pins are complete and on disk, so reporting the whole call as
	// failed would tell the caller to redo work that is already correct. What is
	// owed instead is saying so, which is what this field is.
	IncompleteExternalInputs string `json:"incomplete_external_inputs,omitempty"`
}

var pullToolSpec = toolSpec{
	kinds: []string{"release"},
	description: "Resolve a Release's catalog and source dependencies into the local cache and write its lockfile — it " +
		"changes your workspace and reaches the network. Pass the Release path as `release`. By default " +
		"it reconciles: a recorded pin is honoured and fetched at that version, an unpinned reference is resolved fresh, a " +
		"reference no longer reached is pruned, and an unchanged dependency set rewrites nothing. Call it when a build " +
		"reports a drifted lockfile, or declines an external reference the lockfile does not pin — a build renders only " +
		"from pins the workspace already carries, and this is what makes them exist. Pass `verify` to compare the recorded " +
		"lockfile against what resolves today and write nothing, which is how you detect drift. Pass `upgrade_targets` " +
		"with entity URIs to re-resolve only those, holding every other pin; no argument re-resolves everything at once, " +
		"because that discards the whole lockfile and a tool call has nobody to confirm it. Optional `workspace_root` and " +
		"`catalog_dirs` override discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "Path to the Release YAML whose dependencies to resolve, absolute or relative to the workspace root."
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
    "verify": {
      "type": "boolean",
      "description": "Compare the recorded lockfile against the current resolution and write nothing. Drift, and a missing lockfile, are failures. Cannot be combined with upgrade_targets."
    },
    "upgrade_targets": {
      "type": "array",
      "items": { "type": "string", "minLength": 1 },
      "minItems": 1,
      "description": "Entity URIs to re-resolve, in the bare authored form such as core/database. Each named pin is dropped and re-resolved to the highest available version; all other pins and the recorded external inputs are held. Omit to reconcile instead. Cannot be combined with verify."
    }
  },
  "required": ["release"],
  "anyOf": [
    {"not":{"required":["upgrade_targets"]}},
    {"properties":{"verify":{"const":false}}}
  ]
}`,
	invoke: pullInvoke,
}

// pullInvoke resolves the Release's dependencies and reports what it pinned.
//
// # Why this tool reaches a registry and writes, when the rest of this surface does not
//
// Every other tool here answers a question about a workspace from that workspace
// and what is already cached, because reaching the network or changing the
// workspace to answer a question would surprise a caller that only asked. This is
// not a question: a caller invoking it has asked for the dependencies to be
// resolved, exactly as a caller invoking the build tool has asked for artifacts.
// So what governs is when the operation runs, not which surface runs it — it runs
// on explicit invocation and never as a side effect of validating or inspecting.
//
// # It never prompts, and a withdrawal reaches the work
//
// Nobody is watching a protocol stream, so the shared resolution is given no way to
// ask a person anything: a fork that would have been put to a person halts loudly
// naming what to supply, rather than blocking on an answer that will never come. A
// hang would otherwise be the observable failure, and a caller here has no way to
// answer. For the same reason no progress surface is attached — there is no terminal
// to draw one on — and the diagnostics are captured into the answer instead of
// being written to a process stream, which on this server carries framed protocol
// messages and nothing else.
//
// The invocation's context travels into the resolution, so a caller that withdraws
// stops the work rather than only the reply: no lockfile is written, and whatever
// the lockfile already pinned is left exactly as it was. What this transport's tool
// dispatch supplies is a context nothing cancels, so what is guaranteed here is
// what the resolution does with a withdrawal it is handed — not that this surface
// raises one.
func pullInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a pullArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("pull", err)
		}
	}
	release := strings.TrimSpace(a.Release)
	if release == "" {
		return errorResult("pull requires a 'release' path naming the Release whose dependencies to resolve")
	}
	// An 'upgrade_targets' sent as an empty list asks for nothing to be upgraded.
	// It is refused rather than read as the reconciling default, because the two are
	// different requests and silently treating one as the other would answer a call
	// the caller did not make. Absent and empty are distinguishable here: the
	// decoder leaves the field unset for an absent key.
	if a.UpgradeTargets != nil && len(a.UpgradeTargets) == 0 {
		return errorResult("pull was given an empty 'upgrade_targets' list, which names nothing to upgrade. " +
			"Name at least one entity URI, or omit 'upgrade_targets' to reconcile the lockfile against the " +
			"current dependency resolution.")
	}
	targets := make([]string, 0, len(a.UpgradeTargets))
	for _, t := range a.UpgradeTargets {
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			return errorResult("pull was given a blank entry in 'upgrade_targets'. Every entry names an entity " +
				"URI to re-resolve, in the bare authored form such as core/database.")
		}
		targets = append(targets, trimmed)
	}
	if a.Verify && len(targets) > 0 {
		return errorResult("pull cannot combine 'verify' with 'upgrade_targets': verifying compares the recorded " +
			"lockfile and writes nothing, while upgrading re-resolves pins and rewrites it. Send 'verify' on its " +
			"own to find out whether the lockfile has drifted, or 'upgrade_targets' on its own to re-resolve the " +
			"references you name.")
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError(
			"Could not resolve the workspace for the dependency resolution.", err, "release"))
	}
	releasePath := tw.absWorkspacePath(release)

	// The payload is assembled around the run rather than after it, so a refusal
	// past the resolution point carries what the run had computed. The lockfile path
	// is derived through the same rule the resolution derives it by, so the file this
	// answer names is the file the run acted on.
	payload := pullResultPayload{
		Release:  releasePath,
		Lockfile: lockfile.PathFor(releasePath),
		Resolved: []closureRef{},
	}

	// The diagnostics are captured rather than written through: stdout on this
	// server carries framed protocol messages only, so a notice reaching a process
	// stream would corrupt what the client is parsing.
	var diagnostics bytes.Buffer
	// The resolution is taken from the run's own report of it, which fires before
	// the compare-or-write split and is therefore populated on a refused run as well
	// as a completed one. The returned result carries the same value on the paths
	// that reach a return, so reading it here loses nothing and gains every refusal
	// past that point.
	var resolved closure.Closure
	res, runErr := pullops.Run(ctx, pullResolutionConfig(s, tw, releasePath, a, targets, &diagnostics,
		pullops.Reporter{
			Resolved: func(cl closure.Closure) { resolved = cl },
		}))
	payload.Resolved = closureRefsAcrossAxes(resolved)
	payload.Diagnostics = diagnosticLines(diagnostics.String())

	if runErr != nil {
		return structuredErrorResult(
			explainToolError(pullFailureSummary(a.Verify, len(targets)), runErr, "release"), payload)
	}

	payload.Verified = res.Verified
	payload.SourceArtifacts = len(res.Closure.SourceArtifacts)
	payload.ExternalInputs = res.ExternalInputs
	payload.TrustedPlugins = res.TrustedPlugins
	if res.LockfilePath != "" {
		payload.Lockfile = res.LockfilePath
	}
	if res.Toolchain != nil {
		payload.Toolchain = &pullToolchain{CompilerVersion: res.Toolchain.CompilerVersion}
		for _, p := range res.Toolchain.CorePlugins {
			payload.Toolchain.CorePlugins = append(payload.Toolchain.CorePlugins,
				pullCorePlugin{Plugin: p.Plugin, Version: p.Version})
		}
	}
	if res.ExternalInputDiscovery != nil {
		payload.IncompleteExternalInputs = res.ExternalInputDiscovery.Error()
	}
	return textResult(renderPullText(payload), payload)
}

// pullResolutionConfig assembles the resolution's input contract for one decoded
// call: the workspace and Release it named, the arguments it sent, and the sinks
// this transport supplies.
//
// It is a function rather than a literal at the call site because the upgrade
// switch is DERIVED, and a derivation is only worth having if something can
// assert it. That switch is legible nowhere else: it appears in neither the
// answer nor the lockfile on disk, since over a catalog serving one version of
// each entity a refresh and a reconcile resolve to the same pins. Assembling the
// contract here — rather than field by field at the invocation — leaves the
// derivation one home, so a resolution asked to float every pin on a call that
// named no target cannot be composed without editing this.
//
// targets arrives already trimmed and free of blank entries. Rejecting a
// malformed entry stays with the caller, because that refusal answers the caller
// in the protocol's own shape, which this has no way to return.
func pullResolutionConfig(s *server, tw *toolWorkspace, releasePath string, a pullArgs, targets []string,
	diagOut io.Writer, report pullops.Reporter) pullops.Config {
	return pullops.Config{
		Workspace:   tw.rw,
		ReleasePath: releasePath,
		// The caller's directories are handed over unresolved, which is the contract
		// of the field: the resolution prepends them to the workspace's own stack
		// itself, so this call and a command-line one derive the identical search
		// stack from the same arguments under the same configuration.
		CatalogDirs: a.CatalogDirs,
		// Naming targets IS asking for an upgrade, so the switch is derived from the
		// subject rather than carried beside it, and the two cannot disagree.
		Upgrade:         len(targets) > 0,
		UpgradeTargets:  targets,
		Verify:          a.Verify,
		CompilerVersion: s.serverVersion,
		Toolchain:       s.toolchain,
		DiagOut:         diagOut,
		Report:          report,
		// No prompt and no progress surface are supplied, and neither is an omission:
		// see the notes on pullInvoke.
	}
}

// pullFailureSummary is the caller-facing sentence for a refused resolution,
// chosen by what the call asked for rather than by matching the failure's text.
//
// Each one names what the call was doing when it stopped, because a caller with no
// terminal has only the sentence to route on: a comparison that refused is a
// finding about the lockfile, while a write that refused leaves the caller asking
// what state its workspace is in.
func pullFailureSummary(verify bool, targets int) string {
	switch {
	case verify:
		return "The lockfile does not match the Release's current dependency resolution, or there is no " +
			"lockfile to compare against. Nothing was written."
	case targets > 0:
		return "The dependency resolution refused while re-resolving the references you named."
	default:
		return "The dependency resolution refused, so the lockfile is unchanged."
	}
}

// diagnosticLines splits a captured diagnostic stream into the lines a caller
// reports, dropping the leading indentation the stream uses to nest a notice under
// the step that raised it, and dropping empty lines.
//
// The indentation is a terminal layout: it groups notices visually under a phase a
// reader can see. A caller here has no such display, so carrying the spaces would
// hand it whitespace to strip rather than a message to show.
func diagnosticLines(stream string) []string {
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

// renderPullText renders the human-readable summary: what the call did to the
// lockfile, the counts, and the resolution's own diagnostic lines.
//
// The references themselves are counted rather than listed. A resolution reaches
// every shared document and guest as well as every entity, so listing them inline
// would bury the outcome the reader is looking for — and the caller that wants them
// one by one is reading the structured result, which carries all of them. The
// diagnostics print in full for the opposite reason: each one is a finding about a
// specific reference, and a count of findings is not a finding.
func renderPullText(p pullResultPayload) string {
	var b strings.Builder
	if p.Verified {
		fmt.Fprintf(&b, "The lockfile %s matches the Release's current dependency resolution (%d reference(s) "+
			"resolved). Nothing was written.\n", p.Lockfile, len(p.Resolved))
		if p.Toolchain != nil {
			fmt.Fprintf(&b, "It records compiler version %s", p.Toolchain.CompilerVersion)
			if n := len(p.Toolchain.CorePlugins); n > 0 {
				fmt.Fprintf(&b, " and %d core plugin version(s)", n)
			}
			b.WriteString(", reported for audit and never compared against this binary.\n")
		}
	} else {
		fmt.Fprintf(&b, "Resolved %d reference(s) and wrote the lockfile %s (source_artifacts=%d, "+
			"external_inputs=%d, trusted_plugins=%d).\n",
			len(p.Resolved), p.Lockfile, p.SourceArtifacts, p.ExternalInputs, p.TrustedPlugins)
	}
	if p.IncompleteExternalInputs != "" {
		fmt.Fprintf(&b, "\nExternal inputs were not fully discovered, so the dependency pins are complete and "+
			"the external-input pins may not be: %s\n", p.IncompleteExternalInputs)
	}
	if len(p.Diagnostics) > 0 {
		fmt.Fprintf(&b, "\nThe resolution reported %d line(s):\n", len(p.Diagnostics))
		for _, d := range p.Diagnostics {
			fmt.Fprintf(&b, "  %s\n", d)
		}
	}
	b.WriteString("\nEvery reference resolved, with the version each resolved to, is in the structured result.")
	return strings.TrimRight(b.String(), "\n")
}
