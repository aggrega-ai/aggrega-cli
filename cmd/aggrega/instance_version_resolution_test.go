// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// A component instance compiles at the highest version every constraint declared
// on it admits — the type its Aggregate writes, the Manifest key that maps it and
// the spec.implements of the Recipe that key names — on every surface that
// validates a Release, and where no version satisfies them all, every surface
// reports the one conflict in the same words.
//
// The workspaces are copies of the quickstart a published documentation example
// carries, over a catalog holding the server Component at 1.0.0 and 1.1.0 beside
// the Compose Recipes implementing each, as the hub holds them. The Manifest maps
// the server at 1.0.0 while the Aggregate names it with no version, so a build
// that compiled every bare type at the highest version held would compile the
// server at 1.1.0 and render it through a Recipe written for 1.0.0.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// instanceVersionsWorkspace lays the named workspace of the instance-version
// fixtures down under a fresh root, with the fixtures' catalog beside it as the
// workspace's one catalog directory, and returns the root. The workspace reads
// nothing from a registry.
func instanceVersionsWorkspace(t *testing.T, workspace string) string {
	t.Helper()
	root := t.TempDir()
	fixtures := filepath.Join("testdata", "instance_versions")
	if err := os.CopyFS(root, os.DirFS(filepath.Join(fixtures, workspace))); err != nil {
		t.Fatalf("copy the %s workspace: %v", workspace, err)
	}
	if err := os.CopyFS(filepath.Join(root, "catalog"), os.DirFS(filepath.Join(fixtures, "catalog"))); err != nil {
		t.Fatalf("copy the catalog: %v", err)
	}
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace:\n  catalog_dirs:\n    - ./catalog\noffline: true\n")
	return root
}

// pinServerAgainstNewerKey rewrites the quickstart workspace at root into its one
// fault: the Aggregate names the server at 1.0.0, and the Manifest maps the server
// at 1.1.0 to the Recipe implementing 1.1.0. Each side is consistent on its own —
// the key and its Recipe agree — and no version satisfies both.
func pinServerAgainstNewerKey(t *testing.T, root string) {
	t.Helper()
	replaceInFixture(t, filepath.Join(root, "aggregate.yaml"),
		"      type: core/compute/server\n", "      type: core/compute/server:1.0.0\n")
	replaceInFixture(t, filepath.Join(root, "manifest.yaml"),
		"core/compute/server:1.0.0: compose/server:1.0.0", "core/compute/server:1.1.0: compose/server:2.0.0")
}

// replaceInFixture replaces the one occurrence of old in the file at path with
// new, failing the test when the file does not hold old exactly once, so a
// fixture that stopped carrying the text cannot pass as the fault it was meant to
// carry.
func replaceInFixture(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if n := strings.Count(string(data), old); n != 1 {
		t.Fatalf("%s holds %q %d times, want once", path, old, n)
	}
	writeFixture(t, path, strings.Replace(string(data), old, new, 1))
}

// instanceVersionsCoreValidate validates the Release at root through the shared
// core the command, the daemon and the assistant tool each call, with the inputs
// the command builds, and returns the resolution beside the result.
func instanceVersionsCoreValidate(t *testing.T, root string) (*compilerops.ResolvedClosure, *compilerops.ValidateResult) {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	releasePath := filepath.Join(root, "release.yaml")
	deps, err := resolveReleaseDependencies(context.Background(), rw, releasePath, nil)
	if err != nil {
		t.Fatalf("resolve the Release's dependencies: %v", err)
	}
	resolved := deps.resolved
	return resolved, compilerops.Validate(context.Background(), compilerops.ValidateInputs{
		View:                     deps.view,
		Closure:                  resolved.Closure,
		Release:                  resolved.Release,
		ReleasePath:              releasePath,
		Manifest:                 resolved.Manifest,
		ManifestPath:             resolved.ManifestPath,
		AggBytes:                 resolved.AggBytes,
		Lockfile:                 deps.lockfile,
		ClosureLibrariesResolved: resolved.LibrariesResolved,
		LibraryRecord:            deps.lockfile,
		AggregatePath:            catalog.RelativeToWorkspace(rw.Root(), resolved.AggregatePath),
		WorkspaceRoot:            rw.Root(),
		WorkspaceConfig:          rw.Config(),
		CompilerVersion:          provenanceCompilerVersion(),
	})
}

