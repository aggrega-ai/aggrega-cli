// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// assertLeadsOnce pins the composition every catalog diagnostic follows: the
// condition's registered sentence opens the message and is stated exactly ONCE.
//
// The occurrence count is the half worth having. A message that opens with the
// right sentence and also embeds it further along says one thing twice, and a
// prefix assertion on its own cannot see that.
func assertLeadsOnce(t *testing.T, err error, condition error) {
	t.Helper()
	sentence, ok := cerrors.MessageOf(condition)
	if !ok || sentence == "" {
		t.Fatalf("condition carries no registered sentence")
	}
	ce, ok := err.(*cerrors.CompilerError)
	if !ok {
		t.Fatalf("diagnostic is not a CompilerError: %T", err)
	}
	if !strings.HasPrefix(ce.Message, sentence) {
		t.Errorf("message does not lead with its registered sentence\n  sentence: %q\n  message : %q", sentence, ce.Message)
	}
	if n := strings.Count(ce.Message, sentence); n != 1 {
		t.Errorf("message states the registered sentence %d times; want exactly 1\n  message: %q", n, ce.Message)
	}
}

func TestDuplicateEntityIdentityLeadsWithItsConditionsSentence(t *testing.T) {
	t.Parallel()
	c := &Catalog{}
	if err := c.claimIdentity("component:core/datastore/mysql:1.0.0", `Component "core/datastore/mysql:1.0.0"`, "first.yaml"); err != nil {
		t.Fatalf("first claim must succeed: %v", err)
	}
	err := c.claimIdentity("component:core/datastore/mysql:1.0.0", `Component "core/datastore/mysql:1.0.0"`, "second.yaml")
	if err == nil {
		t.Fatal("a second claim on one identity must halt")
	}
	assertLeadsOnce(t, err, ErrDuplicateEntityIdentity())
}

func TestManifestImplementsMismatchLeadsWithItsConditionsSentence(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(fakeRecipe("core/helm/wrong-recipe", "core/datastore/mysql"))
	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/datastore/postgres", "core/helm/wrong-recipe")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: flatComponents}},
	}
	errs := c.VerifyManifestImplementsConsistency(manifest, "manifest.yaml", c.ComponentAncestry)
	if len(errs) == 0 {
		t.Fatal("a mapping whose recipe implements another type must halt")
	}
	assertLeadsOnce(t, errs[0], ErrManifestImplementsMismatch())
}
