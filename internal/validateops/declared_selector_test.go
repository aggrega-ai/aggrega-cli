// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
)

// The selectors the fixtures below write. The broken socket selector is a
// well-formed one with its closing parenthesis removed, and the broken slot
// selector opens a value set it never closes, so each is refused by the
// grammar for the same reason a build reaching it refuses it.
const (
	wellFormedSocketSelector = "component.aggrega.ai/category=messaging, component.aggrega.ai/type in (server, stateful-server)"
	brokenSocketSelector     = "component.aggrega.ai/category=messaging, component.aggrega.ai/type in (server, stateful-server"
	wellFormedSlotSelector   = "feature.aggrega.ai/kind=secret"
	brokenSlotSelector       = "feature.aggrega.ai/kind in (secret"
)

// selectorProbeComponent is a Component declaring one dependency socket, one
// slot of its own and one slot on the socket, with the three selectors given.
func selectorProbeComponent(socket, ownSlot, socketSlot string) string {
	return fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/selector-probe
  version: 1.0.0
  labels:
    component.aggrega.ai/type: function
spec:
  usesSchema:
    target_messaging:
      selector: %q
      featuresSchema:
        server_ca:
          selector: %q
  featuresSchema:
    credential:
      selector: %q
`, socket, socketSlot, ownSlot)
}

// selectorProbeFeature is a Feature declaring one dependency socket and one slot,
// with the two selectors given.
func selectorProbeFeature(socket, slot string) string {
	return fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: example/selector-probe-feature
  version: 1.0.0
spec:
  usesSchema:
    upstream:
      selector: %q
  featuresSchema:
    credential:
      selector: %q
`, socket, slot)
}