// validateReleaseAt runs `aggrega validate --release` over the Release at root and
// returns the verdict beside what the command wrote to its diagnostic stream.
func validateReleaseAt(t *testing.T, root string) (error, string) {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	var diag, out bytes.Buffer
	verdict := validateRelease(context.Background(), rw, filepath.Join(root, "release.yaml"), nil,
		compilerops.VerbosityDetailed, false, false, &diag, &out)
	return verdict, diag.String()
}

// coreFindings is every blocking finding a validation result carries, as one
// line each, for a failure message.
func coreFindings(resolved *compilerops.ResolvedClosure, vr *compilerops.ValidateResult) []error {
	findings := append([]error(nil), resolved.ManifestConsistencyFindings...)
	findings = append(findings, vr.ClosureExtendsErrors...)
	for _, e := range []error{vr.PillarError, vr.VerdictTrustError, vr.RunErr} {
		if e != nil {
			findings = append(findings, e)
		}
	}
	if vr.Tier4 != nil {
		for _, ve := range vr.Tier4.ResolveDiagnostics {
			findings = append(findings, ve)
		}
		if vr.Tier4.Analysis != nil {
			for _, ae := range vr.Tier4.Analysis.Errors {
				findings = append(findings, ae.ToCompilerError())
			}
		}
	}
	findings = append(findings, vr.ResolutionFindings...)
	return append(findings, vr.InputKindErrors...)
}

// shortCodeOf is the short code a diagnostic naming condition opens with, read
// through the condition's own accessor.
func shortCodeOf(t *testing.T, condition error) string {
	t.Helper()
	code, ok := cerrors.ShortCodeOf(condition)
	if !ok {
		t.Fatalf("the condition %v carries no short code", condition)
	}
	return code
}

// TestValidateHoldsInstanceAtMappedVersion holds `aggrega validate --release` to
// compiling every instance at the version its constraints admit: the quickstart,
// whose Aggregate names the server with no version and whose Manifest maps it at
// 1.0.0, validates with the server compiled at 1.0.0 though 1.1.0 is held; a
// module's server, the only one its Release holds, is compiled at its mapped
// version too; and the quickstart with the server pinned at 1.0.0 against a key
// naming 1.1.0 halts with the conflict between the two, never with the
// mapping-consistency mismatch a Recipe judged against a version it was not
// written for raises.
func TestValidateHoldsInstanceAtMappedVersion(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name, workspace, node string
	}{
		{name: "the quickstart's bare server", workspace: "quickstart-web", node: "web"},
		{name: "a module's bare server", workspace: "quickstart-site", node: "site.web"},
	}
	for _, c := range valid {
		t.Run(c.name+" compiles at the version its mapping admits", func(t *testing.T) {
			t.Parallel()
			root := instanceVersionsWorkspace(t, c.workspace)

			resolved, vr := instanceVersionsCoreValidate(t, root)
			if findings := coreFindings(resolved, vr); len(findings) > 0 {
				t.Fatalf("the Release does not validate:\n%v", errors.Join(findings...))
			}
			if vr.Tier4 == nil || vr.Tier4.IRGraph == nil {
				t.Fatal("the validation built no graph")
			}
			node, ok := vr.Tier4.IRGraph.Spec.Nodes.Get(c.node)
			if !ok {
				t.Fatalf("the graph holds no node %q; it holds %v", c.node, vr.Tier4.IRGraph.Spec.Nodes.Keys())
			}
			if node.Component != "core/compute/server" || node.ResolvedVersion != "1.0.0" {
				t.Errorf("node %q compiled as %s at %q, want core/compute/server at 1.0.0", c.node, node.Component, node.ResolvedVersion)
			}

			if verdict, diag := validateReleaseAt(t, root); verdict != nil {
				t.Errorf("aggrega validate --release refused the Release: %v\n%s", verdict, diag)
			}
		})
	}

	t.Run("a pinned server against a newer key halts with the conflict", func(t *testing.T) {
		t.Parallel()
		root := instanceVersionsWorkspace(t, "quickstart-web")
		pinServerAgainstNewerKey(t, root)

		resolved, vr := instanceVersionsCoreValidate(t, root)
		if !errors.Is(vr.RunErr, catalog.ErrConflictingVersionConstraints()) {
			t.Fatalf("the validation's run error is %v; want the conflicting-version-constraints condition", vr.RunErr)
		}
		for _, f := range coreFindings(resolved, vr) {
			if errors.Is(f, resolver.ErrManifestImplementsMismatch()) {
				t.Errorf("the validation reports the mapping-consistency mismatch beside the conflict: %v", f)
			}
		}

		verdict, diag := validateReleaseAt(t, root)
		if verdict == nil {
			t.Fatalf("aggrega validate --release accepted the Release\n%s", diag)
		}
		if want := shortCodeOf(t, catalog.ErrConflictingVersionConstraints()); !strings.Contains(diag, want) {
			t.Errorf("the command's diagnostics do not open with %s:\n%s", want, diag)
		}
		if mismatch := shortCodeOf(t, resolver.ErrManifestImplementsMismatch()); strings.Contains(diag, mismatch) {
			t.Errorf("the command's diagnostics carry %s beside the conflict:\n%s", mismatch, diag)
		}
	})
}

