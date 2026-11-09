// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The release-scoped validate verb runs the same pre-render pipeline a build
// runs, and two of that pipeline's phases dispatch operator-attached guests. So
// this is a surface where the workspace configuration must be held to the record
// of the plugin identities trusted for its verdict attachments, and where the
// recorded digests must reach the verdict socket's resolution.
//
// These tests are the command-line leg. The daemon and the editor tool thread
// their inputs differently and carry their own legs; one of the three being
// covered is not evidence for the others.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const trustGateSocket = "linter"
const trustGateName = "house-style"
const trustGateVersion = "0.3.0"
const trustGateCoordinate = trustGateSocket + "/" + trustGateName
const trustGateURI = trustGateCoordinate + ":" + trustGateVersion
const trustGateDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// trustGatePluginEntity is a verdict-tier Plugin entity for the gate the record
// below names. It is attached at the linter socket, which is a verdict-tier socket
// the release-scoped validate pipeline never dispatches — so the attachment makes
// the plugin part of the verdict attachment set the record is compared against
// without requiring any guest bytes to exist. Presence, which is what the dropped-
// gate refusal is about, is decided before anything is loaded.
const trustGatePluginEntity = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: ` + trustGateName + `
  version: "` + trustGateVersion + `"
  pluginType: ` + trustGateSocket + `
spec:
  abiVersion: v1alpha1
  exports:
    - name: lint_rules
