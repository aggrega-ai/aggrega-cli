// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The proofs in this file cover the rule that an attachment written on a
// component instance under a name no slot declares does not fit a slot that
// instance leaves empty, evaluated over the finalised graph through the build's
// own invocation. They sit in this package for the reason the other slot proofs
// do: the rule's facts and its enumeration are carried on an unexported output,
// and the near-miss advisory it shares an attachment with is drained inside the
// same run, so only a real compilation shows the two together.
//
// # How each row is built
//
// Every row compiles the fullstack fixture against a temporary copy of its
// catalog in which both of the Aggregate's own component types declare the
// row's slots and the fixture Feature carries the row's labels. The Aggregate is
// spliced in memory and the Release, where a row needs it, is written to a
// temporary file — so each row differs from the fixture in exactly the shape it
// is about, and the fixture stays what every other proof over it expects.
//
// Each row carries one fault or none. A row expecting a build asserts that
// nothing at all halted, not merely that this rule did not, so a row cannot pass
// because some other rule refused the document first.

// misnamedFillFeatureType is the Feature every row attaches. It declares no
// applicability of its own, so no other attachment rule can make it halt.
const misnamedFillFeatureType = "core/proof-injector"

// The attachment names the rows write. None is a slot name any row declares,
// and the second is one edit from the slot the typo rows declare.
const (
	freeAttachmentName  = "receipts"
	typoAttachmentName  = "audti"
	credentialSlot      = "api_key"
	credentialMisnaming = "master_key"
)

// The label constraints and label sets the rows pair. The credential pair is the
// shape the rule was asked for; the grade pair is a Feature carrying no credential
// label, which the rule must judge the same way.
const (
	credentialSelector = "feature.aggrega.ai/kind=secret"
	credentialLabels   = "    feature.aggrega.ai/kind: \"secret\"\n"
	managedSelector    = "fill.aggrega.ai/grade=managed"
	hardenedSelector   = "fill.aggrega.ai/grade=hardened"
	managedLabels      = "    fill.aggrega.ai/grade: \"managed\"\n"
)

// selectedSlot is one optional slot declaring a label constraint, indented to the
// depth a featuresSchema body sits at.
func selectedSlot(name, selector string) string {
	return "    " + name + ":\n      required: false\n      selector: \"" + selector + "\"\n"
}

// unselectedSlot is one optional slot declaring no label constraint.
func unselectedSlot(name string) string {
	return "    " + name + ":\n      required: false\n"
}

// misnamedFillCatalog declares the supplied slots on both of the Aggregate's own
// component types, through the copy-and-splice the other slot proofs use, and
// gives the fixture Feature the supplied labels.
func misnamedFillCatalog(t *testing.T, slots, featureLabels string) string {
	t.Helper()
	root := catalogDeclaringSlots(t, slots)
	path := filepath.Join(root, "base", "features", "proof-injector.yaml")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read feature entity: %v", err)
	}
	const anchor = "  version: \"1.0.0\"\n"
	if !bytes.Contains(source, []byte(anchor)) {
		t.Fatalf("the feature entity no longer carries %q; the label splice would be silently inert", anchor)
	}
	labelled := bytes.Replace(source, []byte(anchor), []byte(anchor+"  labels:\n"+featureLabels), 1)
	if err := os.WriteFile(path, labelled, 0o600); err != nil {
		t.Fatalf("write feature entity: %v", err)
	}
	return root
}

// attachOnTheBackendInstance writes the named attachments on the backend
// component's own block in the Aggregate, each attaching the fixture Feature.
func attachOnTheBackendInstance(t *testing.T, aggregate []byte, names ...string) []byte {
	t.Helper()
	const anchor = "    backend:\n      type: core/compute/api-server\n"
	if !bytes.Contains(aggregate, []byte(anchor)) {
		t.Fatalf("the fixture no longer declares %q the way the splice expects; the splice would be silently inert", anchor)
	}
	block := "      features:\n"
	for _, name := range names {
		block += "        " + name + ":\n          type: " + misnamedFillFeatureType + "\n"
	}
	return bytes.Replace(aggregate, []byte(anchor), []byte(anchor+block), 1)
}

