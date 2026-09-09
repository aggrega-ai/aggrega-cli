// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The tool surface's leg of the dependency resolution.
//
// Every proof here reads the RESPONSE and the FILE, which are the two things this
// transport is responsible for: it reimplements no resolution, so what is worth
// asserting is that the operation ran, that what it wrote is on disk where the
// answer says it is, and that the answer carries the resolution rather than a bare
// acknowledgement.
//
// The fixture is COPIED into a temporary workspace and never used in place. A
// resolution writes — the lockfile, the artifact cache, and the resolved content
// digests it freezes into each writable recipe file — so a proof pointed at the
// tracked fixture would modify tracked files as a side effect of running.

// pullToolchainStamp is the stamp the proofs thread through the server, so the
// recorded value is checked against something this test chose rather than against
// whatever the binary happens to report.
//
// The version is deliberately not release-shaped: cutting a release must not touch
// a test file, so nothing here mirrors the real version.
var pullToolchainStamp = &lockfile.ToolchainStamp{
	CompilerVersion: "0.0.0-pull-tool-probe",
	CorePlugins: []lockfile.CorePluginVersion{
		{Plugin: "renderer/probe", Version: "0.0.0-probe"},
	},
}

// pullWorkspace copies the hermetic end-to-end fixture into a temp workspace and
// configures it offline, then returns the server, the root, the catalog stack and
// the Release path.
//
// Offline is what makes the proof hermetic rather than merely local: the
// network-egress gate refuses before any dial, so no registry tier exists and the
// resolution answers from the copied catalog alone. A proof that resolved against a
// registry would measure that registry's availability as well as this transport.
//
// It is written through the loader's own constant, at the key the setting is
// declared under. Both halves are the assertion rather than tidiness: a setting
// written to a filename the loader does not read, or nested under a block that
// carries only the root-defining directories, parses as nothing and leaves the
// egress gate open — and because this fixture's catalog is local, the resolution
// would still succeed, so the proof would report itself hermetic while nothing
// enforced it. The workspace block is present separately, to anchor the root here
// rather than letting discovery walk up into the checkout the suite runs inside.
func pullWorkspace(t *testing.T) (s *server, root string, dirs []string, releasePath string) {
	t.Helper()
	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "fullstack-app")
	root = t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture workspace: %v", err)
	}
	cfg := "workspace: {}\noffline: true\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	s = newTestServer(t)
	s.toolchain = pullToolchainStamp
	return s, root, []string{filepath.Join(root, "catalog")}, filepath.Join(root, "release.yaml")
}

// unmarshalPullPayload reads the structured payload off a pull result, failing
// when the result carries none — which for this tool is itself a defect, on a
// refusal as much as on a success.
func unmarshalPullPayload(t *testing.T, res *callToolResult) pullResultPayload {
	t.Helper()
	if len(res.StructuredContent) == 0 {
		t.Fatalf("the pull result carries no structured payload: %s", res.Content[0].Text)
	}
	var payload pullResultPayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal pull payload: %v", err)
	}
	return payload
}

// callPull drives the pull tool through the real tools/call dispatch.
func callPull(t *testing.T, s *server, args pullArgs) *callToolResult {
	t.Helper()
	return callTool(t, s, "pull", args)
}

// TestPullToolIsAdvertisedInTheDefaultToolListing is the listing half of the
// parity this tool exists to close.
//
// It reads the listing the protocol method returns rather than the registry the
// method reads, because the registry being right is not the property in question:
// what was missing before was a pull an assistant could SEE without first calling
// the capability tool, and only the listing says whether it can.
func TestPullToolIsAdvertisedInTheDefaultToolListing(t *testing.T) {
	s := newTestServer(t)
	res, rpcErr := s.handleToolsList()
	if rpcErr != nil {
		t.Fatalf("tools/list returned a protocol error: %+v", rpcErr)
	}
	listing, ok := res.(listToolsResult)
	if !ok {
		t.Fatalf("tools/list returned %T, want listToolsResult", res)
	}
	var found *tool
	for i := range listing.Tools {
		if listing.Tools[i].Name == "pull" {
			found = &listing.Tools[i]
		}
	}
	if found == nil {
		names := make([]string, 0, len(listing.Tools))
		for i := range listing.Tools {
			names = append(names, listing.Tools[i].Name)
		}
		t.Fatalf("the default tools/list advertises no pull tool, so an assistant never sees one; it offers %s",
			strings.Join(names, ", "))
	}
	// The description a client shows an assistant has to say what the tool WRITES,
	// because its generated twin quotes this first sentence as the disclosure a
	// caller chooses between the two on.
	first := firstSentence(found.Description)
	for _, owed := range []string{"lockfile", "cache"} {
		if !strings.Contains(first, owed) {
			t.Errorf("the pull tool's first sentence does not name what it writes (%q missing): %q", owed, first)
		}
	}
	if len(found.InputSchema) == 0 {
		t.Error("the advertised pull tool carries no argument schema")
	}
}

