// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lintops

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The linter socket returns an accept-or-reject verdict and is not run by a build,
// so the lint paths are where its attachments are held to the record of the plugin
// identities trusted for them. Only the release-scoped pass can locate that record
// — it sits beside a Release — and these tests hold both halves of what it does
// with it: the recorded digests reach the socket's resolution, and a gate the
// configuration dropped refuses the pass.

// TestReleaseConventionsRefusesAGateTheConfigurationDropped: with a record beside
// the Release holding a plugin the configuration attaches nowhere, the
// release-scoped pass refuses. It refuses before the closure is resolved, which is
// deliberate — a dropped gate is a fault in the configuration rather than in any
// entity, so there is nothing to be learned by doing the closure work first — and
// it is what lets this fixture assert the refusal without a Release closure.
func TestReleaseConventionsRefusesAGateTheConfigurationDropped(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	releasePath := filepath.Join(root, "release.yaml")
	record := &pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{{
			Plugin:          "linter/house-style",
			ResolvedVersion: "0.3.0",
			Digest:          "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		}},
	}
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), record); err != nil {
		t.Fatalf("write trust record: %v", err)
	}

	err = RunRelease(t.Context(), rw, releasePath, nil, nil, io.Discard, io.Discard, nil)
	if err == nil {
		t.Fatal("a record holding a gate the configuration attaches nowhere must refuse the pass; got a clean run")
	}
	if !errors.Is(err, pluginstrust.ErrVerdictAttachmentNotConfigured()) {
		t.Fatalf("the refusal must name the not-configured condition; got %v", err)
	}
}

// TestReleaseConventionsWithoutARecordReachesTheClosure is the A/B that makes the
// refusal above attributable. Over the identical workspace with no record written,
// the pass gets past the record entirely and fails on the absent Release instead —
// so the refusal above came from the record rather than from the fixture.
func TestReleaseConventionsWithoutARecordReachesTheClosure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	err = RunRelease(t.Context(), rw, filepath.Join(root, "release.yaml"), nil, nil, io.Discard, io.Discard, nil)
	if err == nil {
		t.Fatal("the fixture has no Release, so the pass cannot succeed; the A/B would be vacuous")
	}
	if errors.Is(err, pluginstrust.ErrVerdictAttachmentNotConfigured()) {
		t.Errorf("an absent record refuses nothing and must not raise the not-configured condition; got %v", err)
	}
}

// TestConventionsPassHonoursADisagreeingRecordedDigest is the needle for the
// conventions pass's consumption of the recorded digests: a digest recorded for the
// configured linter attachment's plugin which the resolved bytes do not match must
// not resolve. Without the digests reaching the socket, the pass runs clean.
func TestConventionsPassHonoursADisagreeingRecordedDigest(t *testing.T) {
	rw := twoAttachmentWorkspace(t, oneLinterAttachment)
	entity := componentWithOneFinding(t)

	const wrong = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	trusted := pluginpath.PinMaps{
		Pins:        map[string]string{"linter/publishing": wrong},
		PinVersions: map[string]string{"linter/publishing": wrong},
	}

	dirs, named := workspaceStackOf(t, rw)
	err := lintConventionsFiles(t.Context(), rw, dirs, named, []string{entity}, conventionsRun{trusted: resolver.VerdictPluginTrust{PinMaps: trusted}}, io.Discard, io.Discard, nil)
	if err == nil {
		t.Fatal("a recorded digest the resolved bytes do not match must not resolve; got a clean pass")
	}
}

// TestConventionsPassStaysPinFreeWithoutRecordedDigests is the A/B: the identical
// workspace and entity with no recorded digests runs clean, so the halt above is
// earned by the digest rather than by the fixture.
func TestConventionsPassStaysPinFreeWithoutRecordedDigests(t *testing.T) {
	rw := twoAttachmentWorkspace(t, oneLinterAttachment)
	entity := componentWithOneFinding(t)

	dirs, named := workspaceStackOf(t, rw)
	if err := lintConventionsFiles(t.Context(), rw, dirs, named, []string{entity}, conventionsRun{trusted: resolver.VerdictPluginTrust{}}, io.Discard, io.Discard, nil); err != nil {
		t.Fatalf("no recorded digest must resolve exactly as before; got %v", err)
	}
}
