// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// withdrawalAggregateFixture is the Aggregate arm of the per-kind corpus. Its one
// component names the Component fixture below, so the topology pass this kind's
// inspector runs has a resolvable reference.
const withdrawalAggregateFixture = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: storefront
  version: "1.0.0"
spec:
  components:
    api:
      type: core/api-server
`

// withdrawalKind is one arm of the per-kind corpus: the entity kind, the catalog
// file that declares it, the identifier a person types to inspect it, and the OCI
// repository path a withdrawal for it is recorded against.
//
// repoPath is written out as a literal rather than composed through the location
// rule the product calls. Deriving it here would make the two sides agree by
// construction and assert nothing: the row would still pass if the advisory looked
// up a location no push ever publishes to. Written literally, the row pins the
// per-kind derivation AND the location it resolves to, and a change to either has
// to be made deliberately in both places.
type withdrawalKind struct {
	kind       string
	fileName   string
	body       string
	identifier string
	repoPath   string
	// header is a fragment of the entity's own rendered output, asserted
	// alongside the advisory so a row cannot pass on an advisory printed after a
	// render that failed to name the entity.
	header string
}

// withdrawalCorpus declares one arm per kind the catalog-identity branch of
// inspect resolves. TestInspectWithdrawal_CoversEveryPublishableKind holds it
// equal to the publishable-kind set, so a kind added there without an arm here
// fails by name rather than silently going unreported.
func withdrawalCorpus() []withdrawalKind {
	return []withdrawalKind{
		{
			kind: "Component", fileName: "component.yaml", body: bareNameComponentFixture,
			identifier: "core/api-server", repoPath: "component/core/api-server",
			header: "Component: core/api-server",
		},
		{
			// The object-schema Feature, not the string-schema one the file-path
			// goldens use: a bare identifier resolves through the STRICT catalog
			// load, whose envelope validation requires spec.schema to be an object.
			kind: "Feature", fileName: "feature.yaml", body: bareNameFeatureFixture,
			identifier: "core/metrics", repoPath: "feature/core/metrics",
			header: "Feature: core/metrics:1.0.0",
		},
		{
			kind: "Recipe", fileName: "recipe.yaml", body: recipeSynthesizedFixture,
			identifier: "helm/core/api-server-recipe", repoPath: "recipe/helm/core/api-server-recipe",
			header: "Recipe: core/api-server-recipe:1.0.0",
		},
		{
			kind: "FeatureRecipe", fileName: "featurerecipe.yaml", body: featureRecipeFixture,
			identifier: "helm/core/metrics-recipe", repoPath: "featurerecipe/helm/core/metrics-recipe",
			header: "FeatureRecipe: core/metrics-recipe:1.0.0",
		},
		{
			kind: "Plugin", fileName: "plugin.yaml", body: pluginFixture,
			identifier: "provider/helm-renderer", repoPath: "plugin/provider/helm-renderer",
			header: "Plugin: helm-renderer:1.0.0",
		},
		{
			kind: "Aggregate", fileName: "aggregate.yaml", body: withdrawalAggregateFixture,
			identifier: "storefront", repoPath: "aggregate/storefront",
			header: "Aggregate: storefront:1.0.0",
		},
		{
			kind: "SchemaLibrary", fileName: "schemalibrary.yaml", body: schemaLibraryFixture,
			identifier: "CommonSchemas", repoPath: "schemalibrary/commonschemas",
			header: "SchemaLibrary: CommonSchemas:1.0.0",
		},
		{
			kind: "MappingLibrary", fileName: "mappinglibrary.yaml", body: mappingLibraryFixture,
			identifier: "CommonHelmMappings", repoPath: "mappinglibrary/commonhelmmappings",
			header: "MappingLibrary: CommonHelmMappings:1.0.0",
		},
		{
			kind: "WorkloadLibrary", fileName: "workloadlibrary.yaml", body: proofWorkloadLibrary,
			identifier: "BaseWorkloads", repoPath: "workloadlibrary/baseworkloads",
			header: "WorkloadLibrary: BaseWorkloads:1.0.0",
		},
		{
			kind: "ArtifactsLibrary", fileName: "artifactslibrary.yaml", body: proofArtifactsLibrary,
			identifier: "SharedArtifacts", repoPath: "artifactslibrary/sharedartifacts",
			header: "ArtifactsLibrary: SharedArtifacts:1.0.0",
		},
	}
}

const withdrawalFixtureHost = "hub.example.test"

// writeWithdrawalWorkspace authors the per-kind corpus into a workspace that
// declares its own catalog directory and default registry, and returns it.
//
// The registry declaration is what makes the advisory reachable at all: the
// lookup reads the local index of the registry a bare identity resolves from, so
// a workspace with none configured is a workspace where nothing can be recorded.
func writeWithdrawalWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	catDir := filepath.Join(root, "catalog")
	if err := os.MkdirAll(catDir, 0o755); err != nil {
		t.Fatalf("mkdir catalog: %v", err)
	}
	cfg := "workspace:\n  catalog_dirs:\n    - catalog\ndefault_registry: " + withdrawalFixtureHost + "\noffline: true\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	for _, arm := range withdrawalCorpus() {
		if err := os.WriteFile(filepath.Join(catDir, arm.fileName), []byte(arm.body), 0o644); err != nil {
			t.Fatalf("write %s fixture: %v", arm.kind, err)
		}
	}
	return resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
}

// recordWithdrawals writes a local index carrying one marker per arm at the
// severity given, signed or not. It uses the same on-disk writer the index-loading
// tests use, so the record lands at the path the product's own encoder resolves —
// a hand-composed path that missed would produce a silent absence, which reads
// exactly like the defect under test.
func recordWithdrawals(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, severity deprecation.Severity, signed bool) {
	t.Helper()
	var records []registryindex.Record
	for _, arm := range withdrawalCorpus() {
		entry := registryindex.VersionDeprecation{
			Version: "1.0.0",
			Marker: deprecation.Marker{
				Severity:    severity,
				Reason:      "withdrawn for a defect",
				Replacement: arm.identifier + ":2.0.0",
				IssuedOn:    "2026-01-02T03:04:05Z",
			},
		}
		if signed {
			entry.Signer = verifiedIndexSigner()
		}
		records = append(records, registryindex.Record{
			URI:          "oci://" + withdrawalFixtureHost + "/" + arm.repoPath,
			Name:         arm.repoPath,
			Version:      "1.0.0",
			Versions:     []string{"1.0.0"},
			Kind:         arm.kind,
			Deprecations: []registryindex.VersionDeprecation{entry},
		})
	}
	writeIndexFixture(t, rw.Root(), rw.Config().ResolvedArtifactCacheDir(), withdrawalFixtureHost,
		RegistryIndex{Host: withdrawalFixtureHost, Records: records}, "2026-02-01T00:00:00Z\n")
}

// inspectWithdrawalOutput resolves one arm by identifier and returns everything
// the surface printed.
func inspectWithdrawalOutput(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, arm withdrawalKind, opts inspectOptions) string {
	t.Helper()
	if err := inspectCatalogEntity(context.Background(), rw, arm.identifier, nil, opts); err != nil {
		t.Fatalf("inspect %s %q: %v", arm.kind, arm.identifier, err)
	}
	return opts.out.(*bytes.Buffer).String()
}

// TestInspectWithdrawal_EveryKindReportsByInvocation is the proof that matters:
// for every kind the catalog-identity branch resolves, a recorded withdrawal
// reaches the surface a person reads.
//
// It asserts the PRINTED BLOCK from a real resolution rather than the lookup
// underneath it. A unit assertion on the lookup would discharge nothing here,
// because the defect it replaces was that nine of the ten kinds never reached the
// lookup at all — such a test passes while the surface stays silent. The rendered
// text is logged per kind so the block each kind produces is readable from a
// verbose run.
func TestInspectWithdrawal_EveryKindReportsByInvocation(t *testing.T) {
	// Not parallel: the corpus shares one workspace and the arms read one index.
	rw := writeWithdrawalWorkspace(t)
	recordWithdrawals(t, rw, deprecation.SeverityRetracted, true /* signed */)

	for _, arm := range withdrawalCorpus() {
		var buf bytes.Buffer
		out := inspectWithdrawalOutput(t, rw, arm, testInspectOptions(&buf))
		t.Logf("=== %s :: %s ===\n%s", arm.kind, arm.identifier, out)

		if !strings.Contains(out, arm.header) {
			t.Errorf("%s: output missing the entity's own header %q:\n%s", arm.kind, arm.header, out)
		}
		if !strings.Contains(out, "Deprecation: retracted (version 1.0.0)") {
			t.Errorf("%s: a recorded withdrawal must reach this surface; got:\n%s", arm.kind, out)
		}
		if !strings.Contains(out, "Reason: withdrawn for a defect") {
			t.Errorf("%s: the advisory must carry the recorded reason; got:\n%s", arm.kind, out)
		}
		if !strings.Contains(out, "Recorded as of: 2026-02-01T00:00:00Z") {
			t.Errorf("%s: the advisory must state when the snapshot was taken; got:\n%s", arm.kind, out)
		}
	}
}

// TestInspectWithdrawal_CoversEveryPublishableKind holds the corpus equal to the
// publishable-kind set, in both directions and by name.
//
// The set comparison is the point rather than a count. A count agrees while both
// sides are short by one, and it cannot be re-derived later by a reader who does
// not know what was counted; a named difference says which kind is missing and
// from which side. The publishable set is the right yardstick because a withdrawal
// marker is a referrer of a PUBLISHED artifact: a kind that cannot be published
// can carry no marker, and a kind that can must be reported here.
func TestInspectWithdrawal_CoversEveryPublishableKind(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{}
	for _, arm := range withdrawalCorpus() {
		if covered[arm.kind] {
			t.Errorf("kind %s appears twice in the corpus", arm.kind)
		}
		covered[arm.kind] = true
	}
	publishable := map[string]bool{}
	for _, kind := range oci.MediaTypeKinds() {
		publishable[kind] = true
	}

	var uncovered, unpublishable []string
	for kind := range publishable {
		if !covered[kind] {
			uncovered = append(uncovered, kind)
		}
	}
	for kind := range covered {
		if !publishable[kind] {
			unpublishable = append(unpublishable, kind)
		}
	}
	sort.Strings(uncovered)
	sort.Strings(unpublishable)

	if len(uncovered) > 0 {
		t.Errorf("publishable kinds with no withdrawal coverage: %v — each can carry a marker and must report one", uncovered)
	}
	if len(unpublishable) > 0 {
		t.Errorf("covered kinds that are not publishable: %v — no marker can exist for them", unpublishable)
	}
}

// TestInspectWithdrawal_DeclinesAnUnverifiedRecordOnEveryKind proves the trust
// half end-to-end: a record decoding without a verified signature reaches no
// kind's output.
//
// It runs the same corpus as the reporting test with the signature withheld, so a
// change that widened what is reported without keeping the trust test would fail
// here on every kind rather than on none.
func TestInspectWithdrawal_DeclinesAnUnverifiedRecordOnEveryKind(t *testing.T) {
	// Not parallel: the corpus shares one workspace and the arms read one index.
	rw := writeWithdrawalWorkspace(t)
	recordWithdrawals(t, rw, deprecation.SeverityRetracted, false /* signed */)

	for _, arm := range withdrawalCorpus() {
		var buf bytes.Buffer
		out := inspectWithdrawalOutput(t, rw, arm, testInspectOptions(&buf))
		if strings.Contains(out, "Deprecation:") {
			t.Errorf("%s: an unverified record carries no trust basis and must not be reported; got:\n%s", arm.kind, out)
		}
		if !strings.Contains(out, arm.header) {
			t.Errorf("%s: declining the record must leave the entity itself rendered; got:\n%s", arm.kind, out)
		}
	}
}

// TestInspectWithdrawal_KeepsAdvisorySeverities proves the display does NOT
// mirror the selection screen's severity filter.
//
// That filter admits only the withdrawal severity, because it decides what a
// build resolves to. Copying it here would drop the two advisory severities —
// exactly the ones a reader browsing an entity is asking about — so this drives
// each of them through the surface and requires the recorded value to be named.
func TestInspectWithdrawal_KeepsAdvisorySeverities(t *testing.T) {
	for _, severity := range []deprecation.Severity{deprecation.SeverityWarning, deprecation.SeverityError} {
		// Not parallel: each severity rewrites the shared index.
		rw := writeWithdrawalWorkspace(t)
		recordWithdrawals(t, rw, severity, true /* signed */)
		for _, arm := range withdrawalCorpus() {
			var buf bytes.Buffer
			out := inspectWithdrawalOutput(t, rw, arm, testInspectOptions(&buf))
			if !strings.Contains(out, "Deprecation: "+string(severity)+" (version 1.0.0)") {
				t.Errorf("%s: a verified %s must still be reported on a browse surface; got:\n%s", arm.kind, severity, out)
			}
		}
	}
}

// TestInspectWithdrawal_MachineReadableOutputIsUnchanged pins the stated bound:
// the advisory is a text block and --output yaml carries none.
//
// The subject under that flag is a document a consumer parses. Appending a text
// block would break the parse, and folding the advisory into the document would
// assert a field the entity does not have — so the advisory stays on the surface
// a person reads. Asserting it here keeps the bound deliberate rather than an
// omission someone later "fixes" into a corrupted document.
func TestInspectWithdrawal_MachineReadableOutputIsUnchanged(t *testing.T) {
	// Not parallel: the corpus shares one workspace and the arms read one index.
	rw := writeWithdrawalWorkspace(t)
	recordWithdrawals(t, rw, deprecation.SeverityRetracted, true /* signed */)

	for _, arm := range withdrawalCorpus() {
		// The Component branch has its own render path that does not reach the
		// machine-readable emission at all, so it carries nothing to assert here.
		if arm.kind == "Component" {
			continue
		}
		var buf bytes.Buffer
		opts := testInspectOptions(&buf)
		opts.output = outputYAML
		out := inspectWithdrawalOutput(t, rw, arm, opts)
		if strings.Contains(out, "Deprecation:") {
			t.Errorf("%s: the machine-readable emission must carry no advisory text; got:\n%s", arm.kind, out)
		}
	}
}

// TestInspectWithdrawal_NoRecordChangesNothing pins the no-withdrawal case: with
// no index built, every kind renders exactly what it rendered before the advisory
// reached it.
//
// The comparison is against the same corpus run with a record present, so the
// assertion is capable of failing — a test that only checked for the absence of a
// substring would pass on a surface that had stopped rendering anything at all.
func TestInspectWithdrawal_NoRecordChangesNothing(t *testing.T) {
	// Not parallel: the recorded arm rewrites the shared index.
	bare := writeWithdrawalWorkspace(t)
	recorded := writeWithdrawalWorkspace(t)
	recordWithdrawals(t, recorded, deprecation.SeverityRetracted, true /* signed */)

	for _, arm := range withdrawalCorpus() {
		var bareBuf, recordedBuf bytes.Buffer
		bareOut := inspectWithdrawalOutput(t, bare, arm, testInspectOptions(&bareBuf))
		recordedOut := inspectWithdrawalOutput(t, recorded, arm, testInspectOptions(&recordedBuf))

		if strings.Contains(bareOut, "Deprecation:") {
			t.Errorf("%s: no index is built, so nothing may be reported; got:\n%s", arm.kind, bareOut)
		}
		if bareOut == recordedOut {
			t.Errorf("%s: the comparison cannot fail — a recorded withdrawal produced identical output:\n%s", arm.kind, bareOut)
			continue
		}
		if !strings.HasPrefix(recordedOut, bareOut) {
			t.Errorf("%s: the advisory must be additive; the un-recorded render is not a prefix of the recorded one:\n--- bare ---\n%s\n--- recorded ---\n%s",
				arm.kind, bareOut, recordedOut)
		}
	}
}
