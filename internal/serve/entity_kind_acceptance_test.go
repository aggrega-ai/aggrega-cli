// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// Which operations accept a document of each authorable kind, held to the
// operations themselves.
//
// The entity-kinds answer declares, per kind, whether single-entity validation,
// publication and inspection by name accept a document of that kind, and each
// verdict is projected from the operation's own declaration. Comparing the answer
// with those declarations would compare one source with itself and could not
// fail, so these tests invoke each operation instead — on a real document of every
// kind the answer names — and compare what the operation did with what the answer
// says. The second test plants a wrong verdict for each operation, once in each
// direction, and shows the comparison names it, so the agreement is a check that
// can fail rather than two readings of one list.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// acceptanceFixture is one document of an authorable kind, and the name an
// inspection by name sends for it.
//
// The name is written as one entity references another, which for a kind whose
// identity is scoped is the scoped form: a Recipe or a FeatureRecipe by its
// capability, a Plugin by its plugin type.
type acceptanceFixture struct {
	name     string
	document string
}

// acceptanceFixtures holds one real document of each kind an author writes, keyed
// by the kind its header declares.
//
// Each is small and complete. It passes its own kind's schema, which the harness
// checks before any operation runs, and it carries no value an operation computes —
// no lock, no digest, no recorded host functions — so nothing here stands in for
// what production would write. Every name is distinct, so an inspection resolving
// one document cannot be answered by another.
//
// It is keyed rather than listed so the harness can hold it to the answer in both
// directions: a kind the answer names with no document here fails, and so does a
// document for a kind the answer no longer names.
var acceptanceFixtures = map[string]acceptanceFixture{
	"Component": {name: "rowops/probe-server", document: `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: rowops/probe-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`},
	"Feature": {name: "rowops/probe-metrics", document: `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: rowops/probe-metrics
  version: 1.0.0
spec:
  schema:
    values:
      port: { type: integer, required: false }
`},
	"Recipe": {name: "helm/rowops-probe-server", document: `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: rowops-probe-server
  version: 1.0.0
spec:
  capability: helm
  implements: rowops/probe-server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: probe-server:1.0.0
`},
	"FeatureRecipe": {name: "helm/rowops-probe-metrics", document: `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: rowops-probe-metrics
  version: 1.0.0
spec:
  capability: helm
  implements: rowops/probe-metrics
`},
	"SchemaLibrary": {name: "RowopsProbeTypes", document: `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: RowopsProbeTypes
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`},
	"MappingLibrary": {name: "RowopsProbeMappings", document: `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: RowopsProbeMappings
  version: 1.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.image }}'
`},
	"WorkloadLibrary": {name: "RowopsProbeWorkloads", document: `apiVersion: aggrega.ai/v1alpha1
kind: WorkloadLibrary
metadata:
  name: RowopsProbeWorkloads
  version: 1.0.0
spec:
  workloads:
    SimpleService: 'kind: Deployment'
`},
	"ArtifactLibrary": {name: "RowopsProbeArtifacts", document: `apiVersion: aggrega.ai/v1alpha1
kind: ArtifactLibrary
metadata:
  name: RowopsProbeArtifacts
  version: 1.0.0
spec:
  artifacts:
    Support:
      support.yaml: "kind: ConfigMap"
`},
	"Aggregate": {name: "rowops/probe-app", document: `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: rowops/probe-app
  version: 1.0.0
spec:
  components:
    server:
      type: rowops/probe-server
`},
	"Plugin": {name: "linter/rowops-probe", document: `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: rowops-probe
  version: 1.0.0
  pluginType: linter
spec:
  abiVersion: v1alpha1
`},
	"Manifest": {name: "rowops-probe-manifest", document: `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: rowops-probe-manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        rowops/probe-server: helm/rowops-probe-server
`},
	"Release": {name: "rowops-probe-release", document: `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rowops-probe-release
  environment: probe
  version: 1.0.0
spec:
  aggregate: ../aggregate/aggregate.yaml
  manifest: ../manifest/manifest.yaml
  components:
    server: {}
`},
}