// TestPullToolWritesTheLockfileItReports is the write proof: a workspace with no
// lockfile gets one, at the path the answer names, carrying the resolution the
// answer reports.
//
// The file is read back from disk rather than inferred from the result. A transport
// that reported a path and wrote nothing would satisfy every assertion made about
// the response alone, which is exactly the failure a caller cannot see.
func TestPullToolWritesTheLockfileItReports(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)

	lockPath := lockfile.PathFor(releasePath)
	if _, err := os.Stat(lockPath); err == nil {
		t.Fatalf("the fixture already carries a lockfile at %s, so this proof would measure a reconcile "+
			"rather than a first resolution", lockPath)
	}

	res := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if res.IsError {
		t.Fatalf("pull refused on a clean fixture: %s", res.Content[0].Text)
	}
	payload := unmarshalPullPayload(t, res)

	if payload.Verified {
		t.Error("a resolution that wrote the lockfile reported itself as a comparison that wrote nothing")
	}
	if payload.Lockfile != lockPath {
		t.Errorf("the answer names the lockfile %q; the resolution writes %q", payload.Lockfile, lockPath)
	}
	if _, err := os.Stat(payload.Lockfile); err != nil {
		t.Fatalf("the answer names a lockfile that is not on disk: %v", err)
	}
	if len(payload.Resolved) == 0 {
		t.Fatal("the answer reports no resolved reference, so a caller is told a resolution happened and " +
			"nothing about what it resolved")
	}
	// Every reported reference carries an identity, which is the field a caller
	// joins on; a row with no identity is a row it cannot act on.
	for i, ref := range payload.Resolved {
		if strings.TrimSpace(ref.URI) == "" {
			t.Errorf("resolved reference %d carries no identity: %+v", i, ref)
		}
	}
}

// TestPullToolThreadsTheToolchainStampIntoTheLockfile is the proof behind
// supplying the stamp at all: the versions recorded in the file are the ones the
// command layer handed this server, so a lockfile written through this transport
// records what a lockfile written at a terminal records for the same binary.
//
// It is the assertion a fabricated stamp fails. A transport that recorded no stamp,
// or synthesised one with an empty core-plugin list — the shape a build that
// compiles its guests out legitimately produces — would write a file that differs
// from the command line's for the same input, and nothing else here would notice.
func TestPullToolThreadsTheToolchainStampIntoTheLockfile(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)

	res := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if res.IsError {
		t.Fatalf("pull refused on a clean fixture: %s", res.Content[0].Text)
	}
	payload := unmarshalPullPayload(t, res)

	lf, _, err := lockfile.LoadLockfile(payload.Lockfile)
	if err != nil {
		t.Fatalf("load the lockfile the resolution wrote: %v", err)
	}
	if lf.Toolchain == nil {
		t.Fatal("the lockfile records no toolchain stamp, so it differs from one the command line writes for " +
			"the same binary and the same input")
	}
	if lf.Toolchain.CompilerVersion != pullToolchainStamp.CompilerVersion {
		t.Errorf("the lockfile records compiler version %q; the server was handed %q",
			lf.Toolchain.CompilerVersion, pullToolchainStamp.CompilerVersion)
	}
	// The core-plugin list is asserted for its CONTENT, not its length. An empty
	// list is the legitimate shape of a build that carries no core plugins, so a
	// length check alone would pass on a stamp that had been synthesised here rather
	// than threaded through.
	if len(lf.Toolchain.CorePlugins) != len(pullToolchainStamp.CorePlugins) {
		t.Fatalf("the lockfile records %d core plugin version(s); the server was handed %d",
			len(lf.Toolchain.CorePlugins), len(pullToolchainStamp.CorePlugins))
	}
	for i, want := range pullToolchainStamp.CorePlugins {
		if lf.Toolchain.CorePlugins[i] != want {
			t.Errorf("the lockfile records core plugin %d as %+v; the server was handed %+v",
				i, lf.Toolchain.CorePlugins[i], want)
		}
	}
}

