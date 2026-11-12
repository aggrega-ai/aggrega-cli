// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The dependency blocks this proof compiles in place of the one the fixture
// ships.
//
// Each replaces the block whole rather than adding to it, because the shape
// under proof is how many connections the block draws and what each delivers —
// which cannot be expressed by appending to a block whose only connection is
// already written.
const (
	// The block the fixture ships, quoted whole so every shape below replaces
	// exactly it. A splice whose anchor has drifted out of the fixture fails
	// rather than compiling the unspliced document.
	frontEndUsesBlock = "      uses:\n" +
		"        apis:\n" +
		"          my-api:\n" +
		"            target: backend\n" +
		"            inject:\n" +
		"              env:\n" +
		"                - in: API_HOST\n" +
		"                  from: host\n" +
		"                - in: API_PORT\n" +
		"                  from: service_port\n"

	// Two connections on two sockets, both delivering one key under one
	// mechanism. They read from different published keys, so the value one
	// displaces is a different string from the value that survives — which is
	// what made the defect observable in the rendered bytes before this rule.
	collidingAcrossTwoSockets = "      uses:\n" +
		"        apis:\n" +
		"          my-api:\n" +
		"            target: backend\n" +
		"            inject:\n" +
		"              env:\n" +
		"                - in: SHARED_KEY\n" +
		"                  from: host\n" +
		"        backups:\n" +
		"          my-backup:\n" +
		"            target: backend\n" +
		"            inject:\n" +
		"              env:\n" +
		"                - in: SHARED_KEY\n" +
		"                  from: service_port\n"

	// Two connections on ONE socket under two aliases, delivering one key. The
	// socket names both of them identically, so this is the shape a finding
	// carrying only a socket name could not separate.
	collidingOnOneSocket = "      uses:\n" +
		"        apis:\n" +
		"          my-api:\n" +
		"            target: backend\n" +
		"            inject:\n" +
		"              env:\n" +
		"                - in: SHARED_KEY\n" +
		"                  from: host\n" +
		"          my-second-api:\n" +
		"            target: backend\n" +
		"            inject:\n" +
		"              env:\n" +
		"                - in: SHARED_KEY\n" +
		"                  from: service_port\n"

	// The negative control. Everything about the shape is the colliding one
	// except the two keys, so a rule refusing this would be refusing the
	// connection count rather than the collision.
	uncollidingAcrossTwoSockets = "      uses:\n" +
		"        apis:\n" +
		"          my-api:\n" +
		"            target: backend\n" +
		"            inject:\n" +
		"              env:\n" +
		"                - in: FIRST_KEY\n" +
		"                  from: host\n" +
		"        backups:\n" +
		"          my-backup:\n" +
		"            target: backend\n" +
		"            inject:\n" +
		"              env:\n" +
		"                - in: SECOND_KEY\n" +
		"                  from: service_port\n"
)

// twoSocketWebUICatalog copies the fixture catalog and declares a second
// dependency socket on the front end's component type.
//
// It copies rather than editing in place because the fixture on disk is shared
// with the package's other proofs and has to keep producing what they expect.
func twoSocketWebUICatalog(t *testing.T) string {
	t.Helper()
	catalog := copyCatalog(t)
	spliceInFile(t, filepath.Join(catalog, "base/components/web-ui.yaml"),
		"  usesSchema:\n"+
			"    apis:\n      selector: \"component.aggrega.ai/type=server\"\n      inject: [\"env\"]\n",
		"  usesSchema:\n"+
			"    apis:\n      selector: \"component.aggrega.ai/type=server\"\n      inject: [\"env\"]\n"+
			"    backups:\n      selector: \"component.aggrega.ai/type=server\"\n      inject: [\"env\"]\n")
	return catalog
}

// compileWithUsesBlock compiles the fixture with the front end's dependency
// block replaced by the one given, against a catalog declaring both sockets.
func compileWithUsesBlock(t *testing.T, block string) *stepsOneToFourOutput {
	t.Helper()
	aggregate := spliceBytes(t, readAggregate(t), frontEndUsesBlock, block)
	out, _ := compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   twoSocketWebUICatalog(t),
	}, aggregate)
	return out
}

