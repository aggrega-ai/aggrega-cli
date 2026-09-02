// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// buildArgs is the build tool's argument shape.
type buildArgs struct {
	Release        string   `json:"release"`
	WorkspaceRoot  string   `json:"workspace_root"`
	CatalogDirs    []string `json:"catalog_dirs"`
	ResponseFormat string   `json:"response_format"`
}

// buildFile is one rendered artifact in the build result: its output-relative
// path and, in the detailed form, its rendered content.
type buildFile struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

// buildResultPayload is the build tool's structured payload. The render happens
// entirely in memory: Files are the artifacts the pipeline produced, and
// nothing is written to disk.
type buildResultPayload struct {
	Release string      `json:"release"`
	Files   []buildFile `json:"files"`
	// Deprecations names each pinned reference carrying a recorded deprecation
	// marker, from the shared report every surface reads. It is present on a
	// successful build for the same reason the command line prints the notices on
	// one: reporting is unconditional and only halting is configured, so raising
	// the halt threshold never reduces what the caller is told.
	Deprecations []string `json:"deprecations,omitempty"`
}

var buildToolSpec = toolSpec{
	name:  "build",
	kinds: []string{"release"},
	description: "Compile a Release into its rendered deployment artifacts entirely in memory and return them — a " +
		"preview render that writes nothing to disk. Pass the Release path as `release` (absolute, or relative to the " +
		"workspace). The tool runs the full compiler pipeline through the provider render step and returns the produced " +
		"files by output-relative path. Use it to see exactly what a Release would emit before committing to a real build, " +
		"or to inspect one component's rendered output while authoring. In the detailed response format each file's rendered " +
		"content is included; the concise format returns just the file paths. A render failure comes back as actionable " +
		"guidance (route it through explain-error if needed) rather than a partial write. Optional `workspace_root` and " +
		"`catalog_dirs` override workspace and catalog discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "Path to the Release YAML to build, absolute or relative to the workspace root."
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
      "description": "detailed returns each rendered file's content; concise returns only the file paths. Defaults to detailed."
    }
  },
  "required": ["release"]
}`,
	invoke: buildInvoke,
}

func buildInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a buildArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("build", err)
		}
	}
	if strings.TrimSpace(a.Release) == "" {
		return errorResult("build requires a 'release' path")
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for the build.", err, "release"))
	}
	releasePath := tw.absReleasePath(a.Release)
	lf := loadReleaseLockfile(releasePath)
	view := tw.newView(lf)

	// This tool produces an artifact, so it runs the artifact posture of the one
	// preview computation every rendering surface is a transport over: the
	// integrity, closure-scoped validation, extends and axis-reconstruction gates
	// all run, because an artifact rendered from a closure nothing checked is
	// indistinguishable from one that was checked.
	//
	// The drift verify is the one artifact-posture gate held off, and the reason is
	// this surface's own lockfile posture rather than a view about drift: the tool
	// renders a Release that has NO lockfile at all, resolving against whatever
	// pins exist. Refusing a partial lockfile while rendering an absent one would
	// make less pinning information the more permissive input, which is not a
	// coherent contract to give a caller.
	//
	// The external-input service is absent here for the same reason it was before:
	// attaching one changes rendered bytes and is its own change with its own
	// proof.
	policy := compilerops.ArtifactPolicy()
	policy.VerifyLockfileDrift = false

	preview, err := compilerops.Preview(
		ctx,
		compilerops.PreviewInputs{
			View:            view,
			ReleasePath:     releasePath,
			WorkspaceRoot:   tw.root,
			WorkspaceConfig: tw.cfg,
			Lockfile:        lf,
			CompilerVersion: s.serverVersion,
		},
		policy,
		pipeline.Options{},
	)

	// Every recorded marker is reported whatever the outcome, so choosing a
	// stricter threshold never costs the caller a finding.
	deprecationNotices := previewDeprecationNotices(preview)

	if err != nil {
		return errorResult(explainToolError(
			previewFailureSummary(preview, deprecationNotices), err, previewFailureKind(preview, err)))
	}

	detail := parseResponseFormat(a.ResponseFormat) == responseFormatDetailed
	payload := buildResultPayload{Release: releasePath, Files: renderedFiles(preview.Pipeline, detail)}
	if len(deprecationNotices) > 0 {
		payload.Deprecations = deprecationNotices
	}
	text := renderBuildText(payload)
	return textResult(text, payload)
}

// previewDeprecationNotices pulls the recorded-marker notices out of a preview's
// warnings. They are reported whatever the outcome — on a success they ride in
// the payload, and on a refusal they are quoted in the message — because
// reporting is unconditional and only halting is configured, so raising the halt
// threshold never reduces what the caller is told.
func previewDeprecationNotices(preview *compilerops.PreviewResult) []string {
	if preview == nil {
		return nil
	}
	notices := make([]string, 0, len(preview.Warnings))
	for _, w := range preview.Warnings {
		if w.Class == compilerops.WarningDeprecation {
			notices = append(notices, w.Text)
		}
	}
	return notices
}

// previewFailureSummary is the caller-facing sentence for a refused build,
// chosen by the gate that refused rather than by matching the failure's text.
// Each one names what the build did and what the reader is looking at, because
// an agent consuming this has only the sentence to route on.
func previewFailureSummary(preview *compilerops.PreviewResult, deprecationNotices []string) string {
	gate := compilerops.GateNone
	if preview != nil {
		gate = preview.FailedGate
	}
	switch gate {
	case compilerops.GateManifestConsistency:
		return "The build refused: the Manifest maps a type onto a recipe that does not implement it."
	case compilerops.GateDeprecation:
		return "The build refused: this Release pins a deprecated artifact at or above the configured halt severity. " +
			"Every recorded marker in this Release:\n  " + strings.Join(deprecationNotices, "\n  ")
	case compilerops.GatePinnedIntegrity:
		return "The build refused: a pinned entry in the cache no longer matches the digest the lockfile records for it. " +
			"Re-pull the Release to refill the cache from the registry."
	case compilerops.GateLockfileDrift:
		return "The build refused: the lockfile no longer matches the dependencies this Release resolves to."
	case compilerops.GateClosurePillars:
		return "The build refused: an entity this Release reaches does not satisfy its schema."
	case compilerops.GateExtends:
		return "The build refused: an entity this Release reaches inherits from a parent that could not be resolved."
	case compilerops.GateAxisReconstruction:
		return "The build could not reconstruct the Release's pinned dependency set from the lockfile."
	case compilerops.GateRender:
		return "The build failed during rendering; nothing was written to disk."
	default:
		return "The build could not resolve the Release closure."
	}
}

// previewFailureKind names the document class a refusal is about, so the caller
// is pointed at the file it has to edit. The closure-resolution case keeps the
// per-stage attribution the resolution itself produces, which is finer than
// anything this switch could reconstruct.
func previewFailureKind(preview *compilerops.PreviewResult, err error) string {
	gate := compilerops.GateNone
	if preview != nil {
		gate = preview.FailedGate
	}
	switch gate {
	case compilerops.GateManifestConsistency:
		return "manifest"
	case compilerops.GateClosureResolution:
		var cerr *compilerops.ClosureError
		if errors.As(err, &cerr) {
			return closureStageKind(cerr.Stage)
		}
		return "release"
	default:
		return "release"
	}
}

// renderedFiles converts the pipeline's rendered artifact map into the ordered
// file list the build result carries. The paths are sorted so the result is
// deterministic across runs regardless of the map's iteration order; content is
// included only in the detailed form.
func renderedFiles(result *pipeline.Result, includeContent bool) []buildFile {
	paths := make([]string, 0, len(result.Artifacts))
	for p := range result.Artifacts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	files := make([]buildFile, 0, len(paths))
	for _, p := range paths {
		f := buildFile{Path: p}
		if includeContent {
			f.Content = string(result.Artifacts[p])
		}
		files = append(files, f)
	}
	return files
}

// renderBuildText renders the human-readable build summary: the file count and
// each rendered path, one per line.
func renderBuildText(p buildResultPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Build of %s rendered %d file(s) in memory (nothing written to disk):\n", p.Release, len(p.Files))
	for _, f := range p.Files {
		fmt.Fprintf(&b, "  %s\n", f.Path)
	}
	return strings.TrimRight(b.String(), "\n")
}
