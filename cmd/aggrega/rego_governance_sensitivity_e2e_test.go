// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's end-to-end proofs that an operator-authored Rego policy can
// refuse a build over the per-authored-leaf sensitivity marking, and that the
// identity of the plugin which reached that verdict is held to the record beside
// the lockfile.
//
// Both halves run through the real pull -> build path over a self-contained
// fixture catalog, and both are proven by a build's observable outcome rather
// than by inspecting guest output: a refusal stops the build and carries the
// policy's own sentence, and a permitted build writes its rendered artifact.
//
// Every arm moves exactly ONE variable away from its neighbour, because the
// policy is a real one rather than a reflex: a rule that refused everything
// would satisfy each refusal assertion here and fail every paired pass.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The two authored leaves the fixture attachment writes a value at. Both are
// named because the assertions below need both: one is the coordinate a refusal
// must report, the other the coordinate a refusal must never report. Stated as
// two bare literals the pair would read as one fact repeated rather than as the
// discrimination it is.
//
// The Feature declares literalPassword sensitive and deliberately leaves
// secretName unmarked — a locator is the public half of a referenced
// credential — so which of the two a refusal names is the whole test of whether
// the marking follows the declarations or reaches past them.
const (
	sensitivityDeclaredLeaf = "values.literalPassword"
	sensitivityUnmarkedLeaf = "values.secretName"
)

// sensitivityPolicy refuses a build that writes a datum the catalog declares
// sensitive into a Feature attachment's own authored payload.
//
// It reads the per-authored-leaf marking and nothing coarser. The
// whole-attachment answer is deliberately not consulted: that answer is true
// whenever the attached Feature declares sensitivity anywhere, so a rule keyed on
// it would also refuse the permitted arm below — which is exactly what that arm
// exists to catch.
const sensitivityPolicy = `package house.governance

import rego.v1

deny contains msg if {
	some node_id, attachments in input.graph.spec.feature_values_mapping
	some attachment_name, attachment in attachments
	some leaf in attachment.sensitive_authored_leaves
	msg := sprintf("node %v attachment %v writes declared-sensitive material by value at authored leaf %v", [node_id, attachment_name, leaf])
}
`

// sensitivityFeature renders a credential Feature at name. marked decides whether
// the by-value half of the credential is declared sensitive; the locator half is
// unmarked either way.
//
// The two spellings differ in exactly the `sensitive` declarations, so the
// marking-absent arm below differs from the refusing arm in the declaration and
// in nothing else — not in the value authored, not in the delivery wiring.
func sensitivityFeature(name string, marked bool) string {
	byValue := "      literalPassword: { type: string }\n"
	provided := "      password: { type: string, required: true }\n"
	if marked {
		byValue = "      literalPassword: { type: string, sensitive: true }\n"
		provided = "      password: { type: string, required: true, sensitive: true }\n"
	}
	return `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: ` + name + `
  version: "1.0.0"
spec:
  schema:
    values:
      secretName: { type: string }
` + byValue + `    provides:
      name: { type: string, required: true }
` + provided
}

// sensitivityFeatureRecipe renders the compose implementation of the Feature at
// featureName. It renders no resource of its own; its whole job is to fulfil the
// Feature's provides contract so the attachment's delivery mapping has something
// to deliver.
func sensitivityFeatureRecipe(recipeName, featureName string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: ` + recipeName + `
  version: "1.0.0"
spec:
  capability: compose
  implements: ` + featureName + `
  provides:
    name: "{{ .values.secretName }}"
    password: "credential-from-the-feature"
`
}

// sensitivityAggregate renders an Aggregate attaching featureType at the
// Component's one declared slot. byValue decides whether a value is authored at
// the by-value leaf.
//
// THE ORDER OF THE TWO LEAVES IS LOAD-BEARING. Keep secretName first. A refusal
// names the FIRST marked authored leaf, so with the unmarked locator written
// first, a marking that reached past the declarations would surface it there and
// the assertion that this leaf stays unnamed would fire. Swap them and that guard
// goes quiet: the declared leaf would occupy the reported position either way,
// and a marking applied to everything would look exactly like one applied
// correctly. Nothing in the schema enforces this order, which is why it is
// written down here rather than left for a reader to infer.
func sensitivityAggregate(name, featureType string, byValue bool) string {
	leaves := "            secretName: db-creds\n"
	if byValue {
		leaves += "            literalPassword: password-in-the-clear\n"
	}
	return `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: ` + name + `
  version: "1.0.0"
spec:
  components:
    app:
      type: gov/app
      values:
        appName: demo
      features:
        credentials:
          type: ` + featureType + `
          values:
` + leaves + `          inject:
            env:
              - in: DATABASE_PASSWORD
                from: name
`
}

// sensitivityRelease renders a Release deploying the Aggregate file at
// aggregateFile into its own output directory, so two arms of one proof cannot
// read each other's rendered artifacts.
func sensitivityRelease(name, aggregateFile, outDir string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: ` + name + `
  environment: local
  version: "1.0.0"
spec:
  aggregate: ./` + aggregateFile + `
  manifest: ./manifest.yaml
  boundaries:
    cluster:
      directory: ./` + outDir + `
      components:
        app: {}
`
}