// TestPullToolReconcilingAnUnchangedDependencySetRewritesNothing is the no-op
// proof, and it is a byte comparison rather than a claim about the answer.
//
// The property is what the tool's own description promises a caller: an unchanged
// dependency set rewrites nothing. It is asserted over the file's bytes including
// its recorded timestamp, because a resolution that re-stamped the time would
// produce a diff in a reviewed commit for a call that changed no pin.
func TestPullToolReconcilingAnUnchangedDependencySetRewritesNothing(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)
	args := pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs}

	first := callPull(t, s, args)
	if first.IsError {
		t.Fatalf("the first pull refused: %s", first.Content[0].Text)
	}
	lockPath := unmarshalPullPayload(t, first).Lockfile
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile the first pull wrote: %v", err)
	}

	second := callPull(t, s, args)
	if second.IsError {
		t.Fatalf("the second pull refused: %s", second.Content[0].Text)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile after the second pull: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("re-resolving an unchanged dependency set rewrote the lockfile, so a call that changed no "+
			"pin produces a diff:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestPullToolTargetedUpgradeReachesTheResolutionAsAnUpgrade is what the byte
// comparison above structurally cannot establish: that naming targets actually
// asks for an upgrade of exactly those references.
//
// The comparison above cannot do it because the lockfile writer preserves the
// recorded timestamp when nothing else moved, so over a catalog offering one
// version of each entity a wholesale upgrade and a reconcile produce the identical
// bytes — measured, not assumed. Byte-stability is therefore a true property of
// both, and blind to which was asked for.
//
// This discriminates them on a consequence only an upgrade has: a named target the
// resolution cannot account for halts. That outcome needs BOTH derived fields to
// arrive — the switch and the subject — so it fails if either is dropped, which is
// the pairing this transport is responsible for.
func TestPullToolTargetedUpgradeReachesTheResolutionAsAnUpgrade(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)

	written := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if written.IsError {
		t.Fatalf("the preparing pull refused: %s", written.Content[0].Text)
	}

	const unknown = "example/compute/not-a-real-entity"
	res := callPull(t, s, pullArgs{
		Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs,
		UpgradeTargets: []string{unknown},
	})
	if !res.IsError {
		t.Fatalf("a named upgrade target the resolution cannot account for was accepted, so the targets are "+
			"not reaching it as an upgrade: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, unknown) {
		t.Errorf("the refusal does not name the target it could not account for, so a caller cannot tell "+
			"which of its targets was wrong: %q", res.Content[0].Text)
	}
}

// TestPullToolVerifyComparesAndWritesNothing is the read-only proof, and the half
// of it that matters is the byte comparison: a comparison that wrote would be
// indistinguishable from a reconcile in the answer alone.
func TestPullToolVerifyComparesAndWritesNothing(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)

	written := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if written.IsError {
		t.Fatalf("the preparing pull refused: %s", written.Content[0].Text)
	}
	lockPath := unmarshalPullPayload(t, written).Lockfile
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the prepared lockfile: %v", err)
	}

	res := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs, Verify: true})
	if res.IsError {
		t.Fatalf("verifying a lockfile the same resolution just wrote reported drift: %s", res.Content[0].Text)
	}
	payload := unmarshalPullPayload(t, res)
	if !payload.Verified {
		t.Error("a comparison did not report itself as one, so a caller cannot tell it from a run that wrote")
	}
	after, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile after the comparison: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a comparison wrote to the lockfile it was asked only to read")
	}
	// The stamp is what the recorded file says produced it, which is the question a
	// caller reaches a comparison to answer.
	if payload.Toolchain == nil {
		t.Fatal("the comparison reports no toolchain, so a caller is not told what wrote the lockfile it read")
	}
	if payload.Toolchain.CompilerVersion != pullToolchainStamp.CompilerVersion {
		t.Errorf("the comparison reports compiler version %q; the file was written with %q",
			payload.Toolchain.CompilerVersion, pullToolchainStamp.CompilerVersion)
	}
}

