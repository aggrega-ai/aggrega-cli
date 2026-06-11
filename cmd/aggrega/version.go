// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var versionJSON bool

// version is the release version stamped at build time via
// -ldflags "-X main.version=<v>" (the build scripts set it from
// `git describe --tags --always --dirty`). It is empty for a plain
// `go build` / `go install` without the flag; in that case the version
// falls back to runtime/debug.ReadBuildInfo — the module tag on
// `go install <module>@vX.Y.Z`, or the literal "(devel)" for a local
// working-tree build. The ldflags value wins when present so a binary
// produced by the build scripts carries the tag even though Go does not
// stamp Main.Version for an in-tree build. Whatever the source, the
// reported version string is v-less (see normalizeVersion).
var version string

// commit is the short revision stamped at build time via
// -ldflags "-X main.commit=<sha>" (scripts/build.sh sets it from
// `git rev-parse --short HEAD`). It is preferred over the
// runtime/debug.ReadBuildInfo `vcs.revision` setting, which go's
// -buildvcs stamping reports unreliably for a module built inside a git
// submodule (the gitdir-file indirection makes it lag HEAD). Empty for a
// plain `go build` without the flag; then `vcs.revision` is used.
var commit string

// newVersionCmd surfaces the aggrega CLI's build identity. Used by the
// VS Code extension's startup compatibility check and by operators
// inspecting a binary by hand.
func newVersionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "version",
		Short: "Print the aggrega CLI version",
		Long: `Print the aggrega CLI's build identity.

By default the output is a single human-readable line. With --json,
the output is a structured JSON object suitable for programmatic
consumption (the VS Code extension uses this form to read the
binary's version at activation time).

Version strings are v-less (1.0.0, not v1.0.0), homogeneous with the
version form catalog entities use. Tagged release builds report the
release version; source builds report the literal "(devel)".
Operators who see "(devel)" know they are running an unreleased
build.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVersion(cmd.OutOrStdout(), versionJSON)
		},
	}
	c.Flags().BoolVar(&versionJSON, "json", false, "emit a structured JSON object instead of the human-readable line")
	return c
}

// versionInfo is the structured payload emitted by `aggrega version
// --json`. Field names are camelCase to match the Go ecosystem's
// json.Marshal default convention; the wire shape is pinned by
// version_test.go and re-pinned on the extension side by
// version-check.test.ts.
type versionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// runVersion reads runtime/debug.ReadBuildInfo and emits the result
// to w in either human-readable or JSON form. When ReadBuildInfo
// returns ok == false (extremely rare, typically a non-module
// build), the unknown sentinel surfaces in both forms so the
// extension can treat it identically to "(devel)" — skip the compat
// check and log.
func runVersion(w io.Writer, asJSON bool) error {
	info := collectVersionInfo()
	if asJSON {
		enc := json.NewEncoder(w)
		return enc.Encode(info)
	}
	commit := info.Commit
	if len(commit) > 8 {
		commit = commit[:8]
	}
	if commit == "" {
		commit = "unknown"
	}
	_, err := fmt.Fprintf(w, "aggrega %s (commit %s, %s)\n", info.Version, commit, info.GoVersion)
	return err
}

// collectVersionInfo reads runtime/debug.ReadBuildInfo and walks
// info.Settings for vcs.revision. When ReadBuildInfo returns
// ok == false the function returns the unknown sentinel; callers
// (the extension specifically) treat it the same as "(devel)".
func collectVersionInfo() versionInfo {
	out := versionInfo{
		Version:   "(unknown)",
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if version != "" {
		out.Version = version
	}
	if commit != "" {
		out.Commit = commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		out.Version = normalizeVersion(out.Version)
		return out
	}
	if version == "" && info.Main.Version != "" {
		out.Version = info.Main.Version
	}
	if commit == "" {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				out.Commit = s.Value
				break
			}
		}
	}
	out.Version = normalizeVersion(out.Version)
	return out
}

// normalizeVersion strips the leading "v" from tag-shaped version
// values (v0.1.2 → 0.1.2) so every surface reports the same v-less
// form regardless of which build path supplied the value — ldflags
// stamping or the module tag from ReadBuildInfo. Git tags keep their
// v prefix (Go module resolution requires vX.Y.Z tags); the prefix is
// a tag-naming concern, not part of the version string humans and
// tools read. Values not shaped like a v-prefixed version — the
// "(devel)" and "(unknown)" sentinels, bare commit hashes — pass
// through unchanged.
func normalizeVersion(s string) string {
	if len(s) >= 2 && s[0] == 'v' && s[1] >= '0' && s[1] <= '9' {
		return s[1:]
	}
	return s
}