// sensitivityArm names one arm of the fixture: the Aggregate it deploys, whether
// that Aggregate authors a value at the by-value leaf, and whether the Feature it
// attaches declares that leaf sensitive.
type sensitivityArm struct {
	name        string
	featureType string
	byValue     bool
}

// sensitivityArms are the three arms, each one variable from its neighbour: the
// refusing arm authors a value at a declared-sensitive leaf; the permitted arm
// drops that value and keeps everything else, including the Feature's
// declarations, so the attachment is still sensitive at the whole-attachment
// answer; the marking-absent arm restores the value and attaches a Feature that
// declares nothing sensitive.
var sensitivityArms = []sensitivityArm{
	{name: "refused", featureType: "feature.gov/db-credential", byValue: true},
	{name: "permitted", featureType: "feature.gov/db-credential", byValue: false},
	{name: "unmarked", featureType: "feature.gov/db-credential-unmarked", byValue: true},
}

// sensitivityWorkspace lays a self-contained workspace carrying the fixture
// catalog, the policy, and one Aggregate + Release pair per arm. It returns the
// workspace root and the catalog directory.
//
// The catalog is self-contained rather than layered over the standard library:
// a proof that skips when the standard library is absent is a weaker proof than
// one that always runs, and nothing here needs a published entity.
func sensitivityWorkspace(t *testing.T) (root, catalogDir string) {
	t.Helper()
	root = t.TempDir()
	catalogDir = filepath.Join(root, "catalog")

	writeFixture(t, filepath.Join(catalogDir, "components", "app.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: gov/app
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
  featuresSchema:
    credentials:
      required: false
      inject: ["env"]
      description: "The credential this application reads its database password from."
  schema:
    values:
      appName: { type: string }
    provides:
      host: { type: string, required: true }
`)
	writeFixture(t, filepath.Join(catalogDir, "recipes", "app-recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: gov-app-recipe
  version: "1.0.0"
spec:
  capability: compose
  implements: gov/app
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: app:1.0
        variables:
          APP_NAME: '{{ .values.appName }}'
          {{- if and .context.inject .context.inject.env }}
          DATABASE_PASSWORD: '{{ .context.inject.env.DATABASE_PASSWORD }}'
          {{- end }}
        ports:
          p: 8080
    services:
      app:
        type: ClusterIP
        ports:
          p: 8080
`)
	writeFixture(t, filepath.Join(catalogDir, "features", "db-credential.yaml"),
		sensitivityFeature("feature.gov/db-credential", true))
	writeFixture(t, filepath.Join(catalogDir, "features", "db-credential-unmarked.yaml"),
		sensitivityFeature("feature.gov/db-credential-unmarked", false))
	writeFixture(t, filepath.Join(catalogDir, "feature-recipes", "db-credential.yaml"),
		sensitivityFeatureRecipe("gov-db-credential-compose", "feature.gov/db-credential"))
	writeFixture(t, filepath.Join(catalogDir, "feature-recipes", "db-credential-unmarked.yaml"),
		sensitivityFeatureRecipe("gov-db-credential-unmarked-compose", "feature.gov/db-credential-unmarked"))
	writeFixture(t, filepath.Join(catalogDir, "plugins", "compose.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: compose
  version: "1.0.0"
  pluginType: provider
  description: Rendering engine for the compose target.
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
      - compose
  exports:
    - name: render_node
    - name: assemble_boundary
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)

	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: sensitivity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        gov/app: gov-app-recipe:1.0.0
    features:
      type:
        feature.gov/db-credential: gov-db-credential-compose:1.0.0
        feature.gov/db-credential-unmarked: gov-db-credential-unmarked-compose:1.0.0
`)
	writeFixture(t, filepath.Join(root, "policy.rego"), sensitivityPolicy)

	for _, arm := range sensitivityArms {
		writeFixture(t, filepath.Join(root, "aggregate-"+arm.name+".yaml"),
			sensitivityAggregate("sensitivity-"+arm.name, arm.featureType, arm.byValue))
		writeFixture(t, filepath.Join(root, "release-"+arm.name+".yaml"),
			sensitivityRelease("sensitivity-"+arm.name+"-release", "aggregate-"+arm.name+".yaml", "out-"+arm.name))
	}
	return root, catalogDir
}

// TestRegoEmbedded_AuthoredLeafSensitivityRefusesAndPermits is the paired proof
// that an operator-authored policy can refuse a build over the per-authored-leaf
// sensitivity marking, and that the same policy lets its controls through.
//
// Four assertions carry the attribution on the refusing arm. The refusal must
// NAME the declared-sensitive authored leaf, which it can only do by reading the
// per-leaf marking off the wire. It must NOT name the OTHER leaf the same
// attachment authors a value at: the Feature declares nothing about that leaf, so
// a marking that reached past the declarations would name it, the build would be
// refused over material its author left readable, and every presence assertion
// would still be satisfied. Marking too widely is the failure with no cheap
// undo — it declares public data secret and then refuses builds that should
// pass — so the refusal has to show the unmarked leaf staying unmarked in the
// same breath as it names the marked one.
//
// The permitted arm is what distinguishes a working rule from one that refuses
// everything, and it is not simply an attachment with nothing sensitive about it:
// the attachment is still sensitive at the whole-attachment answer there, because
// that answer reads the Feature's declarations and this arm does not touch them.
// A rule keyed on the roll-up rather than on the per-leaf marking would refuse
// this arm.
//
// The marking-absent arm is what shows the refusal is keyed on the DECLARATION
// rather than on the authored value: it restores the value the refusing arm
// carries and attaches a Feature that declares nothing sensitive. Its rendered
// artifact is inspected for the delivered value, so the arm cannot pass by having
// rendered nothing.
func TestRegoEmbedded_AuthoredLeafSensitivityRefusesAndPermits(t *testing.T) {
	nonInteractive(t)

	t.Run("a declared-sensitive authored leaf is refused", func(t *testing.T) {
		root, catalogDir := sensitivityWorkspace(t)
		err := pullThen(t, root, filepath.Join(root, "release-refused.yaml"), []string{catalogDir},
			regoSensitivityConfig(t, catalogDir), "dist-refused")
		if err == nil {
			t.Fatal("a value authored at a declared-sensitive leaf must be refused")
		}
		if !errors.Is(err, analyzer.ErrPolicyViolation()) {
			t.Fatalf("want ErrPolicyViolation, got: %v", err)
		}
		msg := err.Error()
		if !strings.Contains(msg, `authored leaf `+sensitivityDeclaredLeaf) {
			t.Errorf("the refusal must name the declared-sensitive leaf %q, which is the only way it could have read the per-leaf marking; got: %v",
				sensitivityDeclaredLeaf, msg)
		}
		if strings.Contains(msg, sensitivityUnmarkedLeaf) {
			t.Errorf("the refusal names %q, a leaf this attachment authors a value at but the Feature declares nothing about; "+
				"the marking joins declaration to declaration, so a refusal reaching this leaf means the marking was applied "+
				"more widely than any author asked for; got: %v", sensitivityUnmarkedLeaf, msg)
		}
	})

	for _, arm := range []string{"permitted", "unmarked"} {
		t.Run("the "+arm+" arm builds", func(t *testing.T) {
			root, catalogDir := sensitivityWorkspace(t)
			outDir := "dist-" + arm
			if err := pullThen(t, root, filepath.Join(root, "release-"+arm+".yaml"), []string{catalogDir},
				regoSensitivityConfig(t, catalogDir), outDir); err != nil {
				t.Fatalf("the %s arm must not be refused: %v", arm, err)
			}
			// The build produced output, so the policy really was evaluated and
			// reached no refusal rather than the build having rendered nothing.
			if found, tree := distTreeContains(t, filepath.Join(root, outDir), "DATABASE_PASSWORD"); !found {
				t.Fatalf("the %s arm produced no rendered output:\n%s", arm, tree)
			}
		})
	}
}

// regoSensitivityConfig writes the embedded Rego guest's Plugin entity into the
// fixture catalog and returns the workspace configuration attaching it over the
// build graph. The entity is what the closure walk pins; the .wasm resolves
// through the embed tier by the bare coordinate.
func regoSensitivityConfig(t *testing.T, catalogDir string) string {
	t.Helper()
	writeRegoPluginEntity(t, catalogDir)
	return regoAttachment("ir-verdict", "policy.rego", "")
}

// housePolicyEntity is a Plugin entity for the Rego guest published under an
// identity this binary does not carry, so its artifact cannot resolve from inside
// the binary and the resolution falls through to the content cache.
//
// It declares the exports the guest's own self-report declares, because the host
// refuses to call an export a plugin did not declare and cross-checks the entity
// against the artifact it resolved.
const housePolicyEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: house-policy
  version: "1.0.0"
  pluginType: governance
spec:
  abiVersion: v1alpha1
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
  exports:
    - name: verdict_ir
    - name: verdict_render
    - name: mutate
    - name: transform_annotations
`

// The two record-selection sentinels the trust table below uses in place of a
// literal digest. They are deliberately not digest-shaped, so neither can be
// mistaken for one and neither can collide with a real content digest.
const (
	// recordNothing writes no record at all — the first-run state.
	recordNothing = "(no record)"
	// recordTheResolvedDigest records whatever digest the seeded artifact
	// actually hashes to, which the table cannot know before the seeding runs.
	recordTheResolvedDigest = "(the resolved digest)"
)

// TestRegoCachedGate_TheRecordedDigestGovernsWhichArtifactRuns probes the record
// beside the lockfile against a gate that genuinely runs: a real Rego guest,
// seeded into the content cache under an identity this binary does not carry, so
// the pinned entity-fronted cascade misses and resolution falls through to the
// cache-not-pin read — the one tier a recorded digest governs.
//
// A real guest rather than a placeholder artifact is what makes the three
// directions separable. With a placeholder the agreeing and absent arms fail for
// their own reason — the bytes are not a loadable guest — so neither can show the
// resolution completing. Here they both reach the guest's own verdict, which is a
// different diagnostic from the refusal, so the refusing arm is attributable to
// the recorded digest and to nothing else.
//
// Three directions, and all three are needed. A disagreeing record refuses before
// a byte of the guest is read. An agreeing record refuses nothing, so the refusal
// is keyed on the disagreement rather than on a record being present at all. An
// ABSENT record refuses nothing either, so an unrecorded gate is a first-run
// state rather than a pin of zero.
func TestRegoCachedGate_TheRecordedDigestGovernsWhichArtifactRuns(t *testing.T) {
	nonInteractive(t)

	// The guest bytes this binary carries for the embedded Rego identity, re-served
	// under a different identity. Reading them from the binary rather than
	// compiling a fixture is what makes the seeded artifact a real, loadable guest.
	guest, carried := embedded.Lookup(regoGovernanceURI)
	if !carried {
		t.Fatalf("this binary carries no %s guest, so there is no loadable artifact to seed", regoGovernanceURI)
	}

	// A digest of the right shape that no artifact in this fixture hashes to. The
	// guard below asserts that rather than trusting it.
	const plantedDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"

	for _, tc := range []struct {
		name string
		// recorded is the digest to record for the gate. The two sentinel values
		// below select a digest the fixture only learns at seeding time, so a row
		// never carries a digest it could not have known.
		recorded string
		// refusedOnTrust says whether the build must stop on the trust
		// disagreement. When false it must stop on the guest's own verdict
		// instead, which is what shows the gate resolved and ran.
		refusedOnTrust bool
	}{
		{name: "a disagreeing recorded digest refuses the gate", recorded: plantedDigest, refusedOnTrust: true},
		{name: "an agreeing recorded digest resolves the gate", recorded: recordTheResolvedDigest, refusedOnTrust: false},
		{name: "no record at all resolves the gate", recorded: recordNothing, refusedOnTrust: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, catalogDir := sensitivityWorkspace(t)
			releasePath := filepath.Join(root, "release-refused.yaml")

			// The entity goes into a directory that is NOT on the catalog search
			// stack, so the entity-fronted cascade misses it and resolution reaches
			// the cache.
			gate := seedCachedVerdictPluginArtifact(t, root, filepath.Join(root, "unsearched-catalog"),
				"governance", "house-policy", "1.0.0", housePolicyEntity, guest)
			if gate.WasmDigest == plantedDigest {
				t.Fatalf("the seeded artifact hashes to the planted digest, so the arms would be indistinguishable")
			}

			// A pull cannot itself acquire a cache-only gate — it resolves the
			// artifact through the catalog, which deliberately does not carry the
			// entity — so the lockfile is filled with the gate detached. That is the
			// state a completed pull from a registry leaves behind.
			pullWorkspace := resolveWorkspaceWithConfig(t, root, "workspace: {}\noffline: true\nplugins: {}\n")
			if err := pullRun(pullConfig{
				ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, ws: pullWorkspace,
				DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true},
			}); err != nil {
				t.Fatalf("pull: %v", err)
			}

			switch tc.recorded {
			case recordNothing:
				// A pull that acquired no verdict-socket plugin writes no record, so
				// the absence is asserted rather than assumed: a record left behind by
				// the pull above would make this the agreeing direction under another
				// name.
				if _, err := os.Stat(pluginstrust.PathFor(releasePath)); !os.IsNotExist(err) {
					t.Fatalf("this direction needs no record to exist; stat reported %v", err)
				}
			case recordTheResolvedDigest:
				writeRecordFor(t, releasePath, gate.Coordinate, "1.0.0", gate.WasmDigest)
			default:
				writeRecordFor(t, releasePath, gate.Coordinate, "1.0.0", tc.recorded)
			}

			buildWorkspace := resolveWorkspaceWithConfig(t, root,
				governanceAttachment(gate.URI, "ir-verdict", "policy.rego", ""))
			err := dispatchBuildWithDiag(buildWorkspace, releasePath, []string{catalogDir}, false,
				buildDiagOptions{quiet: true}, buildops.RunOptions{OutDir: filepath.Join(root, "dist")},
				os.Stderr, os.Stderr)
			if err == nil {
				t.Fatal("the fixture Aggregate authors a declared-sensitive leaf, so every direction stops the build; " +
					"a clean build means the gate did not run and the arms would be indistinguishable")
			}
			onTrust := errors.Is(err, resolver.ErrVerdictPluginTrustMismatch())
			onVerdict := errors.Is(err, analyzer.ErrPolicyViolation())
			switch {
			case tc.refusedOnTrust && !onTrust:
				t.Fatalf("a recorded digest the cached manifest contradicts must refuse on the trust disagreement; got: %v", err)
			case !tc.refusedOnTrust && onTrust:
				t.Fatalf("this direction records no disagreement, so nothing may refuse on trust; got: %v", err)
			case !tc.refusedOnTrust && !onVerdict:
				t.Fatalf("the gate must resolve and reach its own verdict here; got: %v", err)
			}
			if tc.refusedOnTrust {
				// Both digests are named, because the remediation is to decide which
				// of the two artifacts is the intended one.
				for _, want := range []string{plantedDigest, gate.WasmDigest, gate.Coordinate} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal must name %q so a reader can act on it; got: %v", want, err)
					}
				}
				return
			}
			// The guest's own sentence, which only the seeded artifact could have
			// produced. Matching the condition alone would be satisfied by any
			// verdict from any plugin, so it would not show that THIS artifact ran.
			if !strings.Contains(err.Error(), "authored leaf "+sensitivityDeclaredLeaf) {
				t.Errorf("the halt must carry the seeded guest's own verdict sentence, which is what shows the cached artifact "+
					"resolved and ran; got: %v", err)
			}
		})
	}
}
