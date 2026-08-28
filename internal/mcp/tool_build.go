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
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
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
			return nil, invalidToolArguments("build", err)
		}
	}
	if strings.TrimSpace(a.Release) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "build requires a 'release' path"}
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for the build.", err, "release"))
	}
	releasePath := tw.absReleasePath(a.Release)
	lf := loadReleaseLockfile(releasePath)
	view := tw.newView(lf)

	resolved, cerr := compilerops.ResolveClosure(compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   tw.root,
		WorkspaceConfig: tw.cfg,
		Lockfile:        lf,
		Augment:         true,
	})
	if cerr != nil {
		return errorResult(explainToolError(
			"The build could not resolve the Release closure.", cerr, closureStageKind(cerr.Stage)))
	}
	// This tool produces an artifact, so it reaches the same verdict the command
	// line's build does on a Manifest the catalog contradicts: it refuses. The
	// report-shaped tools beside it enumerate the same finding and carry on, which
	// is the distinction — what the caller asked to be produced, not how the
	// finding was reached.
	if err := errors.Join(resolved.ManifestConsistencyFindings...); err != nil {
		return errorResult(explainToolError(
			"The build refused: the Manifest maps a type onto a recipe that does not implement it.",
			err, "manifest"))
	}

	// The deprecation report over the lockfile's recorded markers, and this verb's
	// verdict on it. The tool produces an artifact, so it reaches the same verdict
	// the command line's build does: it reports every recorded marker and refuses
	// where the workspace configured a halt at that severity, through the one shared
	// halting rule. The dial is the build's own and is read here because this IS the
	// build operation over a different transport — one key doing one job. The
	// report-shaped tools beside it enumerate the same findings and carry on.
	//
	// The refusal carries EVERY notice rather than only the halting one, so choosing
	// a stricter threshold never costs the caller a finding.
	deprecations := lf.DeprecationFindings()
	var deprecationHalt error
	deprecationNotices := make([]string, 0, len(deprecations))
	for _, f := range deprecations {
		deprecationNotices = append(deprecationNotices, f.Notice())
		if deprecationHalt == nil {
			deprecationHalt = deprecation.HaltFor(
				f.Notice(), deprecation.Severity(f.Severity), deprecation.Severity(tw.cfg.Build.HaltOnDeprecatedSeverity))
		}
	}
	if deprecationHalt != nil {
		return errorResult(explainToolError(
			"The build refused: this Release pins a deprecated artifact at or above the configured halt severity. "+
				"Every recorded marker in this Release:\n  "+strings.Join(deprecationNotices, "\n  "),
			deprecationHalt, "release"))
	}

	// BuildToBuffer renders to in-memory buffers and performs no filesystem
	// writes; the returned artifacts are returned to the caller and never
	// persisted here, so the workspace is unchanged by a build tool call.
	result, err := compilerops.BuildToBuffer(ctx, compilerops.BuildInputs{
		View:                             view,
		Resolved:                         resolved,
		ReleasePath:                      releasePath,
		WorkspaceConfig:                  tw.cfg,
		WorkspaceRoot:                    tw.root,
		Lockfile:                         lf,
		CompilerVersion:                  s.serverVersion,
		TolerateCatalogViewInconsistency: true,
	}, pipeline.Options{})
	if err != nil {
		return errorResult(explainToolError(
			"The build failed during rendering; nothing was written to disk.", err, "release"))
	}

	detail := parseResponseFormat(a.ResponseFormat) == responseFormatDetailed
	payload := buildResultPayload{Release: releasePath, Files: renderedFiles(result, detail)}
	if len(deprecationNotices) > 0 {
		payload.Deprecations = deprecationNotices
	}
	text := renderBuildText(payload)
	return textResult(text, payload)
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