// TestPullToolVerifyRefusesADriftedLockfileAndKeepsTheResolution is the fidelity
// proof, and it is the one assertion here that a correctly-routed refusal can still
// fail.
//
// A drift verdict is the case a caller most needs to read field by field: it has to
// decide what to change. The shared resolution reports the verdict by returning an
// empty result alongside its error, so a transport that read the resolution off
// that result would answer a refusal with prose and nothing else — while every
// assertion about the channel it travelled on stayed green.
func TestPullToolVerifyRefusesADriftedLockfileAndKeepsTheResolution(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)

	written := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if written.IsError {
		t.Fatalf("the preparing pull refused: %s", written.Content[0].Text)
	}
	lockPath := unmarshalPullPayload(t, written).Lockfile

	recorded, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the prepared lockfile: %v", err)
	}
	// Drift is introduced by moving a recorded version away from what resolves, and
	// the splice is asserted to have changed something. A substitution that matched
	// nothing would leave the file agreeing with the resolution, and this proof would
	// then pass a tool that reported no drift at all.
	const anchor = "resolved_version:"
	if !bytes.Contains(recorded, []byte(anchor)) {
		t.Fatalf("the lockfile carries no %q key, so this proof cannot introduce drift and would assert "+
			"nothing", anchor)
	}
	drifted := bytes.Replace(recorded, []byte(anchor+" "), []byte(anchor+" 9999.0.0 # "), 1)
	if bytes.Equal(drifted, recorded) {
		t.Fatal("the splice changed nothing, so the lockfile still matches the resolution")
	}
	if err := os.WriteFile(lockPath, drifted, 0o644); err != nil {
		t.Fatalf("write the drifted lockfile: %v", err)
	}

	res := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs, Verify: true})
	if !res.IsError {
		t.Fatalf("verifying a drifted lockfile reported success: %s", res.Content[0].Text)
	}
	// The refusal is legible with no terminal: it says what is wrong and where to
	// read the authoring contract.
	if !strings.Contains(res.Content[0].Text, "See: ") {
		t.Errorf("the refusal carries no documentation pointer: %q", res.Content[0].Text)
	}
	payload := unmarshalPullPayload(t, res)
	if len(payload.Resolved) == 0 {
		t.Error("the refusal discarded the resolution the run had already computed, so a caller holding a " +
			"drift verdict cannot read what the Release resolves to today")
	}
	if payload.Lockfile != lockPath {
		t.Errorf("the refusal names the lockfile %q; the drifted file is %q", payload.Lockfile, lockPath)
	}
}

// TestPullToolRefusesComparingAndUpgradingTogether drives the refused combination
// through the real dispatch and holds its guidance to naming both arguments, so a
// caller is told which pair it may not send rather than that something was wrong.
//
// The schema declares the same rule, and the cross-check that the two agree is
// driven from the argument-schema proofs. What this adds is the guidance text,
// which no schema can carry.
func TestPullToolRefusesComparingAndUpgradingTogether(t *testing.T) {
	s := newTestServer(t)
	res := callPull(t, s, pullArgs{
		Release:        "release.yaml",
		Verify:         true,
		UpgradeTargets: []string{"core/database"},
	})
	if !res.IsError {
		t.Fatalf("comparing and upgrading together was accepted: %s", res.Content[0].Text)
	}
	for _, owed := range []string{"verify", "upgrade_targets"} {
		if !strings.Contains(res.Content[0].Text, owed) {
			t.Errorf("the refusal does not name %q, so the caller is not told which pair it may not send: %q",
				owed, res.Content[0].Text)
		}
	}
}

// TestPullToolExposesNoWholesaleUpgrade pins the withholding as a property of the
// advertised surface rather than of a check inside the tool.
//
// The bare form discards the whole lockfile and re-resolves everything, and the
// command line gates it behind a person who can be asked. A tool call has nobody to
// ask, so the form is left unrepresentable: there is no argument that selects it,
// which is a stronger statement than a guard refusing it, because a guard is one
// edit from being relaxed and an absent argument has to be designed back in.
//
// It reads the ADVERTISED schema, since that is the whole of what a caller can
// compose from.
func TestPullToolExposesNoWholesaleUpgrade(t *testing.T) {
	s := newGuidanceServer(t)
	spec, found := s.lookupTool("pull")
	if !found {
		t.Fatal("no pull tool is registered; this assertion measures nothing")
	}
	for _, forbidden := range []string{"upgrade", "include_prerelease", "detail", "quiet", "progress"} {
		if declaresStringArgument(t, *spec, forbidden) {
			t.Errorf("the pull tool declares a %q argument, which this surface withholds", forbidden)
		}
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(spec.inputSchema), &schema); err != nil {
		t.Fatalf("the pull tool's input schema does not parse: %v", err)
	}
	// The named-target form is the only upgrade this surface can express, so its
	// presence is what makes the absence of the wholesale form a withholding rather
	// than the capability simply being missing.
	if _, ok := schema.Properties["upgrade_targets"]; !ok {
		t.Error("the pull tool declares no upgrade_targets, so no upgrade is reachable at all")
	}
	for _, forbidden := range []string{"upgrade", "include_prerelease", "detail", "quiet", "progress"} {
		if _, ok := schema.Properties[forbidden]; ok {
			t.Errorf("the pull tool's schema declares %q, which this surface withholds", forbidden)
		}
	}
}