// TestConflictParityAcrossSurfaces holds the command, the daemon's diagnostics and
// the assistant tool's validation to reporting a conflict between the versions an
// instance's constraints admit alike: one condition, read through its accessor,
// and one message carrying the same values — the Release, where the instance is
// declared, each constraint with the file it is written in, and the versions held.
func TestConflictParityAcrossSurfaces(t *testing.T) {
	root := instanceVersionsWorkspace(t, "quickstart-web")
	pinServerAgainstNewerKey(t, root)

	_, vr := instanceVersionsCoreValidate(t, root)
	var conflict *cerrors.CompilerError
	if !errors.Is(vr.RunErr, catalog.ErrConflictingVersionConstraints()) || !errors.As(vr.RunErr, &conflict) {
		t.Fatalf("the shared core reports %v; want the conflicting-version-constraints condition", vr.RunErr)
	}
	message := conflict.Message()
	for _, value := range []string{
		`Release "quickstart-web-local"`,
		`at ".spec.components.web"`,
		`instance "web"`,
		`type "core/compute/server:1.0.0", written in "aggregate.yaml"`,
		`Manifest mapping key "core/compute/server:1.1.0", written in "manifest.yaml"`,
		`mapped to recipe "compose/server:2.0.0", whose spec.implements is "core/compute/server:1.1.0"`,
		`catalog holds "1.0.0, 1.1.0"`,
	} {
		if !strings.Contains(message, value) {
			t.Errorf("the conflict does not name %s:\n%s", value, message)
		}
	}
	code, ok := cerrors.CodeOf(catalog.ErrConflictingVersionConstraints())
	if !ok {
		t.Fatal("the conflicting-version-constraints condition carries no code")
	}
	shortCode := shortCodeOf(t, catalog.ErrConflictingVersionConstraints())

	t.Run("the command", func(t *testing.T) {
		verdict, diag := validateReleaseAt(t, root)
		if verdict == nil {
			t.Fatalf("the command accepted the Release\n%s", diag)
		}
		if !strings.Contains(diag, shortCode) || !strings.Contains(diag, message) {
			t.Errorf("the command's diagnostics do not carry %s and the conflict's message %q:\n%s", shortCode, message, diag)
		}
	})

	t.Run("the daemon", func(t *testing.T) {
		diags := validateOverTheDaemonWire(t, root)
		var found bool
		for _, d := range diags {
			if d.Code != code {
				continue
			}
			found = true
			if d.ShortCode != shortCode || !strings.Contains(d.Message, message) {
				t.Errorf("the daemon's diagnostic carries %s and %q; want %s and the conflict's message %q",
					d.ShortCode, d.Message, shortCode, message)
			}
		}
		if !found {
			t.Errorf("the daemon reports no diagnostic of code %s: %+v", code, diags)
		}
	})

	t.Run("the assistant tool", func(t *testing.T) {
		payload := validateOverTheAssistantWire(t, root)
		if payload.Valid {
			t.Fatalf("the assistant tool reports the Release valid: %+v", payload)
		}
		var found bool
		for _, d := range payload.Diagnostics {
			if strings.Contains(d, shortCode) && strings.Contains(d, message) {
				found = true
			}
		}
		if !found {
			t.Errorf("no diagnostic of the assistant tool carries %s and the conflict's message %q: %q",
				shortCode, message, payload.Diagnostics)
		}
	})
}

// daemonDiagnosticReply is one diagnostic of the daemon's validation answer,
// decoded by field name rather than through the daemon's own type, so the
// comparison reads the wire a client binds to.
type daemonDiagnosticReply struct {
	Message   string `json:"message"`
	Code      string `json:"code"`
	ShortCode string `json:"shortCode"`
}