`

// writeTrustValidateWorkspace lays a clean release-scoped validate workspace and
// writes a workspace configuration attaching each URI in attach at the linter
// socket. It returns the workspace root, the catalog search stack, and the Release
// path the record sits beside.
//
// The fixture reuses the clean parity aggregate / manifest / release, so a run
// that produces any diagnostic at all produced it from the record rather than
// from the fixture — which is what makes the silence assertion meaningful.
func writeTrustValidateWorkspace(t *testing.T, attach ...string) (root string, dirs []string, releasePath string) {
	t.Helper()
	root = t.TempDir()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), cliBackendAggregate)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), cliCleanManifest)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, cliDefaultRelease)

	overlay := filepath.Join(root, "overlay")
	writeFixture(t,
		filepath.Join(overlay, "plugin", trustGateSocket, trustGateName, trustGateVersion, "plugin.yaml"),
		trustGatePluginEntity)

	cfg := "plugins:\n  linter:\n"
	if len(attach) == 0 {
		cfg = "plugins: {}\n"
	}
	for _, uri := range attach {
		cfg += "    - uri: " + uri + "\n"
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	return root, []string{overlay, cliFullstackCatalogDir(t)}, releasePath
}

// writeDroppedGateRecord writes a record beside releasePath holding the fixture
// gate's identity.
func writeDroppedGateRecord(t *testing.T, releasePath, digest string) {
	t.Helper()
	record := &pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{{
			Plugin:          trustGateCoordinate,
			ResolvedVersion: trustGateVersion,
			Digest:          digest,
		}},
	}
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), record); err != nil {
		t.Fatalf("write trust record: %v", err)
	}
}

// TestValidateReleaseRefusesAGateTheConfigurationDropped is the planted defect at
// the command-line leg: a record naming a gate the configuration attaches nowhere
// must refuse the validation, and the refusal must reach the caller as the verdict
// error rather than being computed and discarded.
func TestValidateReleaseRefusesAGateTheConfigurationDropped(t *testing.T) {
	root, dirs, releasePath := writeTrustValidateWorkspace(t)
	writeDroppedGateRecord(t, releasePath, trustGateDigest)

	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	var diag, out bytes.Buffer
	verr := validateRelease(context.Background(), rw, releasePath, dirs, compilerops.VerbosityDetailed, false, false, &diag, &out)
	if verr == nil {
		t.Fatalf("a record naming a gate the configuration attaches nowhere must refuse; got a clean pass\n out: %s", out.String())
	}
	if !errors.Is(verr, pluginstrust.ErrVerdictAttachmentNotConfigured()) {
		t.Fatalf("the refusal must name the not-configured condition; got %v", verr)
	}
	if !strings.Contains(verr.Error(), trustGateCoordinate) {
		t.Errorf("the refusal must name the dropped gate; got %v", verr)
	}
}

// TestValidateReleaseWithAMatchingConfigurationStaysSilent is the A/B that makes
// the refusal above attributable, and it is the silence half of the proof: over
// the identical fixture and the identical record, with the configuration still
// attaching the recorded gate, the validation passes clean. So the refusal above
// came from the disagreement rather than from the record merely being present.
func TestValidateReleaseWithAMatchingConfigurationStaysSilent(t *testing.T) {
	root, dirs, releasePath := writeTrustValidateWorkspace(t, trustGateURI)
	writeDroppedGateRecord(t, releasePath, trustGateDigest)

	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	assertRecordedGateIsIdentifiable(t, rw, dirs)

	var diag, out bytes.Buffer
	if verr := validateRelease(context.Background(), rw, releasePath, dirs, compilerops.VerbosityDetailed, false, false, &diag, &out); verr != nil {
		t.Fatalf("a record whose gate is still attached must refuse nothing; got %v\n diagOut: %s", verr, diag.String())
	}
}

// TestValidateReleaseHonoursADisagreeingRecordedDigest is the second half of what
// the record establishes, at this surface: holding the configuration to the record
// says every recorded gate is still attached, and carrying the recorded digests
// into resolution is what says the artifact acquired is the recorded one.
//
// The gate here is a governance attachment at the ir-verdict role, which the
// release-scoped validate pipeline DOES dispatch, and it is seeded into the
// content cache the way a completed pull leaves it. With a digest recorded for it
// that the cached manifest does not report, the socket's resolution refuses before
// it reads a byte of the guest.
func TestValidateReleaseHonoursADisagreeingRecordedDigest(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), cliBackendAggregate)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), cliCleanManifest)
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, cliDefaultRelease)

	// A cache dir inside the workspace, seeded the way a pull leaves a
	// verdict-socket plugin: entity blob, committing manifest, and the .wasm
	// addressed by its own content digest. The entity is NOT laid into the catalog
	// search stack, so the pinned entity-fronted cascade misses and resolution falls
	// through to the cache-not-pin read — which is the tier a recorded digest
	// governs.
	gate := verdicttest.SeedCached(t, root, filepath.Join(root, "unsearched-catalog"), "governance", "opa-gate", "1.2.0")

	// The record written below carries no signature fact, so the configuration
	// declares the hub's own skip: under the default registry's built-in
	// requirement the load would otherwise stop on the missing fact, and this pair
	// is about the recorded digest alone.
	cfg := "plugins:\n  governance:\n    - uri: " + gate.URI + "\n      role: " + string(plugintype.RoleIRVerdict) + "\n" + fakeRegistryTrust
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	if gate.WasmDigest == trustGateDigest {
		t.Fatalf("the fixture's real .wasm digest collides with the planted wrong one; the test would be vacuous")
	}
	writeRecordFor(t, releasePath, gate.Coordinate, "1.2.0", trustGateDigest)

	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	dirs := []string{cliFullstackCatalogDir(t)}

	var diag, out bytes.Buffer
	verr := validateRelease(context.Background(), rw, releasePath, dirs, compilerops.VerbosityDetailed, false, false, &diag, &out)
	if verr == nil {
		t.Fatalf("a recorded digest the cached manifest contradicts must not resolve; got a clean pass\n out: %s", out.String())
	}
	combined := diag.String() + verr.Error()
	if !strings.Contains(combined, resolver.ErrVerdictPluginTrustMismatch().Error()) {
		t.Fatalf("the halt must name the trust-mismatch condition; got verdict %v\n diagOut: %s", verr, diag.String())
	}
}

// TestValidateReleaseWithTheRecordedDigestResolvesTheGate is the A/B for the test
// above: over the identical fixture with the REAL cached digest recorded, the
// resolution raises no trust disagreement. It still fails — the header-only .wasm
// is not a loadable guest — so the assertion is that the failure is no longer the
// trust mismatch, which is what makes the halt above attributable to the digest
// rather than to the fixture.
func TestValidateReleaseWithTheRecordedDigestResolvesTheGate(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), cliBackendAggregate)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), cliCleanManifest)
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, cliDefaultRelease)

	gate := verdicttest.SeedCached(t, root, filepath.Join(root, "unsearched-catalog"), "governance", "opa-gate", "1.2.0")

	// The same stance as the test above, for the same reason: this row has to reach
	// the guest's load, which the missing signature fact would otherwise stop first.
	cfg := "plugins:\n  governance:\n    - uri: " + gate.URI + "\n      role: " + string(plugintype.RoleIRVerdict) + "\n" + fakeRegistryTrust
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	writeRecordFor(t, releasePath, gate.Coordinate, "1.2.0", gate.WasmDigest)

	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	var diag, out bytes.Buffer
	verr := validateRelease(context.Background(), rw, releasePath, []string{cliFullstackCatalogDir(t)}, compilerops.VerbosityDetailed, false, false, &diag, &out)
	if verr == nil {
		t.Fatalf("the seeded .wasm is header-only and cannot load, so the A/B would be vacuous; got a clean pass")
	}
	combined := diag.String() + verr.Error()
	if strings.Contains(combined, resolver.ErrVerdictPluginTrustMismatch().Error()) {
		t.Fatalf("an agreeing recorded digest must raise no trust disagreement; got verdict %v\n diagOut: %s", verr, diag.String())
	}
}

// writeRecordFor writes a record beside releasePath holding one entry.
func writeRecordFor(t *testing.T, releasePath, coordinate, version, digest string) {
	t.Helper()
	record := &pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{{
			Plugin:          coordinate,
			ResolvedVersion: version,
			Digest:          digest,
		}},
	}
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), record); err != nil {
		t.Fatalf("write trust record: %v", err)
	}
}

// assertRecordedGateIsIdentifiable guards the one way a silence assertion passes
// for the wrong reason. The comparison against the record is suppressed when an
// attachment's plugin cannot be identified — deliberately, because an
// unidentifiable attachment might be the very one accounting for a record entry —
// so a fixture whose Plugin entity does not resolve would produce silence with the
// record unenforced, which is indistinguishable from a match. This asserts both
// halves: the configuration attaches exactly the recorded gate, and that
// attachment's entity resolves to the coordinate the record is addressed by.
func assertRecordedGateIsIdentifiable(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, dirs []string) {
	t.Helper()
	attached := rw.Config().VerdictAttachments()
	if len(attached) != 1 || attached[0].Ref.URI != trustGateURI {
		t.Fatalf("fixture no longer attaches the recorded gate, so the silence would be vacuous; attachments %+v", attached)
	}
	view := catalog.NewLazyLookup(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), dirs, nil)
	entity, ok := view.PluginByURI(trustGateURI)
	if !ok {
		t.Fatalf("the attached gate's Plugin entity does not resolve, so the comparison would be suppressed rather than satisfied")
	}
	if got := entity.ArtifactReference(); got != trustGateCoordinate {
		t.Fatalf("the attached gate resolves to coordinate %q; the record is addressed by %q, so the silence would not be a match",
			got, trustGateCoordinate)
	}
}
