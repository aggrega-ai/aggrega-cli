// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	trustFixtureURI        = "linter/publishing:1.0.0"
	trustFixtureCoordinate = "linter/publishing"
	trustFixtureDigest     = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
)

// trustFixture writes a workspace holding one Plugin entity for a verdict-tier
// linter attachment, and returns a catalog view over it plus the Release path the
// trust record sits beside. The Release file itself is never read — the record's
// location is derived from the path, and these tests exercise the record rather
// than a build.
func trustFixture(t *testing.T) (*catalog.LazyLookup, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "catalog", "plugin", "linter", "publishing", "1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir catalog: %v", err)
	}
	entity := `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: publishing
  version: "1.0.0"
  pluginType: linter
spec:
  abiVersion: v1alpha1
  exports:
    - name: lint_rules
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(entity), 0o644); err != nil {
		t.Fatalf("write plugin entity: %v", err)
	}
	view := catalog.NewLazyLookup(root, "", []string{filepath.Join(root, "catalog")}, nil)
	return view, filepath.Join(root, "release.yaml")
}

// writeTrustRecord writes a record holding the fixture's plugin beside releasePath.
func writeTrustRecord(t *testing.T, releasePath string) string {
	t.Helper()
	path := pluginstrust.PathFor(releasePath)
	record := &pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{{
			Plugin:          trustFixtureCoordinate,
			ResolvedVersion: "1.0.0",
			Digest:          trustFixtureDigest,
		}},
	}
	if err := pluginstrust.Write(path, record); err != nil {
		t.Fatalf("write trust record: %v", err)
	}
	return path
}

// configAttaching returns a workspace config attaching the fixture's plugin at the
// linter socket, which is a verdict-tier socket.
func configAttaching(uris ...string) workspaceconfig.Config {
	cfg := workspaceconfig.Default()
	for _, uri := range uris {
		cfg.Plugins.Linter = append(cfg.Plugins.Linter, workspaceconfig.PluginRef{URI: uri})
	}
	return cfg
}

// TestVerdictPluginTrust_RefusesAnEmptyReleasePath: the record's path is derived
// from the Release path, so an unsupplied one is refused rather than defaulted. A
// default would read a file in whatever directory the process happens to be in,
// and finding none there would report a recorded workspace as unrecorded — which
// is the silent failure the composition exists to prevent.
func TestVerdictPluginTrust_RefusesAnEmptyReleasePath(t *testing.T) {
	view, _ := trustFixture(t)
	if _, err := VerdictPluginTrust(view, configAttaching(trustFixtureURI), ""); err == nil {
		t.Fatal("an unsupplied Release path must be refused; got a clean empty result")
	}
}

// TestVerdictPluginTrust_AbsentRecordYieldsNoPinsAndNoRefusal: a workspace that
// has recorded nothing resolves exactly as it did before the record existed.
func TestVerdictPluginTrust_AbsentRecordYieldsNoPinsAndNoRefusal(t *testing.T) {
	view, releasePath := trustFixture(t)
	pins, err := VerdictPluginTrust(view, configAttaching(trustFixtureURI), releasePath)
	if err != nil {
		t.Fatalf("an absent record is a first-run state, not a fault; got %v", err)
	}
	if len(pins.Pins) != 0 {
		t.Errorf("an absent record pins nothing; got %+v", pins.Pins)
	}
}

// TestVerdictPluginTrust_SuppliesTheRecordedDigest is the wiring proof: a record on
// disk becomes the pin map a resolution verifies against, keyed by the plugin's
// identity coordinate rather than by the authored reference.
func TestVerdictPluginTrust_SuppliesTheRecordedDigest(t *testing.T) {
	view, releasePath := trustFixture(t)
	writeTrustRecord(t, releasePath)

	pins, err := VerdictPluginTrust(view, configAttaching(trustFixtureURI), releasePath)
	if err != nil {
		t.Fatalf("a record whose plugin is still attached must not refuse; got %v", err)
	}
	if got := pins.Pins[trustFixtureCoordinate]; got != trustFixtureDigest {
		t.Errorf("the recorded digest must reach the pin map under the identity coordinate; got %q for %q (map %+v)",
			got, trustFixtureCoordinate, pins.Pins)
	}
}

// TestVerdictPluginTrust_RefusesWhenTheConfigurationDroppedEveryGate is the case
// the record exists for, and the one a per-surface assignment loses silently: the
// configuration attaches no verdict plugin at all while the record holds one.
// Nothing resolves, so nothing else in the run has anything to complain about.
func TestVerdictPluginTrust_RefusesWhenTheConfigurationDroppedEveryGate(t *testing.T) {
	view, releasePath := trustFixture(t)
	writeTrustRecord(t, releasePath)

	_, err := VerdictPluginTrust(view, configAttaching(), releasePath)
	if err == nil {
		t.Fatal("a configuration attaching no verdict plugin over a non-empty record must refuse; got a clean pass")
	}
	if !errors.Is(err, pluginstrust.ErrVerdictAttachmentNotConfigured()) {
		t.Errorf("the refusal must name the not-configured condition; got %v", err)
	}
}

// TestApplyGovernanceComposition_SuppliesBothHalvesInOneCall holds the structural
// guarantee the single composition exists for: one call sets the attachment list
// the pipeline dispatches AND the digests the verdict sockets verify against, so a
// build surface cannot acquire one without the other.
//
// Both halves are asserted over one call deliberately. Asserted separately, a
// composition that supplied the attachments and silently dropped the digests would
// pass the attachment assertion, and the dropped digests would be an empty map —
// which is a legal value meaning nothing was recorded.
func TestApplyGovernanceComposition_SuppliesBothHalvesInOneCall(t *testing.T) {
	view, releasePath := trustFixture(t)
	writeTrustRecord(t, releasePath)

	cfg := configAttaching(trustFixtureURI)
	// A live transform-role attachment the lockfile does not pin, so the
	// reconstruction is observable: it must not survive. A live verdict-role
	// attachment, which must.
	cfg.Plugins.Governance = []workspaceconfig.PluginRef{
		{URI: "governance/tagger:1.0.0", Role: plugintype.RoleLabelTransform},
		{URI: "governance/opa-gate:1.0.0", Role: plugintype.RoleIRVerdict},
	}

	inputs := pipeline.Inputs{WorkspaceConfig: cfg, Lockfile: &lockfile.Lockfile{}}
	if err := ApplyGovernanceComposition(&inputs, view, releasePath); err != nil {
		t.Fatalf("ApplyGovernanceComposition: %v", err)
	}

	if got := inputs.VerdictPluginTrust.Pins[trustFixtureCoordinate]; got != trustFixtureDigest {
		t.Errorf("the recorded digest half is missing: got %q for %q (map %+v)",
			got, trustFixtureCoordinate, inputs.VerdictPluginTrust.Pins)
	}
	governance := inputs.WorkspaceConfig.Plugins.Governance
	if len(governance) != 1 || governance[0].URI != "governance/opa-gate:1.0.0" {
		t.Errorf("the attachment half is wrong: a lockfile pinning no transform must leave only the verdict attachment; got %+v", governance)
	}
}

// TestBuildToBufferRefusesABuildWithNoReleasePath holds the same guarantee at the
// surface the daemon and the editor's build tool share. A build there with no
// Release path could not locate the record, so it refuses rather than building
// with every configured gate unheld.
func TestBuildToBufferRefusesABuildWithNoReleasePath(t *testing.T) {
	view, releasePath, workspaceRoot, wsCfg := fixtureWorkspace(t)
	ctx := context.Background()

	resolved, cerr := ResolveClosure(ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   workspaceRoot,
		WorkspaceConfig: wsCfg,
	})
	if cerr != nil {
		t.Fatalf("ResolveClosure: stage=%d %v", cerr.Stage, cerr)
	}

	if _, err := BuildToBuffer(ctx, BuildInputs{
		View:                             view,
		Resolved:                         resolved,
		WorkspaceConfig:                  wsCfg,
		WorkspaceRoot:                    workspaceRoot,
		CompilerVersion:                  "test",
		TolerateCatalogViewInconsistency: true,
	}, pipeline.Options{SkipRender: true}); err == nil {
		t.Fatal("a build with no Release path cannot locate the record and must refuse; got a clean build")
	}
}

// TestValidateRefusesWithNoReleasePath holds the guarantee at the validation
// surface that BuildToBuffer holds at the build surface: a validation that cannot
// locate the record refuses rather than running the pipeline — whose topology and
// static-analysis phases dispatch operator-attached guests — with every configured
// gate unheld. The refusal lands on the result's own discriminant so each front end
// maps it in gate order rather than reading it as a pipeline failure.
func TestValidateRefusesWithNoReleasePath(t *testing.T) {
	view, releasePath, workspaceRoot, wsCfg := fixtureWorkspace(t)
	ctx := context.Background()

	resolved, cerr := ResolveClosure(ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   workspaceRoot,
		WorkspaceConfig: wsCfg,
	})
	if cerr != nil {
		t.Fatalf("ResolveClosure: stage=%d %v", cerr.Stage, cerr)
	}

	vr := Validate(ctx, ValidateInputs{
		View:            view,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   resolved.AggregatePath,
		WorkspaceConfig: wsCfg,
		CompilerVersion: "test",
	})
	if vr.VerdictTrustError == nil {
		t.Fatal("a validation with no Release path cannot locate the record and must refuse; got a clean gate")
	}
	if vr.Tier4 != nil || vr.RunErr != nil {
		t.Errorf("the refusal must stop validation before the pipeline; Tier4=%+v RunErr=%v", vr.Tier4, vr.RunErr)
	}
}

// TestValidateWithAnAbsentRecordRunsTheWholePipeline is the absence half, and it is
// the one an editor depends on: a workspace that has never resolved a verdict
// attachment has no record, which is a first-run state rather than a fault, so the
// validation must run exactly as it did before the record existed. A refusal here
// would make the editor unusable on a fresh checkout.
func TestValidateWithAnAbsentRecordRunsTheWholePipeline(t *testing.T) {
	view, releasePath, workspaceRoot, wsCfg := fixtureWorkspace(t)
	ctx := context.Background()

	if _, err := os.Stat(pluginstrust.PathFor(releasePath)); !os.IsNotExist(err) {
		t.Fatalf("the fixture has acquired a record, so the absence case would be vacuous: stat %v", err)
	}

	resolved, cerr := ResolveClosure(ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   workspaceRoot,
		WorkspaceConfig: wsCfg,
	})
	if cerr != nil {
		t.Fatalf("ResolveClosure: stage=%d %v", cerr.Stage, cerr)
	}

	vr := Validate(ctx, ValidateInputs{
		View:            view,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		ReleasePath:     releasePath,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   resolved.AggregatePath,
		WorkspaceConfig: wsCfg,
		CompilerVersion: "test",
	})
	if vr.VerdictTrustError != nil {
		t.Fatalf("an absent record is a first-run state, not a fault; got %v", vr.VerdictTrustError)
	}
	if vr.Tier4 == nil || vr.Tier4.IRGraph == nil {
		t.Fatalf("validation must run the whole pipeline over an unrecorded workspace: %+v", vr.Tier4)
	}
}
