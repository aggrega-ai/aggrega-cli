// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"os"
	"strings"
	"testing"
)

// TestEveryMethodRefusesAnUndeclaredRequestField is the contract a caller has to
// be able to rely on: a field the method does not declare is refused, naming it,
// rather than dropped.
//
// 🔴 It drives EVERY method the daemon routes rather than the handful this
// discipline arrived with, and that breadth is the point. A refusal on one method
// while its siblings stay permissive is a refusal a caller cannot rely on — they
// would have to know, per method, whether their argument was read, which is
// exactly the thing they sent the argument to avoid finding out the hard way.
//
// The reason refusing beats ignoring is that the two outcomes are not close
// together. A caller that sends a field asking for the write to be withheld, and
// has that field dropped, gets the write and a success answer: it asked for a
// rehearsal and got the thing itself, with nothing in the response to tell it
// apart from a request whose field was honoured.
//
// The invented field is spelled so it can never collide with a real one, and the
// assertion is that the refusal NAMES it — a generic decode complaint would pass
// a weaker check while telling a caller nothing about which argument was wrong.
func TestEveryMethodRefusesAnUndeclaredRequestField(t *testing.T) {
	t.Parallel()

	const invented = "zzz_not_a_declared_field"

	methods := Methods()
	if len(methods) == 0 {
		t.Fatal("the routed-method census is empty, so this test asserts nothing")
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			srv, _, _ := registryWriteFixture(t, "workspace: {}\n")
			resp := callDaemonMethod(t, srv, method, map[string]any{invented: true})
			if resp.Error == nil {
				t.Fatalf("%s accepted an undeclared field and answered with a result: %s\n"+
					"a silently ignored argument reads to a caller as an argument that was honoured",
					method, resp.Result)
			}
			if !strings.Contains(resp.Error.Message, invented) {
				t.Errorf("%s refused, but does not name the field it refused: %q", method, resp.Error.Message)
			}
			if !strings.Contains(resp.Error.Message, "does not accept") {
				t.Errorf("%s refusal does not read as an unknown-argument refusal: %q", method, resp.Error.Message)
			}
		})
	}
}

// TestTheRoutingFieldsAreAcceptedOnAMethodThatDeclaresNeither is the negative
// control for the test above, and without it that test would be satisfied by a
// decoder that refused everything.
//
// 🔴 It is also the specific regression the strict read is one line away from
// causing. Two fields are read off a request's RAW params by the dispatch path —
// the workspace pointer and the progress token — and they belong to no method's
// own shape, because routing has to read them before it knows which shape the
// request even has. A strict read of one method's declared set alone would refuse
// them on every well-formed request that carried one, which is a daemon that
// answers nothing.
//
// The method chosen declares neither, so the acceptance cannot come from the
// shape and must come from the routing union.
func TestTheRoutingFieldsAreAcceptedOnAMethodThatDeclaresNeither(t *testing.T) {
	t.Parallel()

	declared := declaredParamFields(configInitParams{})
	for _, field := range []string{"workspaceRoot", "workDoneToken"} {
		if declared[field] {
			t.Fatalf("configInitParams declares %q, so this test no longer proves the routing union carries it", field)
		}
		if !routingParamFields[field] {
			t.Errorf("%q is read off a request's raw params but is not in the routing union, so every request carrying it is refused", field)
		}
	}

	srv, globalPath, _ := registryWriteFixture(t, "workspace: {}\n")
	// The fixture seeds the global layer, and the existing-file guard now applies
	// under every delivery — so the file is cleared rather than forced, keeping
	// this test's subject the routing union alone.
	if err := os.Remove(globalPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clear the global layer: %v", err)
	}
	resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{
		"registry":      "routing.example.test",
		"dryRun":        true,
		"workspaceRoot": srv.workspaceRoot,
	})
	var res configInitResult
	decodeDaemonResult(t, resp, &res)
	if res.Scope.ConfigFile != globalPath {
		t.Errorf("configFile: got %q, want %q", res.Scope.ConfigFile, globalPath)
	}
}

// TestTheRetiredDeliverySpellingIsRefusedRatherThanIgnored is where the rename
// and the strict read meet, and it is the case the rename would otherwise have
// made silently worse.
//
// Before the strict read, a client still sending the retired spelling with
// otherwise valid params got a WRITE and a success answer, having asked for a
// rehearsal. The old word now gets this surface's own unknown-argument refusal,
// so such a client learns its argument was not honoured instead of discovering it
// from the file on disk.
func TestTheRetiredDeliverySpellingIsRefusedRatherThanIgnored(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		method  string
		retired string
		params  map[string]any
	}{
		{"aggrega/configInit", "report", map[string]any{"registry": "retired.example.test"}},
		{"aggrega/import", "report", map[string]any{"source": "flow.json", "outputDir": "out"}},
		{"aggrega/editDocument", "report", map[string]any{"file": "app.yaml", "edit": "set-value"}},
		{"aggrega/pluginGenCatalog", "report", map[string]any{"fromEmbedded": true, "overlay": "o", "outputDir": "out"}},
		{"aggrega/pluginAdd", "dry_run", map[string]any{"type": "governance", "query": "x", "role": "label-transform"}},
		// The registry registration is the method the defect was measured on: it
		// honours the delivery internally through the shared registration layer
		// while declaring no field for it, so the field was dropped and the write
		// went ahead.
		{"aggrega/registryAdd", "dryRun", map[string]any{"host": "retired.example.test"}},
	} {
		t.Run(tc.method+"/"+tc.retired, func(t *testing.T) {
			t.Parallel()
			srv, _, _ := registryWriteFixture(t, "workspace: {}\n")
			params := map[string]any{tc.retired: true}
			for k, v := range tc.params {
				params[k] = v
			}
			resp := callDaemonMethod(t, srv, tc.method, params)
			if resp.Error == nil {
				t.Fatalf("%s accepted the retired spelling %q and answered with a result: %s",
					tc.method, tc.retired, resp.Result)
			}
			if !strings.Contains(resp.Error.Message, tc.retired) {
				t.Errorf("%s refusal does not name %q: %q", tc.method, tc.retired, resp.Error.Message)
			}
		})
	}
}

// TestTheAcceptedFieldSetNamesTheSurfaceRatherThanOnlyTheFault pins the half of
// the refusal that makes it actionable: a caller reading it learns what the
// method DOES take, so a misspelling is corrected in one round trip rather than
// by guessing.
func TestTheAcceptedFieldSetNamesTheSurfaceRatherThanOnlyTheFault(t *testing.T) {
	t.Parallel()

	accepted := acceptedParamFields(configInitParams{})
	for _, want := range []string{"dryRun", "force", "global", "registry", "workspace", "workspaceRoot", "workDoneToken"} {
		found := false
		for _, got := range accepted {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the accepted set omits %q: %v", want, accepted)
		}
	}
	for i := 1; i < len(accepted); i++ {
		if accepted[i-1] >= accepted[i] {
			t.Errorf("the accepted set is not sorted, so one request produces one message only by luck: %v", accepted)
			break
		}
	}
}