// acceptanceHarness is the workspace every operation is invoked in, and the
// daemon answering over it.
type acceptanceHarness struct {
	srv        *Server
	root       string
	catalogDir string

	// documents is each fixture's path relative to the workspace root, in the
	// spelling the daemon reports a document by, keyed by the kind its header
	// declares.
	documents map[string]string
}

// acceptanceWorkspace asks the daemon for the entity-kinds answer through the
// production dispatch, then lays one document of every kind that answer names
// into a directory of its own under one catalog directory, inside a workspace
// resolved the way a publication resolves one. It returns the answer's entries
// with the harness.
//
// Each document sits alone in its directory because a publication refuses a
// document sharing its directory with another entity where its kind has a
// directory of its own, and sharing would make every such kind read as refused
// for a reason that is not its kind. All of them sit under the catalog
// directory, the Manifest and the Release included, so an inspection refusing
// either refuses it where the catalog reads documents rather than because the
// document is somewhere the catalog does not look.
func acceptanceWorkspace(t *testing.T) ([]entityKindDescriptor, *acceptanceHarness) {
	t.Helper()

	srv, root := publishFixture(t, defaultRegistryWorkspace)
	entries := decodeGetSchemaResult(t, callGetSchema(t, srv, map[string]any{})).EntityKinds
	if len(entries) == 0 {
		t.Fatal("the entity-kinds answer names no kind, so there is no operation to invoke and every " +
			"comparison below would assert nothing")
	}

	catalogDir := filepath.Join(root, "catalog")
	h := &acceptanceHarness{srv: srv, root: root, catalogDir: catalogDir, documents: map[string]string{}}
	named := map[string]bool{}
	for _, e := range entries {
		named[e.DocumentKind] = true
		fixture, ok := acceptanceFixtures[e.DocumentKind]
		if !ok {
			t.Fatalf("the answer names %s and no document of that kind is held here, so no operation can be "+
				"invoked on one; add a document of it to acceptanceFixtures", e.DocumentKind)
		}
		rel := filepath.Join("catalog", e.Kind, e.Kind+".yaml")
		path := filepath.Join(root, rel)
		writeFixtureFile(t, path, fixture.document)
		if _, err := envelope.ValidateBytes(envelope.Kind(e.DocumentKind), []byte(fixture.document), path); err != nil {
			t.Fatalf("the %s document does not pass its own kind's schema, so an operation refusing it would "+
				"say nothing about the kind: %v", e.DocumentKind, err)
		}
		h.documents[e.DocumentKind] = filepath.ToSlash(rel)
	}
	var stale []string
	for kind := range acceptanceFixtures {
		if !named[kind] {
			stale = append(stale, kind)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("documents are held for %v, which the answer no longer names; remove them", stale)
	}

	srv.wsConfig.Workspace.CatalogDirs = []string{catalogDir}
	srv.lazyCatalog = catalog.NewLazyLookup(root, "", []string{catalogDir}, nil)
	return entries, h
}

// acceptanceObserver invokes one operation on the document of one kind and
// reports whether the operation accepted it.
//
// Accepted means the operation completed. Refused means it refused with its own
// refusal of a document of that kind, recognised by that refusal's identity
// rather than by its wording. Anything else — a refusal of the document for what
// it says, or an answer about some other entity — is reported as a defect in the
// harness rather than read as a verdict about the kind, because a document an
// operation refuses for another reason stands for no real document of the kind.
type acceptanceObserver func(t *testing.T, h *acceptanceHarness, entry entityKindDescriptor) bool

// acceptanceObservers invokes each operation acceptedBy declares a verdict for,
// keyed by that member's wire name. A member with no observer here, or an
// observer naming no member, fails the harness, so a member added to the answer
// cannot go uninvoked.
var acceptanceObservers = map[string]acceptanceObserver{
	"singleEntityValidation": observeSingleEntityValidation,
	"publication":            observePublication,
	"inspectionByName":       observeInspectionByName,
}

// observeSingleEntityValidation runs the single-entity scope `aggrega validate
// <path>` runs, over the document of entry's kind, against the harness's catalog
// directory and a cache of its own.
func observeSingleEntityValidation(t *testing.T, h *acceptanceHarness, entry entityKindDescriptor) bool {
	t.Helper()
	path := filepath.Join(h.root, filepath.FromSlash(h.documents[entry.DocumentKind]))
	_, err := validateops.ValidateEntity(context.Background(), h.root, t.TempDir(), []string{h.catalogDir}, nil, path, false)
	var refused *validateops.UnacceptedKindError
	switch {
	case err == nil:
		return true
	case errors.As(err, &refused):
		return false
	default:
		t.Errorf("single-entity validation refused the %s document for something other than its kind: %v",
			entry.DocumentKind, err)
		return false
	}
}

// observePublication sends aggrega/publishEntity naming the document of entry's
// kind alone, as a dry run, through the production dispatch.
//
// The wire carries a refusal's text, and the identifiers of a condition it
// names, but not the failure itself, and a kind this method does not publish is
// refused with a failure recognised by its type or by a condition — and a type
// is not on the wire. So a refusal is read again from the two steps the method
// runs, where the failure is still in hand, and held to be the same refusal the
// wire carried before its identity is read. It is held as the wire answers it:
// the one function every error answer passes through on its way to the wire
// (conditionRefusal) is applied to it here too, so the short code it leads a
// message naming a condition with, and the identifiers it adds, are that
// function's own rather than a second statement of its rule.
func observePublication(t *testing.T, h *acceptanceHarness, entry entityKindDescriptor) bool {
	t.Helper()
	params := publishEntityParams{Entity: h.documents[entry.DocumentKind], DryRun: true, Registry: publishTestRegistry}
	resp := callDaemonMethod(t, h.srv, "aggrega/publishEntity", params)
	if resp.Error == nil {
		var res publishEntityResult
		decodeDaemonResult(t, resp, &res)
		if res.Kind != entry.DocumentKind || !res.DryRun {
			t.Errorf("the dry-run publication of the %s document answered for kind %q with dryRun %t",
				entry.DocumentKind, res.Kind, res.DryRun)
		}
		return true
	}

	cfg, paramErr := h.srv.publishEntityConfig(params, io.Discard)
	if paramErr != nil {
		t.Errorf("the publication of the %s document was refused as a malformed request, which is about the "+
			"request rather than the kind: %s", entry.DocumentKind, paramErr.Message)
		return false
	}
	_, env := runPublishEntity(context.Background(), cfg)
	if env == nil {
		t.Errorf("the publication of the %s document was refused on the wire (%s) and answered when run "+
			"again, so the refusal cannot be identified", entry.DocumentKind, resp.Error.Message)
		return false
	}
	answered := conditionRefusal(env)
	if answered.Message != resp.Error.Message || !reflect.DeepEqual(answered.Data, resp.Error.Data) {
		t.Errorf("the refusal the wire carried for the %s document is not the one the operation raises:\n"+
			" wire:      %s %+v\n operation: %s %+v", entry.DocumentKind, resp.Error.Message, resp.Error.Data,
			answered.Message, answered.Data)
		return false
	}
	var executable *publishops.ExecutableRequiredError
	if errors.As(env.cause, &executable) || errors.Is(env.cause, oci.ErrUnknownEntityKind()) {
		return false
	}
	t.Errorf("the publication of the %s document was refused for something other than its kind: %s",
		entry.DocumentKind, resp.Error.Message)
	return false
}

// observeInspectionByName sends aggrega/entityDefinition the name of the
// document of entry's kind through the production dispatch.
//
// A name resolved counts only where it resolved to this kind from this
// document. A refusal counts only as the method's own refusal of a name the
// workspace declares — the not-resolvable code, naming this document — which is
// what separates a kind the resolution does not reach from a document the
// catalog was never shown.
func observeInspectionByName(t *testing.T, h *acceptanceHarness, entry entityKindDescriptor) bool {
	t.Helper()
	name := acceptanceFixtures[entry.DocumentKind].name
	document := h.documents[entry.DocumentKind]
	resp := callEntityDefinition(t, h.srv, map[string]any{"name": name})
	if resp.Error == nil {
		got := decodeEntityDefinition(t, resp)
		if got.Kind != entry.DocumentKind || got.File != document {
			t.Errorf("%q resolved to a %s declared in %q, not to the %s document in %q",
				name, got.Kind, got.File, entry.DocumentKind, document)
			return false
		}
		return true
	}
	if resp.Error.Code == errEntityNotResolvable && resp.Error.Data != nil && resp.Error.Data.File == document {
		return false
	}
	t.Errorf("inspecting %q was refused other than as a name the workspace declares in %q and the catalog "+
		"resolves no entity for: code %d, data %+v, message %s",
		name, document, resp.Error.Code, resp.Error.Data, resp.Error.Message)
	return false
}

// acceptanceMembers names the members of acceptedBy by their wire names, in
// declaration order. They are read from the type's own tags, so a member added
// to the answer is a member the harness must find an observer for.
func acceptanceMembers() []string {
	typ := reflect.TypeOf(entityKindAcceptance{})
	out := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		out = append(out, name)
	}
	return out
}