// validateOverTheDaemonWire starts the daemon over the workspace at root, asks it
// through its own framing to validate the Aggregate against the Release beside it,
// and returns the diagnostics of the answer.
func validateOverTheDaemonWire(t *testing.T, root string) []daemonDiagnosticReply {
	t.Helper()
	aggregatePath := filepath.Join(root, "aggregate.yaml")
	content, err := os.ReadFile(aggregatePath)
	if err != nil {
		t.Fatalf("read the Aggregate: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "aggrega/validate",
		"params": map[string]string{
			"workspaceRoot": root,
			"file":          aggregatePath,
			"content":       string(content),
			"release":       "release.yaml",
		},
	})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}
	var stdin bytes.Buffer
	fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
	stdin.Write(body)

	var stdout, stderr bytes.Buffer
	if rerr := serve.Run(context.Background(), serve.RunConfig{
		Stdin:           &stdin,
		Stdout:          &stdout,
		Stderr:          &stderr,
		WorkspaceRoot:   root,
		CompilerVersion: provenanceCompilerVersion(),
	}); rerr != nil {
		t.Fatalf("the daemon exited with an error: %v\nstderr: %s", rerr, stderr.String())
	}

	reply := answerFrame(t, stdout.Bytes())
	if raw, refused := reply["error"]; refused {
		t.Fatalf("aggrega/validate refused the request: %s\nstderr: %s", raw, stderr.String())
	}
	var result struct {
		Diagnostics []daemonDiagnosticReply `json:"diagnostics"`
	}
	if err := json.Unmarshal(reply["result"], &result); err != nil {
		t.Fatalf("decode the result: %v\nresult: %s", err, reply["result"])
	}
	return result.Diagnostics
}

// answerFrame returns the framed message of the daemon's output stream that
// answers the request it was sent, passing over the notifications the daemon
// pushes beside it.
func answerFrame(t *testing.T, out []byte) map[string]json.RawMessage {
	t.Helper()
	const header = "Content-Length: "
	for rest := out; ; {
		start := bytes.Index(rest, []byte(header))
		if start < 0 {
			t.Fatalf("the daemon answered nothing: %q", out)
		}
		frame := decodeOneFrame(t, rest[start:])
		if _, answers := frame["id"]; answers {
			return frame
		}
		rest = rest[start+len(header):]
	}
}

// assistantValidateReply is the assistant tool's validation payload, decoded by
// field name rather than through the tool's own type.
type assistantValidateReply struct {
	Valid       bool     `json:"valid"`
	Diagnostics []string `json:"diagnostics"`
}

// validateOverTheAssistantWire starts the tool protocol over the workspace at
// root, calls the validate tool on the Release there through its own message
// loop, and returns the structured payload of the answer.
func validateOverTheAssistantWire(t *testing.T, root string) assistantValidateReply {
	t.Helper()
	t.Chdir(root)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "validate",
			"arguments": map[string]string{"release": "release.yaml", "workspace_root": root},
		},
	})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}
	var stdin bytes.Buffer
	stdin.Write(body)
	stdin.WriteByte('\n')

	var stdout, stderr bytes.Buffer
	if rerr := mcp.Run(context.Background(), mcp.Config{
		Stdin:         &stdin,
		Stdout:        &stdout,
		Stderr:        &stderr,
		ServerVersion: provenanceCompilerVersion(),
	}); rerr != nil {
		t.Fatalf("the session exited with an error: %v\nstderr: %s", rerr, stderr.String())
	}
	var envelope struct {
		Result *struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	line := bytes.TrimSpace(stdout.Bytes())
	if err := json.Unmarshal(line, &envelope); err != nil {
		t.Fatalf("decode the reply: %v\nreply: %s", err, line)
	}
	if envelope.Error != nil || envelope.Result == nil {
		t.Fatalf("the validate tool was refused: %s\nstderr: %s", line, stderr.String())
	}
	var payload assistantValidateReply
	if err := json.Unmarshal(envelope.Result.StructuredContent, &payload); err != nil {
		t.Fatalf("decode the structured payload: %v\npayload: %s", err, envelope.Result.StructuredContent)
	}
	return payload
}

