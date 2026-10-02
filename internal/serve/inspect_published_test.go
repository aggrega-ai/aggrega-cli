// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestEntityDefinitionReadsARegistryReferenceSentAsTheName pins that a registry
// reference sent as the by-name request's `name` is routed to the published-entity
// reading rather than to the catalog, and that a reading which cannot answer is
// refused with entity-not-resolvable carrying the named condition.
//
// The workspace is offline, so the reading is refused at the egress gate and the
// proof reaches no network; that refusal is only reachable through the reading,
// which is what makes it evidence of the route. The server holds no catalog view
// at all, so a call that fell through to the catalog path would not get as far as
// a refusal. Reading a coordinate that exists is driven against a registry by this
// package's integration-tagged proof, through the same dispatch.
func TestEntityDefinitionReadsARegistryReferenceSentAsTheName(t *testing.T) {
	t.Parallel()
	s := &Server{wsConfig: workspaceconfig.Config{Offline: true}}

	result, envelope := s.entityDefinition(context.Background(), "oci://registry.example.com/component/core/api:1.0.0", s.workspaceTerms())
	if envelope == nil {
		t.Fatalf("an offline workspace answered a registry reference: %+v", result)
	}
	if envelope.Code != errEntityNotResolvable {
		t.Errorf("code = %d; want entity-not-resolvable (%d)", envelope.Code, errEntityNotResolvable)
	}
	if !errors.Is(envelope.cause, egress.ErrOfflineEgress()) {
		t.Errorf("cause = %v; want the offline refusal the reading raises", envelope.cause)
	}
	if !strings.HasPrefix(envelope.Message, "entityDefinition: E0125: ") {
		t.Errorf("message = %q; want it to open with the method and the condition's short code", envelope.Message)
	}
}