// TestPullToolDerivesTheUpgradeSwitchFromTheTargetsAlone guards the composition,
// which is the half of the withholding an absent argument does not establish.
//
// The advertised schema settles that no ARGUMENT selects the wholesale refresh —
// the form that discards the whole lockfile and re-resolves every pin. What it
// cannot settle is what the transport asks for once no target is named, and that
// is one token: the switch is derived from the targets, so forcing it on turns
// every plain reconcile call into the refresh a tool call has nobody to confirm.
//
// Nothing a caller reads catches that, which is why the assembled contract is the
// subject here rather than an outcome. The answer carries the same fields either
// way; the lockfile on disk is byte-identical too, because over a catalog serving
// one version of each entity a refresh and a reconcile resolve to the same pins.
// Measured on this fixture: a recorded pin moved to a version the catalog cannot
// serve is re-resolved by a reconcile rather than halting it, so even
// manufacturing an unservable pin does not separate the two from the outside.
func TestPullToolDerivesTheUpgradeSwitchFromTheTargetsAlone(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)
	tw, err := s.resolveToolWorkspace(root, dirs)
	if err != nil {
		t.Fatalf("resolve the workspace the assembled contract is built against: %v", err)
	}

	cases := []struct {
		name        string
		targets     []string
		wantUpgrade bool
	}{
		{name: "naming no target asks for a reconcile", targets: nil, wantUpgrade: false},
		{
			name:        "naming one target upgrades exactly it",
			targets:     []string{"core/datastore/postgres"},
			wantUpgrade: true,
		},
		{
			name:        "naming several targets upgrades exactly those",
			targets:     []string{"core/datastore/postgres", "core/compute/api-server"},
			wantUpgrade: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := pullResolutionConfig(s, tw, releasePath, pullArgs{
				Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs,
				UpgradeTargets: tc.targets,
			}, tc.targets, io.Discard, pullops.Reporter{})

			switch {
			case tc.wantUpgrade && !cfg.Upgrade:
				t.Errorf("naming %d upgrade target(s) composes a resolution with the upgrade switch off, so "+
					"the named pins are honoured rather than re-resolved and the caller's request does nothing",
					len(tc.targets))
			case !tc.wantUpgrade && cfg.Upgrade:
				t.Error("a call naming no upgrade target composes a resolution with the upgrade switch on, " +
					"which discards the whole lockfile and re-resolves every pin from current registry state " +
					"— the wholesale refresh this surface withholds because a tool call has nobody to confirm it")
			}
			if !slices.Equal(cfg.UpgradeTargets, tc.targets) {
				t.Errorf("the resolution is asked to upgrade %v; the call named %v",
					cfg.UpgradeTargets, tc.targets)
			}
		})
	}
}

// TestPullToolAndItsGeneratedTwinDiscloseEachOther is the two-way disclosure,
// asserted over the pairing this tool earns by naming the operation it answers.
//
// The pairing is keyed on the operation rather than on a spelling, so this is also
// what proves the registration reached it: a tool registered without its operation
// would be advertised, invocable, and paired with nothing.
func TestPullToolAndItsGeneratedTwinDiscloseEachOther(t *testing.T) {
	// The command-backed surface is used rather than the smaller fixture one,
	// because which commands back an operation is exactly what decides whether a
	// generated twin exists — and it reads that from the command line's own
	// availability records, so this follows a command renamed or newly exempted.
	s := &server{out: io.Discard, logw: io.Discard, commandTools: deriveCommandTools(commandBackedSurface())}

	var curatedPull, generatedPull *toolSpec
	for _, spec := range s.curatedTools() {
		if spec.name == "pull" {
			s := spec
			curatedPull = &s
		}
	}
	for i := range s.commandTools {
		if s.commandTools[i].operation == "pull" {
			generatedPull = &s.commandTools[i]
		}
	}
	if curatedPull == nil {
		t.Fatal("the curated set serves no pull tool")
	}
	if generatedPull == nil {
		t.Fatal("this session derived no generated tool for the pull operation, so the disclosure this " +
			"asserts is unreachable and the assertion would measure nothing")
	}
	if !strings.Contains(curatedPull.description, generatedPull.name) {
		t.Errorf("the curated pull tool does not disclose its generated twin %q", generatedPull.name)
	}
	if !strings.Contains(generatedPull.description, curatedPull.name) {
		t.Errorf("the generated tool %q does not disclose the curated pull tool", generatedPull.name)
	}
}
