// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// versionFlags carries the `version` command's flag state. It is constructed per
// command so two invocations in one process cannot observe each other's flag
// values.
type versionFlags struct {
	output  string
	verbose bool
}

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
// `git rev-parse HEAD`, cut to nine digits). It is preferred over the
// runtime/debug.ReadBuildInfo `vcs.revision` setting, which go's
// -buildvcs stamping reports unreliably for a module built inside a git
// submodule (the gitdir-file indirection makes it lag HEAD). Empty for a
// plain `go build` without the flag; then `vcs.revision` is used.
var commit string

// newVersionCmd surfaces the aggrega CLI's build identity. Used by the
// VS Code extension's startup compatibility check and by operators
// inspecting a binary by hand.
func newVersionCmd() *cobra.Command {
	opts := &versionFlags{}
	c := &cobra.Command{
		Use:   "version",
		Short: "Print the aggrega CLI version",
		Long: `Print the aggrega CLI's build identity.

By default the output is a single human-readable line followed by a
one-line pointer to the plugins compiled into the binary. With
-o json, the output is a structured JSON object suitable for
programmatic consumption (the VS Code extension uses this form to
read the binary's version at activation time), in the same indented
shape every aggrega command's JSON output uses.

With --verbose, the output replaces the pointer line with a table of
the embedded plugins — type, name, served capabilities, and commit —
under a header naming the count and their shared version. With
--verbose -o json, the JSON object gains an embeddedPlugins array
carrying the same detail.

Version strings are v-less (1.0.0, not v1.0.0), homogeneous with the
version form catalog entities use. Tagged release builds report the
release version, a build stamped with a version reports that version,
and a build carrying no version stamp reports the literal "(devel)",
which marks an unreleased build.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := parseFormat("version", opts.output, "text", "json")
			if err != nil {
				return err
			}
			return runVersion(cmd.OutOrStdout(), format == "json", opts.verbose)
		},
	}
	c.Flags().StringVarP(&opts.output, "output", "o", "text", "Output format: text|json.")
	c.Flags().BoolVar(&opts.verbose, "verbose", false, "include the embedded-plugin set in the output")
	return c
}

// versionInfo is the structured payload emitted by `aggrega version
// -o json`. Field names are camelCase to match the Go ecosystem's
// json.Marshal default convention; the wire shape is pinned by
// version_test.go and re-pinned on the extension side by
// version-check.test.ts.
//
// The payload is emitted in the indented shape every structured surface
// shares, followed by one newline, so a consumer reading the stream sees
// one document per invocation. What consumers depend on is the field
// set and the field names, not the surrounding layout: a consumer that
// matches on the emitted text rather than parsing it strips whitespace
// first. version_test.go pins the emitted shape as well as the field
// set, so neither can move without a test saying so.
//
// abiVersion reports the plugin ABI version this binary speaks — the
// single version its host and its embedded guests agree on. It is a
// build-time constant (it does not vary with the git-describe stamp),
// exposed here so a consumer can read a binary's plugin-ABI contract
// without loading a guest. It is the machine-readable source a
// consumer pipeline reads to gate on ABI compatibility.
type versionInfo struct {
	Version    string `json:"version"`
	Committed  string `json:"committed,omitempty"`
	Commit     string `json:"commit"`
	ABIVersion string `json:"abiVersion"`
	GoVersion  string `json:"goVersion"`
	Platform   string `json:"platform"`

	// EmbeddedPlugins carries the build-embedded plugin set and is populated
	// only under --verbose; it is absent from the default wire shape so a
	// consumer reading the plain version object sees an unchanged contract.
	EmbeddedPlugins []embeddedPluginJSON `json:"embeddedPlugins,omitempty"`
}

// embeddedPluginJSON is one embedded plugin's detail on the --verbose -o json
// surface: its identity, self-reported type, served capabilities, and build
// provenance.
type embeddedPluginJSON struct {
	URI          string   `json:"uri"`
	Type         string   `json:"type"`
	Capabilities []string `json:"capabilities,omitempty"`
	Version      string   `json:"version,omitempty"`
	Commit       string   `json:"commit,omitempty"`
}

// runVersion reads runtime/debug.ReadBuildInfo and emits the result
// to w in either human-readable or JSON form. When ReadBuildInfo
// returns ok == false (extremely rare, typically a non-module
// build), the unknown sentinel surfaces in both forms so the
// extension can treat it identically to "(devel)" — skip the compat
// check and log.
func runVersion(w io.Writer, asJSON, verbose bool) error {
	info := collectVersionInfo()
	summaries := collectEmbeddedSummaries()
	if asJSON {
		if verbose {
			info.EmbeddedPlugins = embeddedSummariesToJSON(summaries)
		}
		return writeStructured(w, "version", info)
	}
	if _, err := io.WriteString(w, formatVersionLine(info)); err != nil {
		return err
	}
	if verbose {
		return writeEmbeddedTable(w, summaries)
	}
	return writeEmbeddedPointer(w, summaries)
}

// embeddedPluginSummary is one embedded plugin's identity and provenance as
// read from its build-extracted describe() descriptor, without instantiating the
// guest.
type embeddedPluginSummary struct {
	uri          string
	pluginType   string
	capabilities []string
	version      string
	commit       string
}

// collectEmbeddedSummaries reads the build-extracted describe() descriptor for
// each embedded plugin, in the deterministic URI order the embedded set reports.
// It instantiates nothing: the static descriptors are enough to name the set and
// its provenance. A build with no embedded plugins yields an empty slice.
func collectEmbeddedSummaries() []embeddedPluginSummary {
	uris := embedded.URIs()
	out := make([]embeddedPluginSummary, 0, len(uris))
	for _, uri := range uris {
		s := embeddedPluginSummary{uri: uri}
		if raw, ok := embedded.LookupDescriptor(uri); ok {
			var desc wasm.PluginDescriptor
			if err := json.Unmarshal(raw, &desc); err == nil {
				s.pluginType = desc.PluginType
				s.capabilities = desc.Capabilities
				if desc.Metadata != nil {
					s.version = desc.Metadata.Version
					s.commit = desc.Metadata.Commit
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// embeddedSummariesToJSON maps the embedded summaries to their JSON detail form,
// preserving order.
func embeddedSummariesToJSON(summaries []embeddedPluginSummary) []embeddedPluginJSON {
	out := make([]embeddedPluginJSON, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, embeddedPluginJSON{
			URI:          s.uri,
			Type:         s.pluginType,
			Capabilities: s.capabilities,
			Version:      s.version,
			Commit:       s.commit,
		})
	}
	return out
}

// sharedEmbeddedVersion returns the single version stamped across the embedded
// set. The embedded plugins are built and stamped together, so they share one
// version; the function returns "(unknown)" when the set carries no stamp and
// "(mixed)" in the unexpected case that the stamps diverge.
func sharedEmbeddedVersion(summaries []embeddedPluginSummary) string {
	seen := map[string]struct{}{}
	last := ""
	for _, s := range summaries {
		seen[s.version] = struct{}{}
		last = s.version
	}
	if len(seen) != 1 {
		return "(mixed)"
	}
	if last == "" {
		return "(unknown)"
	}
	return last
}

// writeEmbeddedPointer writes the one-line pointer to the embedded plugin set
// that follows the plain version line. It writes nothing when no plugins are
// embedded (a build without the built-in guests).
func writeEmbeddedPointer(w io.Writer, summaries []embeddedPluginSummary) error {
	if len(summaries) == 0 {
		return nil
	}
	_, err := fmt.Fprintf(w, "embedded  %d plugins version %s — `aggrega plugin list` for details\n",
		len(summaries), sharedEmbeddedVersion(summaries))
	return err
}

// writeEmbeddedTable writes the verbose embedded-plugin table — a header naming
// the count and shared version, then a four-column table keyed to each plugin's
// commit provenance. It writes a short notice when no plugins are embedded.
func writeEmbeddedTable(w io.Writer, summaries []embeddedPluginSummary) error {
	if len(summaries) == 0 {
		_, err := fmt.Fprintln(w, "\nNo embedded plugins.")
		return err
	}
	shared := sharedEmbeddedVersion(summaries)
	header := fmt.Sprintf("Embedded plugins (%d, all version %s):", len(summaries), shared)
	if shared == "(mixed)" {
		header = fmt.Sprintf("Embedded plugins (%d, versions vary — see COMMIT):", len(summaries))
	}
	fmt.Fprintf(w, "\n%s\n\n", header)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  TYPE\tNAME\tCAPABILITIES\tCOMMIT")
	for _, s := range summaries {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n",
			s.pluginType, s.uri, strings.Join(s.capabilities, ", "), s.commit)
	}
	return tw.Flush()
}

// formatVersionLine renders the human-readable version line. When a
// commit timestamp is present it appears before the commit hash and is
// labelled "committed" (it is the commit time from vcs.time, not the
// build time). When the timestamp is absent the segment is omitted
// rather than filled with a placeholder. The commit hash is shortened
// to eight characters and falls back to "unknown" when absent. The
// plugin ABI version is shown as an "abi" segment when present; it is
// omitted only for a synthetic info value that carries none.
func formatVersionLine(info versionInfo) string {
	commit := info.Commit
	if len(commit) > 8 {
		commit = commit[:8]
	}
	if commit == "" {
		commit = "unknown"
	}
	segs := make([]string, 0, 4)
	if info.Committed != "" {
		segs = append(segs, "committed "+info.Committed)
	}
	segs = append(segs, "commit "+commit)
	if info.ABIVersion != "" {
		segs = append(segs, "abi "+info.ABIVersion)
	}
	segs = append(segs, info.GoVersion)
	return fmt.Sprintf("aggrega %s (%s)\n", info.Version, strings.Join(segs, ", "))
}

// collectVersionInfo reads runtime/debug.ReadBuildInfo and walks
// info.Settings for vcs.revision and vcs.time. When ReadBuildInfo
// returns ok == false the function returns the unknown sentinel;
// callers (the extension specifically) treat it the same as
// "(devel)". The commit timestamp comes only from vcs.time — there
// is no ldflags source for it — and is used verbatim (already UTC
// RFC3339); it is left empty when the setting is absent.
func collectVersionInfo() versionInfo {
	out := versionInfo{
		Version:    "(unknown)",
		ABIVersion: wasm.ABIVersion,
		GoVersion:  runtime.Version(),
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
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
	for _, s := range info.Settings {
		if s.Key == "vcs.time" {
			out.Committed = s.Value
			break
		}
	}
	out.Version = normalizeVersion(out.Version)
	return out
}

// provenanceCompilerVersion returns the compiler-version string stamped into
// IR provenance — the same build identity `aggrega version` reports, so the
// audit trail names the exact binary that produced the IR. A build stamped
// with a version reports it (v-less, e.g. "1.2.0" for a release tag); a build
// carrying no version stamp reports the "(devel)" marker, so IR built from an
// unstamped compiler is never mistaken for release output. It shares collectVersionInfo's derivation
// (ldflags else ReadBuildInfo) so the stamp and the version command cannot drift.
func provenanceCompilerVersion() string {
	return collectVersionInfo().Version
}

// toolchainStamp returns the toolchain versions a pull records in the lockfile:
// this binary's compiler version, and the version of each core plugin it carries.
// Both halves are read through the derivations this file already owns —
// provenanceCompilerVersion and collectEmbeddedSummaries — so the stamp cannot
// report a different version from the one `aggrega version` reports for the same
// binary.
//
// It records versions and nothing else. A core plugin is published on the
// compiler's own tag at the same version, so the version reads the same whether a
// plugin was served from inside the binary or fetched from a registry; recording
// which of those happened would make the lockfile differ between two machines that
// resolved the same closure, which is the property a committed lockfile depends on.
// So the descriptor's URI and version are read and its source is deliberately not.
//
// The stamp is advisory: nothing reads it back to decide whether a build may
// proceed. See lockfile.ToolchainStamp for why enforcing it would pin the compiler
// by proxy.
//
// A build carrying no embedded plugins — the light build, which compiles the
// guests out — yields a stamp with an empty core-plugin list rather than no stamp,
// because the compiler version is recorded either way.
func toolchainStamp() *lockfile.ToolchainStamp {
	summaries := collectEmbeddedSummaries()
	stamp := &lockfile.ToolchainStamp{
		CompilerVersion: provenanceCompilerVersion(),
		CorePlugins:     make([]lockfile.CorePluginVersion, 0, len(summaries)),
	}
	for _, s := range summaries {
		stamp.CorePlugins = append(stamp.CorePlugins, lockfile.CorePluginVersion{
			Plugin:  s.uri,
			Version: normalizeVersion(s.version),
		})
	}
	return stamp
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