// TestAttachmentSlotsReadTheCompiledVersion holds the answer to which attachment
// slots a connection carries to the Component the connection's node was compiled
// from. The gateway names its type with no version and its Manifest maps it to a
// Recipe written for 1.0.0, so it compiles at 1.0.0, whose models socket declares
// a slot named credential; the catalog also holds 1.1.0, whose socket declares a
// slot named token. The daemon and the assistant tool, which share the answer,
// each name credential.
func TestAttachmentSlotsReadTheCompiledVersion(t *testing.T) {
	root := instanceVersionsWorkspace(t, "gateway-slots")

	resolved, vr := instanceVersionsCoreValidate(t, root)
	if findings := coreFindings(resolved, vr); len(findings) > 0 {
		t.Fatalf("the Release does not validate:\n%v", errors.Join(findings...))
	}
	if node, ok := vr.Tier4.IRGraph.Spec.Nodes.Get("gateway"); !ok || node.ResolvedVersion != "1.0.0" {
		t.Fatalf("the gateway compiled at %q; the case needs it compiled at 1.0.0", node.ResolvedVersion)
	}

	// The daemon draws a graph only for a Release carrying a lockfile. Nothing in
	// it pins a version the answer reads: every entity resolves from the catalog
	// directory.
	releasePath := filepath.Join(root, "release.yaml")
	if err := lockfile.WriteLockfile(lockfile.PathFor(releasePath), &lockfile.Lockfile{
		Version: 1, Release: "release.yaml", GeneratedAt: "2026-11-20T10:00:00Z",
	}); err != nil {
		t.Fatalf("write the lockfile: %v", err)
	}
	connection := map[string]string{"socket": "models", "alias": "gpt"}
	want := []string{"credential"}

	t.Run("the daemon", func(t *testing.T) {
		if got := attachmentSlotsOverTheDaemonWire(t, root, connection); !slices.Equal(got, want) {
			t.Errorf("the daemon answers the slots %q, want %q", got, want)
		}
	})
	t.Run("the assistant tool", func(t *testing.T) {
		if got := attachmentSlotsOverTheAssistantWire(t, root, connection); !slices.Equal(got, want) {
			t.Errorf("the assistant tool answers the slots %q, want %q", got, want)
		}
	})
}

// TestMappingTheEntryRuleRefusesIsReportedOnce holds a Manifest entry whose key
// and Recipe disagree with each other to the one report the rule over the
// mapping entry makes: the quickstart's server, written with no version, is
// mapped by the key core/compute/server:1.1.0 to the Recipe implementing 1.0.0.
// The entry is the fault, so the validation reports the mapping-consistency
// mismatch alone, never a conflict between the instance's constraints beside it,
// and the build of the same documents halts on the same one condition.
func TestMappingTheEntryRuleRefusesIsReportedOnce(t *testing.T) {
	t.Parallel()
	root := instanceVersionsWorkspace(t, "quickstart-web")
	replaceInFixture(t, filepath.Join(root, "manifest.yaml"),
		"core/compute/server:1.0.0: compose/server:1.0.0", "core/compute/server:1.1.0: compose/server:1.0.0")

	resolved, vr := instanceVersionsCoreValidate(t, root)
	findings := coreFindings(resolved, vr)
	if len(findings) != 1 || !errors.Is(findings[0], resolver.ErrManifestImplementsMismatch()) {
		t.Fatalf("the validation reports %d finding(s); want the mapping-consistency mismatch alone:\n%v",
			len(findings), errors.Join(findings...))
	}

	verdict, diag := validateReleaseAt(t, root)
	if verdict == nil {
		t.Fatalf("aggrega validate --release accepted the Release\n%s", diag)
	}
	mismatch := shortCodeOf(t, resolver.ErrManifestImplementsMismatch())
	if n := strings.Count(diag, mismatch); n != 1 {
		t.Errorf("the command's diagnostics carry %s %d times, want once:\n%s", mismatch, n, diag)
	}
	if conflict := shortCodeOf(t, catalog.ErrConflictingVersionConstraints()); strings.Contains(diag, conflict) {
		t.Errorf("the command's diagnostics carry %s beside the mismatch:\n%s", conflict, diag)
	}

	buildErr := instanceVersionsBuild(t, root)
	if !errors.Is(buildErr, resolver.ErrManifestImplementsMismatch()) || errors.Is(buildErr, catalog.ErrConflictingVersionConstraints()) {
		t.Errorf("the build halts with %v; want the mapping-consistency mismatch alone", buildErr)
	}
}