// acceptanceField returns the field of a carrying the named member's verdict, or
// an invalid value when no field carries that wire name.
func acceptanceField(a *entityKindAcceptance, member string) reflect.Value {
	v := reflect.ValueOf(a).Elem()
	for i := range v.NumField() {
		if name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ","); name == member {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}

// acceptanceVerdict reads the named member's verdict off a.
func acceptanceVerdict(a entityKindAcceptance, member string) bool {
	return acceptanceField(&a, member).Bool()
}

// setAcceptanceVerdict writes the named member's verdict into a.
func setAcceptanceVerdict(a *entityKindAcceptance, member string, verdict bool) {
	acceptanceField(a, member).SetBool(verdict)
}

// observeAcceptance invokes every operation acceptedBy names on the document of
// every kind the answer names, and returns what each did, keyed by the kind the
// document declares.
func observeAcceptance(t *testing.T, h *acceptanceHarness, entries []entityKindDescriptor) map[string]entityKindAcceptance {
	t.Helper()
	members := acceptanceMembers()
	for _, m := range members {
		if acceptanceObservers[m] == nil {
			t.Fatalf("acceptedBy declares %q and nothing here invokes that operation; add an observer for it", m)
		}
	}
	observers := slices.Sorted(maps.Keys(acceptanceObservers))
	for _, name := range observers {
		if !slices.Contains(members, name) {
			t.Fatalf("an observer is held for %q, which acceptedBy no longer declares; remove it", name)
		}
	}

	out := make(map[string]entityKindAcceptance, len(entries))
	for _, e := range entries {
		var seen entityKindAcceptance
		for _, m := range members {
			setAcceptanceVerdict(&seen, m, acceptanceObservers[m](t, h, e))
		}
		out[e.DocumentKind] = seen
	}
	return out
}

// acceptanceMismatch is one place where the answer and an operation disagree:
// the kind, the acceptedBy member naming the operation, what the answer
// declares, and what the operation did.
type acceptanceMismatch struct {
	kind      string
	operation string
	declared  bool
	observed  bool
}

// String renders the disagreement the way a failure reports it.
func (m acceptanceMismatch) String() string {
	did := "refused it"
	if m.observed {
		did = "accepted it"
	}
	return fmt.Sprintf("%s, %s: the answer declares %t and the operation %s", m.kind, m.operation, m.declared, did)
}

// acceptanceMismatches names every place the answer's declaration and what the
// operations were observed to do disagree, in the answer's order and, within an
// entry, in acceptedBy's own order. An entry with no observation is read as
// refused by every operation, so an acceptance declared with nothing observed
// behind it is named.
func acceptanceMismatches(declared []entityKindDescriptor, observed map[string]entityKindAcceptance) []acceptanceMismatch {
	members := acceptanceMembers()
	var out []acceptanceMismatch
	for _, d := range declared {
		seen := observed[d.DocumentKind]
		for _, m := range members {
			want, got := acceptanceVerdict(d.AcceptedBy, m), acceptanceVerdict(seen, m)
			if want != got {
				out = append(out, acceptanceMismatch{kind: d.DocumentKind, operation: m, declared: want, observed: got})
			}
		}
	}
	return out
}

// TestEveryDeclaredAcceptanceIsWhatTheOperationDoes is the invocation proof: for
// every kind the entity-kinds answer names and every operation its acceptedBy
// declares a verdict for, the operation is invoked on a real document of that
// kind, and it accepts exactly where the answer says it does and refuses, with
// its own refusal, where the answer says it does not.
func TestEveryDeclaredAcceptanceIsWhatTheOperationDoes(t *testing.T) {
	t.Parallel()

	entries, h := acceptanceWorkspace(t)
	observed := observeAcceptance(t, h, entries)
	if got := acceptanceMismatches(entries, observed); len(got) > 0 {
		t.Errorf("the entity-kinds answer and the operations disagree in %d place(s):\n%s",
			len(got), joinMismatches(got))
	}
}

// TestAcceptanceComparisonNamesEachPlantedDisagreement is the planted defect,
// standing. It feeds the comparison the real answer with one verdict flipped —
// for each operation, once in each direction — against what the operations
// really did, and requires exactly that one disagreement to be named.
//
// The unflipped answer is the control, and it must name nothing: a comparison
// that already disagrees would make every row below unreadable.
//
// A direction is planted in the answer where some kind carries the opposite
// verdict. Where no kind does — every kind accepted, or none — the same
// disagreement is planted on the other side, in what the operation was observed
// to do, so neither direction goes unexercised because the product happens to
// have no example of it.
func TestAcceptanceComparisonNamesEachPlantedDisagreement(t *testing.T) {
	t.Parallel()

	entries, h := acceptanceWorkspace(t)
	observed := observeAcceptance(t, h, entries)
	if got := acceptanceMismatches(entries, observed); len(got) > 0 {
		t.Fatalf("the unplanted answer already disagrees with the operations, so no planted row can be read:\n%s",
			joinMismatches(got))
	}

	type plant struct {
		name     string
		answer   []entityKindDescriptor
		observed map[string]entityKindAcceptance
		want     acceptanceMismatch
	}
	var rows []plant
	for _, member := range acceptanceMembers() {
		for _, declared := range []bool{true, false} {
			answer, seen := slices.Clone(entries), observed
			i := slices.IndexFunc(entries, func(e entityKindDescriptor) bool {
				return acceptanceVerdict(e.AcceptedBy, member) != declared
			})
			if i >= 0 {
				setAcceptanceVerdict(&answer[i].AcceptedBy, member, declared)
			} else {
				i = 0
				seen = maps.Clone(observed)
				flipped := seen[entries[i].DocumentKind]
				setAcceptanceVerdict(&flipped, member, !declared)
				seen[entries[i].DocumentKind] = flipped
			}
			did := "refuses"
			if !declared {
				did = "accepts"
			}
			rows = append(rows, plant{
				name:     fmt.Sprintf("%s declared %t where the operation %s the %s document", member, declared, did, entries[i].DocumentKind),
				answer:   answer,
				observed: seen,
				want:     acceptanceMismatch{kind: entries[i].DocumentKind, operation: member, declared: declared, observed: !declared},
			})
		}
	}
	if len(rows) == 0 {
		t.Fatal("acceptedBy declares no member, so no disagreement could be planted and the comparison was never exercised")
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got := acceptanceMismatches(row.answer, row.observed)
			if !reflect.DeepEqual(got, []acceptanceMismatch{row.want}) {
				t.Errorf("the comparison did not name exactly the planted disagreement.\n got:\n%s\nwant:\n%s",
					joinMismatches(got), row.want)
			}
		})
	}
}

// joinMismatches renders mismatches one per line for a failure message.
func joinMismatches(ms []acceptanceMismatch) string {
	if len(ms) == 0 {
		return "  (none)"
	}
	lines := make([]string, 0, len(ms))
	for _, m := range ms {
		lines = append(lines, "  "+m.String())
	}
	return strings.Join(lines, "\n")
}