// TestValidateRefusesAnUnreadableDeclaredSelector drives both validation scopes —
// one entity, and the whole catalog — over a Component or Feature declaring one
// selector the selector grammar refuses, on a socket or a slot nothing wires or
// fills, and asserts the refusal: the condition a build reaching the same
// selector reports it under (selector violation for a socket's selector, the slot
// condition for a slot's), its short code, filed as a catalog fault at the
// entity's file, in the words a build prints — the selector, where it is
// declared, and the grammar's reason — with the remedy naming the declaration.
//
// Each row carries one fault. The controls carry none and must validate, so a
// row cannot pass by reaching a refusal the selectors did not cause.
//
// Without the parse at validation every faulted row validates cleanly on both
// scopes, which is what this fails on.
func TestValidateRefusesAnUnreadableDeclaredSelector(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		doc  string
		// refused is the selector the grammar refuses, empty for a control.
		refused   string
		condition error
		shortCode string
		// declaredAt is the place the message names, rendered as a message reads it.
		declaredAt string
		advice     string
	}{
		{
			name:       "a Component socket's selector the grammar refuses",
			doc:        selectorProbeComponent(brokenSocketSelector, wellFormedSlotSelector, wellFormedSlotSelector),
			refused:    brokenSocketSelector,
			condition:  analyzer.ErrSelectorViolation(),
			shortCode:  "E0007",
			declaredAt: `declared on socket "target_messaging"`,
			advice:     "Correct the selector on the socket's declaration in the component type's usesSchema.",
		},
		{
			name:       "a Component's own slot's selector the grammar refuses",
			doc:        selectorProbeComponent(wellFormedSocketSelector, brokenSlotSelector, wellFormedSlotSelector),
			refused:    brokenSlotSelector,
			condition:  analyzer.ErrSlotSelectorUnsatisfied(),
			shortCode:  "E0008",
			declaredAt: `declared on slot "credential"`,
			advice:     "Correct the selector on the slot's declaration in the component's catalog entity.",
		},
		{
			name:       "a slot a Component's socket declares, whose selector the grammar refuses",
			doc:        selectorProbeComponent(wellFormedSocketSelector, wellFormedSlotSelector, brokenSlotSelector),
			refused:    brokenSlotSelector,
			condition:  analyzer.ErrSlotSelectorUnsatisfied(),
			shortCode:  "E0008",
			declaredAt: `declared on slot "server_ca", of socket "target_messaging"`,
			advice: "Correct the selector on the slot's declaration in the socket's featuresSchema on the " +
				"component's catalog entity.",
		},
		{
			name:       "a Feature socket's selector the grammar refuses",
			doc:        selectorProbeFeature(brokenSocketSelector, wellFormedSlotSelector),
			refused:    brokenSocketSelector,
			condition:  analyzer.ErrSelectorViolation(),
			shortCode:  "E0007",
			declaredAt: `declared on socket "upstream"`,
			advice:     "Correct the selector on the socket's declaration in the Feature's usesSchema.",
		},
		{
			name:       "a Feature slot's selector the grammar refuses",
			doc:        selectorProbeFeature(wellFormedSocketSelector, brokenSlotSelector),
			refused:    brokenSlotSelector,
			condition:  analyzer.ErrSlotSelectorUnsatisfied(),
			shortCode:  "E0008",
			declaredAt: `declared on slot "credential"`,
			advice:     "Correct the selector on the slot's declaration in the Feature's catalog entity.",
		},
		{
			name: "control: a Component whose every selector the grammar reads",
			doc:  selectorProbeComponent(wellFormedSocketSelector, wellFormedSlotSelector, wellFormedSlotSelector),
		},
		{
			name: "control: a Feature whose every selector the grammar reads",
			doc:  selectorProbeFeature(wellFormedSocketSelector, wellFormedSlotSelector),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := t.TempDir()
			catalogDir := filepath.Join(ws, "catalog")
			if err := os.MkdirAll(catalogDir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			path := filepath.Join(catalogDir, "entity.yaml")
			if err := os.WriteFile(path, []byte(tc.doc), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			const cacheDir = ".aggrega/cache"

			var (
				entityRes EntityResult
				entityErr error
				catRes    CatalogResult
				catErr    error
			)
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("validation panicked: %v", r)
					}
				}()
				entityRes, entityErr = ValidateEntity(context.Background(), ws, cacheDir, []string{catalogDir}, nil, path, false)
				catRes, catErr = ValidateCatalog(t.Context(), ws, cacheDir, []string{catalogDir}, nil, nil)
			}()
			if catErr != nil {
				t.Fatalf("the whole-catalog check could not load the catalog: %v", catErr)
			}

			if tc.refused == "" {
				if entityErr != nil || !entityRes.Valid {
					t.Fatalf("the control must validate as one entity; valid=%v err=%v", entityRes.Valid, entityErr)
				}
				if !catRes.Valid || len(catRes.SelectorErrs) != 0 {
					t.Fatalf("the control must pass the whole-catalog check; valid=%v selector errors=%v",
						catRes.Valid, catRes.SelectorErrs)
				}
				return
			}

			_, parseErr := selector.Parse(tc.refused)
			if parseErr == nil {
				t.Fatalf("fixture: the grammar must refuse %q for this row to mean anything", tc.refused)
			}
			wantDetail := fmt.Sprintf("(%q, %s, which could not be evaluated: %q)", tc.refused, tc.declaredAt, parseErr.Error())

			assertRefusal := func(scope string, err error) {
				t.Helper()
				if !errors.Is(err, tc.condition) {
					t.Errorf("%s: the refusal must match %v; got %v", scope, tc.condition, err)
				}
				if code, ok := cerrors.ShortCodeOf(err); !ok || code != tc.shortCode {
					t.Errorf("%s: the short code must be %s; got %q (found %v)", scope, tc.shortCode, code, ok)
				}
				var head *cerrors.CompilerError
				if !errors.As(err, &head) || head == nil {
					t.Fatalf("%s: the refusal carries no diagnostic to lead with: %v", scope, err)
				}
				if head.Category() != cerrors.CatalogError {
					t.Errorf("%s: the refusal must be filed as a catalog fault; got %q", scope, head.Category())
				}
				if head.Source().File != path {
					t.Errorf("%s: the refusal must name the entity file %q; got %q", scope, path, head.Source().File)
				}
				msg := head.Message()
				for _, want := range []string{tc.condition.Error(), wantDetail, tc.advice} {
					if !strings.Contains(msg, want) {
						t.Errorf("%s: the refusal must carry %q; got:\n%s", scope, want, msg)
					}
				}
			}

			if entityErr == nil {
				t.Fatalf("one entity: a document declaring %q must be refused; it validated (valid=%v)", tc.refused, entityRes.Valid)
			}
			if !strings.HasPrefix(entityErr.Error(), "shape: ") {
				t.Errorf("one entity: the refusal must be reported under the shape stage; got %q", entityErr.Error())
			}
			assertRefusal("one entity", entityErr)

			if catRes.Valid || len(catRes.SelectorErrs) != 1 {
				t.Fatalf("whole catalog: one refused selector must fail the check with one finding; valid=%v selector errors=%v",
					catRes.Valid, catRes.SelectorErrs)
			}
			assertRefusal("whole catalog", catRes.SelectorErrs[0])
		})
	}
}