// attachOnASubsystem rewrites the fixture's two components into one subsystem
// and attaches the fixture Feature under name on that subsystem, so it reaches
// both components by inheritance and is written on neither.
func attachOnASubsystem(t *testing.T, name string) []byte {
	t.Helper()
	return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: fullstack-app
  version: "1.0.0"
spec:
  aggregates:
    data:
      type: db-module
  subsystems:
    app:
      features:
        ` + name + `:
          type: ` + misnamedFillFeatureType + `
      components:
        backend:
          type: core/compute/api-server
          uses:
            databases:
              - data.main-db
        frontend:
          type: core/compute/web-ui
          uses:
            apis:
              my-api:
                target: backend
                inject:
                  env:
                    - in: API_HOST
                      from: host
                    - in: API_PORT
                      from: service_port
`)
}

// attachOnTheBackendBoundary attaches the fixture Feature under name on the
// Release boundary holding the backend component, so it reaches that component
// by inheritance and is written on no component entry.
func attachOnTheBackendBoundary(t *testing.T, release []byte, name string) []byte {
	t.Helper()
	const anchor = "        aggrega.ai/namespace: \"backend-zone\"\n"
	if !bytes.Contains(release, []byte(anchor)) {
		t.Fatalf("the fixture Release no longer carries %q; the splice would be silently inert", anchor)
	}
	block := "      features:\n        " + name + ":\n          type: " + misnamedFillFeatureType + "\n"
	return bytes.Replace(release, []byte(anchor), []byte(anchor+block), 1)
}

// attachOnTheBackendReleaseEntry attaches the fixture Feature under name on the
// Release's own entry for the backend component.
func attachOnTheBackendReleaseEntry(t *testing.T, release []byte, name string) []byte {
	t.Helper()
	const anchor = "            port: 8082\n"
	if !bytes.Contains(release, []byte(anchor)) {
		t.Fatalf("the fixture Release no longer carries %q; the splice would be silently inert", anchor)
	}
	block := "          features:\n            " + name + ":\n              type: " + misnamedFillFeatureType + "\n"
	return bytes.Replace(release, []byte(anchor), []byte(anchor+block), 1)
}

// compileMisnamedFillWorkspace compiles the fixture against the supplied catalog,
// Aggregate bytes and — where release is non-nil — Release bytes, collecting the
// advisories the run raises on the one channel a build records them on.
func compileMisnamedFillWorkspace(
	t *testing.T,
	catalogPath string,
	aggregate []byte,
	release []byte,
) (*stepsOneToFourOutput, *AdvisoryLog) {
	t.Helper()

	releasePath := invocationReleasePath
	if release != nil {
		releasePath = filepath.Join(t.TempDir(), "release.yaml")
		if err := os.WriteFile(releasePath, release, 0o600); err != nil {
			t.Fatalf("write release: %v", err)
		}
	}
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(invocationManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", catalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}

	advisories := &AdvisoryLog{}
	var captured *stepsOneToFourOutput
	runErr := runStepsOneToFour(
		context.Background(),
		Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggregate,
			AggregatePath:     invocationAggregatePath,
			ReleasePath:       releasePath,
			WorkspaceConfig:   defaultWorkspaceConfigForTest(),
			PluginPins:        pluginpath.NoPins(),
			CompilerVersion:   invocationVersion,
			AdvisoryLog:       advisories,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		func(out *stepsOneToFourOutput) error {
			captured = out
			return nil
		},
	)
	if runErr != nil {
		t.Fatalf("pre-render pipeline: %v", runErr)
	}
	if captured == nil {
		t.Fatal("pipeline completed without invoking the continuation")
	}
	return captured, advisories
}

// nearMissKeyPaths reduces the advisories a run recorded to the authoring keys of
// the attachment-name near misses among them.
func nearMissKeyPaths(log *AdvisoryLog) []string {
	var out []string
	for _, entry := range log.Entries() {
		if entry.Code != "attachment-slot-near-miss" {
			continue
		}
		if entry.Location == nil {
			out = append(out, "")
			continue
		}
		out = append(out, entry.Location.KeyPath)
	}
	return out
}

// wantMisnamedFill is one halt a row expects: the authoring key it is positioned
// at, empty where the row does not pin a position, the file that key is a path
// inside, the surface it is filed under, and the values its message must name.
type wantMisnamedFill struct {
	locator  string
	file     string
	category cerrors.Category
	names    []string
}

// TestAnAttachmentFittingAnEmptySlotUnderAnotherNameHalts drives every shape the
// rule distinguishes through one compilation each, and asserts both what halted
// and what the near-miss advisory said about the same attachment.
func TestAnAttachmentFittingAnEmptySlotUnderAnotherNameHalts(t *testing.T) {
	baseRelease, err := os.ReadFile(invocationReleasePath)
	if err != nil {
		t.Fatalf("read release: %v", err)
	}

	rows := []struct {
		name          string
		slots         string
		featureLabels string
		aggregate     func(t *testing.T) []byte
		release       func(t *testing.T) []byte
		wantHalts     []wantMisnamedFill
		wantNearMiss  []string
	}{
		{
			name:          "a credential written on the instance under another name beside the empty slot it fits halts, naming both",
			slots:         selectedSlot(credentialSlot, credentialSelector),
			featureLabels: credentialLabels,
			aggregate: func(t *testing.T) []byte {
				return attachOnTheBackendInstance(t, readAggregate(t), credentialMisnaming)
			},
			wantHalts: []wantMisnamedFill{{
				locator:  ".spec.components.backend.features." + credentialMisnaming,
				file:     "aggregate.yaml",
				category: cerrors.DeveloperError,
				names:    []string{credentialMisnaming, credentialSlot, "backend", misnamedFillFeatureType},
			}},
		},
		{
			name:          "the same credential attached once at a Release boundary builds",
			slots:         selectedSlot(credentialSlot, credentialSelector),
			featureLabels: credentialLabels,
			aggregate:     readAggregate,
			release: func(t *testing.T) []byte {
				return attachOnTheBackendBoundary(t, baseRelease, credentialMisnaming)
			},
		},
		{
			name:          "the same credential attached once on a subsystem builds",
			slots:         selectedSlot(credentialSlot, credentialSelector),
			featureLabels: credentialLabels,
			aggregate: func(t *testing.T) []byte {
				return attachOnASubsystem(t, credentialMisnaming)
			},
		},
		{
			name:          "an attachment whose Feature fits no declared slot builds",
			slots:         selectedSlot(credentialSlot, credentialSelector),
			featureLabels: managedLabels,
			aggregate: func(t *testing.T) []byte {
				return attachOnTheBackendInstance(t, readAggregate(t), credentialMisnaming)
			},
		},
		{
			name:          "an extra fitting attachment beside a slot another attachment fills builds",
			slots:         selectedSlot(credentialSlot, credentialSelector),
			featureLabels: credentialLabels,
			aggregate: func(t *testing.T) []byte {
				return attachOnTheBackendInstance(t, readAggregate(t), credentialSlot, credentialMisnaming)
			},
		},
		{
			name:          "a Feature carrying no credential label that fits an empty slot under another name halts the same way",
			slots:         selectedSlot(slotName, managedSelector),
			featureLabels: managedLabels,
			aggregate: func(t *testing.T) []byte {
				return attachOnTheBackendInstance(t, readAggregate(t), freeAttachmentName)
			},
			wantHalts: []wantMisnamedFill{{
				locator: ".spec.components.backend.features." + freeAttachmentName,
				names:   []string{freeAttachmentName, slotName, "backend"},
			}},
		},
		{
			name:          "a Feature fitting two empty slots halts once, naming each",
			slots:         selectedSlot(slotName, managedSelector) + selectedSlot(secondSlotName, managedSelector),
			featureLabels: managedLabels,
			aggregate: func(t *testing.T) []byte {
				return attachOnTheBackendInstance(t, readAggregate(t), freeAttachmentName)
			},
			wantHalts: []wantMisnamedFill{{
				locator: ".spec.components.backend.features." + freeAttachmentName,
				names:   []string{freeAttachmentName, slotName, secondSlotName},
			}},
		},
		{
			name:          "a slot declaring no selector is fitted by no attachment",
			slots:         unselectedSlot(slotName),
			featureLabels: managedLabels,
			aggregate: func(t *testing.T) []byte {
				return attachOnTheBackendInstance(t, readAggregate(t), freeAttachmentName)
			},
		},
		{
			name:          "an attachment the Release writes on the component's own entry halts",
			slots:         selectedSlot(slotName, managedSelector),
			featureLabels: managedLabels,
			aggregate:     readAggregate,
			release: func(t *testing.T) []byte {
				return attachOnTheBackendReleaseEntry(t, baseRelease, freeAttachmentName)
			},
			// An attachment only the Release writes is positioned at the Release's
			// entry for the component, in the boundary that entry sits in, and filed
			// under the operator who wrote it.
			wantHalts: []wantMisnamedFill{{
				locator:  ".spec.boundaries.backend-ns.components.backend.features." + freeAttachmentName,
				file:     "release.yaml",
				category: cerrors.OpsError,
				names:    []string{freeAttachmentName, slotName, "backend"},
			}},
		},
		{
			name:          "a name one edit from a slot its Feature does not fit stays the near-miss advisory",
			slots:         selectedSlot(slotName, hardenedSelector),
			featureLabels: managedLabels,
			aggregate: func(t *testing.T) []byte {
				return attachOnTheBackendInstance(t, readAggregate(t), typoAttachmentName)
			},
			wantNearMiss: []string{".spec.components.backend.features." + typoAttachmentName},
		},
		{
			name:          "a name one edit from an empty slot its Feature fits is the halt and only the halt",
			slots:         selectedSlot(slotName, managedSelector),
			featureLabels: managedLabels,
			aggregate: func(t *testing.T) []byte {
				return attachOnTheBackendInstance(t, readAggregate(t), typoAttachmentName)
			},
			wantHalts: []wantMisnamedFill{{
				locator: ".spec.components.backend.features." + typoAttachmentName,
				names:   []string{typoAttachmentName, slotName, "backend"},
			}},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			var release []byte
			if row.release != nil {
				release = row.release(t)
			}
			out, advisories := compileMisnamedFillWorkspace(t,
				misnamedFillCatalog(t, row.slots, row.featureLabels), row.aggregate(t), release)

			// Every violation the run raised is accounted for: a row expecting a
			// build expects none of any kind, and a row expecting the halt expects
			// that halt and nothing beside it.
			if len(out.Analysis.Errors) != len(row.wantHalts) {
				t.Fatalf("violations = %d, want %d: %v", len(out.Analysis.Errors), len(row.wantHalts), out.Analysis.Errors)
			}
			for i, want := range row.wantHalts {
				violation := out.Analysis.Errors[i]
				if !errors.Is(violation.Sentinel, resolver.ErrMisnamedSlotFill()) {
					t.Errorf("violation %d carries %v, want the misnamed-fill identity", i, violation.Sentinel)
				}
				if violation.Kind != analyzer.AnalysisErrorLocated {
					t.Errorf("violation %d kind = %q, want %q", i, violation.Kind, analyzer.AnalysisErrorLocated)
				}
				if want.locator != "" && violation.Field != want.locator {
					t.Errorf("violation %d locator = %q, want %q", i, violation.Field, want.locator)
				}
				if want.file != "" && filepath.Base(violation.OriginFile) != want.file {
					t.Errorf("violation %d is positioned in %q, want %s", i, violation.OriginFile, want.file)
				}
				if want.category != "" {
					if got := violation.ToCompilerError().Category(); got != want.category {
						t.Errorf("violation %d is filed under %q, want %q", i, got, want.category)
					}
				}
				for _, name := range want.names {
					if !strings.Contains(violation.Message, `"`+name+`"`) {
						t.Errorf("violation %d does not name %q: %q", i, name, violation.Message)
					}
				}
			}
			if len(out.Analysis.Warnings) != 0 {
				t.Errorf("a halting rule reported on the advisory stream: %v", out.Analysis.Warnings)
			}

			if got := nearMissKeyPaths(advisories); !slices.Equal(got, row.wantNearMiss) {
				t.Errorf("near-miss advisories at %v, want %v", got, row.wantNearMiss)
			}
		})
	}
}

// TestAMisnamedFillNamingOneSlotAdvisesItsName pins the remedy the halt carries:
// where the Feature fits one empty slot the advice names that slot, and where it
// fits several it names none of them, because which one the author meant is
// theirs to say and the detail already lists every candidate.
func TestAMisnamedFillNamingOneSlotAdvisesItsName(t *testing.T) {
	legs := []struct {
		name       string
		slots      string
		wantAdvice string
	}{
		{
			name:       "one empty slot fitted",
			slots:      selectedSlot(slotName, managedSelector),
			wantAdvice: `Attach it as "` + slotName + `" to fill the slot.`,
		},
		{
			name:       "two empty slots fitted",
			slots:      selectedSlot(slotName, managedSelector) + selectedSlot(secondSlotName, managedSelector),
			wantAdvice: "Attach it under the name of the slot it is meant to fill.",
		},
	}
	for _, leg := range legs {
		t.Run(leg.name, func(t *testing.T) {
			out, _ := compileMisnamedFillWorkspace(t,
				misnamedFillCatalog(t, leg.slots, managedLabels),
				attachOnTheBackendInstance(t, readAggregate(t), freeAttachmentName), nil)
			if len(out.Analysis.Errors) != 1 {
				t.Fatalf("violations = %d, want 1: %v", len(out.Analysis.Errors), out.Analysis.Errors)
			}
			if !strings.HasSuffix(out.Analysis.Errors[0].Message, leg.wantAdvice) {
				t.Errorf("message %q does not close on the advice %q", out.Analysis.Errors[0].Message, leg.wantAdvice)
			}
		})
	}
}