// instanceVersionsBuild runs the build's own computation over the Release at
// root — the preview every surface that emits an artifact runs, with every gate
// on — with no lockfile, and returns its refusal.
func instanceVersionsBuild(t *testing.T, root string) error {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	releasePath := filepath.Join(root, "release.yaml")
	dirs, err := rw.ResolveCatalogDirs(releasePath, nil)
	if err != nil {
		t.Fatalf("resolve the catalog directories: %v", err)
	}
	view := catalog.NewLazyLookup(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), dirs, nil)
	_, err = compilerops.PreviewForArtifact(context.Background(),
		compilerops.NewPreviewInputs(compilerops.NoExternalInputs(), view, releasePath, rw.Root(), rw.Config(),
			provenanceCompilerVersion()),
		pipeline.Options{SkipRender: true})
	return err
}

// attachmentSlotsReply is the part of an attach-candidates answer naming the
// slots a connection carries, decoded by field name from either surface's wire.
type attachmentSlotsReply struct {
	Slots []struct {
		Slot string `json:"slot"`
	} `json:"slots"`
}

// names returns the slot names the reply carries, in its order.
func (r attachmentSlotsReply) names() []string {
	out := make([]string, 0, len(r.Slots))
	for _, s := range r.Slots {
		out = append(out, s.Slot)
	}
	return out
}

// attachmentSlotsOverTheDaemonWire asks the daemon over the workspace at root,
// through its own framing, which slots the gateway's connection carries.
func attachmentSlotsOverTheDaemonWire(t *testing.T, root string, connection map[string]string) []string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "aggrega/attachCandidates",
		"params": map[string]any{
			"workspaceRoot": root,
			"graphNode":     "gateway",
			"release":       "release.yaml",
			"connection":    connection,
		},
	})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}
	var stdin bytes.Buffer
	fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
	stdin.Write(body)
	var stdout, stderr bytes.Buffer
	if rerr := serve.Run(context.Background(), serve.RunConfig{
		Stdin:           &stdin,
		Stdout:          &stdout,
		Stderr:          &stderr,
		WorkspaceRoot:   root,
		CompilerVersion: provenanceCompilerVersion(),
	}); rerr != nil {
		t.Fatalf("the daemon exited with an error: %v\nstderr: %s", rerr, stderr.String())
	}
	reply := answerFrame(t, stdout.Bytes())
	if raw, refused := reply["error"]; refused {
		t.Fatalf("aggrega/attachCandidates refused the request: %s\nstderr: %s", raw, stderr.String())
	}
	var result attachmentSlotsReply
	if err := json.Unmarshal(reply["result"], &result); err != nil {
		t.Fatalf("decode the result: %v\nresult: %s", err, reply["result"])
	}
	return result.names()
}

// attachmentSlotsOverTheAssistantWire calls the assistant's attach-candidates
// tool over the workspace at root, through its own message loop, for the slots
// the gateway's connection carries.
func attachmentSlotsOverTheAssistantWire(t *testing.T, root string, connection map[string]string) []string {
	t.Helper()
	t.Chdir(root)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "attach-candidates",
			"arguments": map[string]any{
				"release": "release.yaml", "graph_node": "gateway", "connection": connection, "workspace_root": root,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}
	var stdin bytes.Buffer
	stdin.Write(body)
	stdin.WriteByte('\n')
	var stdout, stderr bytes.Buffer
	if rerr := mcp.Run(context.Background(), mcp.Config{
		Stdin:         &stdin,
		Stdout:        &stdout,
		Stderr:        &stderr,
		ServerVersion: provenanceCompilerVersion(),
	}); rerr != nil {
		t.Fatalf("the session exited with an error: %v\nstderr: %s", rerr, stderr.String())
	}
	var envelope struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent json.RawMessage `json:"structuredContent"`
			IsError           bool            `json:"isError"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	line := bytes.TrimSpace(stdout.Bytes())
	if err := json.Unmarshal(line, &envelope); err != nil {
		t.Fatalf("decode the reply: %v\nreply: %s", err, line)
	}
	if envelope.Error != nil || envelope.Result == nil || envelope.Result.IsError {
		t.Fatalf("the attach-candidates tool was refused: %s\nstderr: %s", line, stderr.String())
	}
	var payload attachmentSlotsReply
	if err := json.Unmarshal(envelope.Result.StructuredContent, &payload); err != nil {
		t.Fatalf("decode the structured payload: %v\npayload: %s", err, envelope.Result.StructuredContent)
	}
	return payload.names()
}
