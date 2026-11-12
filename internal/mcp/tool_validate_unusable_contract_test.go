// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// unusableContractComponent declares a pillar whose body COMPILES and then
// builds no validator: `items` carries a bare scalar where a schema belongs.
//
// The `keep` sibling is a well-formed constraint declared beside it. It is what
// would go unenforced were the contract passed over, so it is the reason the
// refusal is worth more than the constraints it costs.
const unusableContractComponent = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/unusable
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      bad: { type: array, items: nonsense }
      keep: { type: integer }
`

// TestValidateToolRefusesAnUnusableContract drives the tool protocol over an
// entity declaring a contract no validator can be built from.
//
// It is the tool-protocol leg of one claim asserted on three surfaces: the same
// document is driven through the command line's build and validation entries in
// the resolver package's contract corpus, and through the editor session in the
// daemon's Tier-4 parity cases. A refusal one surface reports and another
// swallows is a divergence rather than one behaviour with three presentations,
// and it is worse than the silence being removed, because the author would be
// told twice that nothing is wrong.
//
// The condition is asserted on the CORE, where an identity exists to match, and
// the payload is asserted to carry that verdict verbatim — the tool serves
// strings, so a condition cannot be matched on the payload itself. Asserting
// only the payload's text would pass on any refusal at all.
//
// INVARIANT: the tool protocol refuses a declared contract nothing can be
// measured against, under the same condition every other surface refuses it
// with, and names the authored field the correction goes in.
// CONSEQUENCE: were this to fail, an assistant driving the tool would be told a
// workspace is fine while a build refuses it — the divergence this claim exists
// to close.
func TestValidateToolRefusesAnUnusableContract(t *testing.T) {
	s := newTestServer(t)

	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	entityPath := filepath.Join(catalogDir, "components", "unusable.yaml")
	if err := os.MkdirAll(filepath.Dir(entityPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(entityPath, []byte(unusableContractComponent), 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	dirs := []string{catalogDir}
	tw := resolvedToolWorkspace(t, s, root, dirs)

	// The core first, so the condition is established before the payload is
	// compared against it. A payload comparison alone would be satisfied by a
	// refusal for an unrelated reason.
	_, coreErr := validateops.ValidateEntity(context.Background(),
		tw.root, tw.cacheDir, tw.dirs, tw.named, resolvedEntityPath(t, tw, entityPath), false)
	if coreErr == nil {
		t.Fatalf("the core accepted a contract no validator can be built from.\n" +
			"INVARIANT: a declared contract nothing can be measured against is refused.\n" +
			"CONSEQUENCE: the payload comparison below is asserted over a passing fixture.")
	}
	if !errors.Is(coreErr, resolver.ErrSchemaContractCompileFailure()) {
		t.Fatalf("the core refused with %v; want the compile-failure condition.\n"+
			"INVARIANT: the refusal carries the identity of the condition it met.\n"+
			"CONSEQUENCE: a caller matching on the condition stops seeing this refusal.", coreErr)
	}
	if !strings.Contains(coreErr.Error(), "spec.schema.values") {
		t.Errorf("the refusal does not name the authored field: %v\n"+
			"INVARIANT: the refusal locates the contract at the field it was declared in.\n"+
			"CONSEQUENCE: an author is told a contract is unreadable and not which one.", coreErr)
	}

	res := callTool(t, s, "validate", validateArgs{
		Entity:        entityPath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	payload := unmarshalValidatePayload(t, res)
	assertHaltParity(t, payload, coreErr)
}