// TestTheConnectionCollisionRuleIsInvokedOverTheCompiledGraph reads the rule's
// verdict off the production path — the pre-render phases as the build runs them
// — on the documents it refuses and on the ones it admits.
//
// The verdict is read from the analysis the phases publish rather than from a
// traversal this test drives, because that is the whole claim: the rule is asked
// during a compilation and what it raises reaches the slice the build halts on. A
// traversal driven from here would prove the rule computes the right answer and
// say nothing about whether anything asks it.
//
// What it pins is the pairing that produced no finding at all before it: two of a
// component's dependency connections delivering one key under one mechanism. That
// build was green and the later delivery displaced the earlier in the rendered
// bytes, with nothing saying which or why.
func TestTheConnectionCollisionRuleIsInvokedOverTheCompiledGraph(t *testing.T) {
	for _, tc := range []struct {
		name string
		// block is the front end's dependency block as this row authors it.
		block string
		// wantPositions is each expected finding's locator, in the order the
		// graph holds the connections. Empty means the document is admitted.
		wantPositions []string
		// wantMessages is each expected finding's whole message, in the same
		// order. It is the whole string rather than the values inside it
		// because what an author needs is to tell the values apart, and a check
		// reading each one out separately passes on a message that does not.
		wantMessages []string
	}{
		{
			name:  "two connections on two sockets delivering one key are both refused",
			block: collidingAcrossTwoSockets,
			wantPositions: []string{
				".spec.components.frontend.uses.apis.my-api.inject.env.SHARED_KEY",
				".spec.components.frontend.uses.backups.my-backup.inject.env.SHARED_KEY",
			},
			wantMessages: []string{
				`duplicate delivery key across inject sources ("inject.env.SHARED_KEY", "apis", "backups")`,
				`duplicate delivery key across inject sources ("inject.env.SHARED_KEY", "apis", "backups")`,
			},
		},
		{
			// The finding names its socket twice here, and the repetition is the
			// signal rather than a defect: a socket appears once per connection
			// named for it, so one name twice is what says the two connections
			// share a socket. What separates them for the author is the
			// position, which is why both are asserted.
			name:  "two connections on one socket under two aliases are both refused",
			block: collidingOnOneSocket,
			wantPositions: []string{
				".spec.components.frontend.uses.apis.my-api.inject.env.SHARED_KEY",
				".spec.components.frontend.uses.apis.my-second-api.inject.env.SHARED_KEY",
			},
			wantMessages: []string{
				`duplicate delivery key across inject sources ("inject.env.SHARED_KEY", "apis", "apis")`,
				`duplicate delivery key across inject sources ("inject.env.SHARED_KEY", "apis", "apis")`,
			},
		},
		{
			name:  "two connections delivering different keys reach the render",
			block: uncollidingAcrossTwoSockets,
		},
		{
			// The ordinary shape, and the one a rule reading the wrong threshold
			// would refuse: a single connection delivering two keys is every
			// aggregate that wires anything at all.
			name:  "one connection delivering two keys reaches the render",
			block: frontEndUsesBlock,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := compileWithUsesBlock(t, tc.block)

			// The rule must have been asked about something. A connection set
			// that came back empty would raise nothing and read exactly like a
			// rule that ran and approved, so what it was asked about is measured
			// before its verdict is read.
			asked := 0
			for _, edge := range out.GraphCoordinates.Edges() {
				delivery := out.GraphView.PerEdgeInject(
					edge.Node.ID, edge.Socket, edge.Alias)
				for _, mechanism := range delivery.Keys() {
					delivered, _ := delivery.Get(mechanism)
					asked += len(delivered.Keys())
				}
			}
			if asked == 0 {
				t.Fatal("no connection delivers anything; a verdict over this document would certify nothing")
			}

			var positions, messages []string
			for i, finding := range out.Analysis.Errors {
				if !errors.Is(finding.Sentinel, resolver.ErrInjectKeyCollision()) {
					t.Errorf("finding %d carries %v, want the collision condition", i, finding.Sentinel)
					continue
				}
				if finding.OriginFile != invocationAggregatePath {
					t.Errorf("finding %d names document %q, want %q",
						i, finding.OriginFile, invocationAggregatePath)
				}
				positions = append(positions, finding.Field)
				messages = append(messages, finding.Message)
			}

			if !slices.Equal(positions, tc.wantPositions) {
				t.Errorf("findings are positioned at %v, want %v", positions, tc.wantPositions)
			}
			if !slices.Equal(messages, tc.wantMessages) {
				t.Errorf("the findings read %v, want %v", messages, tc.wantMessages)
			}
			t.Logf("the rule was asked about %d delivery entr(ies) and raised %d finding(s)",
				asked, len(messages))
		})
	}
}
