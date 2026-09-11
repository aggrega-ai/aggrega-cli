// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
)

// The fixture both arms below run over: two published versions of one plugin, of
// which the newer is withdrawn and the older is good.
//
// The assertions are derived from these constants — what the test PLANTED — rather
// than from the operation's own answer or from the wire projection of it. An
// expectation read off either side is computed by the thing under test and agrees
// with it whatever it does, which is how a single-sourcing assertion goes blind.
const (
	wireRetractedNewest = "2.0.0"
	wireRetractedOlder  = "1.2.0"
	wireRetractedReason = "withdrawn in favour of a successor"
	wireRetractedRef    = "hub.aggrega.ai/plugin/governance/tag-injector"
)

// TestThePluginAddAnswerCarriesTheRetractionAdvisory is the invocation-level proof
// that the substitution reaches a caller over the request surface, plus the control
// that keeps the assertion from passing over an empty advisory.
//
// Both arms drive the METHOD and read the PAYLOAD, rather than the projection
// function that composes it: a projection asserted directly passes while nothing
// calls it, which is exactly the shape that leaves a field declared and unreachable.
//
// 🔴 The second arm is not decoration. "The advisory names 2.0.0" passes on a
// surface that reports one unconditionally, and only a run over a repository with
// nothing withdrawn says that the advisory tracks the screen rather than the
// request.
func TestThePluginAddAnswerCarriesTheRetractionAdvisory(t *testing.T) {
	// Not parallel: the plugin layer's registry seams are process-global.
	for _, tc := range []struct {
		name      string
		withdrawn map[string]string
		wantPin   string
	}{
		{
			name:      "a withdrawn newest version",
			withdrawn: map[string]string{wireRetractedNewest: wireRetractedReason},
			wantPin:   wireRetractedOlder,
		},
		{
			name:      "nothing withdrawn",
			withdrawn: nil,
			wantPin:   wireRetractedNewest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFakeRegistryForAttachWithdrawing(t, []string{wireRetractedOlder, wireRetractedNewest}, tc.withdrawn)
			srv, _, _ := registryWriteFixture(t, `workspace:
  catalog_dirs:
    - catalog
default_registry: hub.aggrega.ai
`)
			resp := callDaemonMethod(t, srv, "aggrega/pluginAdd", map[string]any{
				"type": "governance", "query": "tag-injector", "role": "label-transform",
			})
			if resp.Error != nil {
				t.Fatalf("expected a result; got error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			// The payload as it goes out on the wire, logged so what a client actually
			// receives is readable rather than inferred from the struct it decodes into.
			t.Logf("aggrega/pluginAdd answered:\n%s", resp.Result)

			var got pluginAddResult
			decodeDaemonResult(t, resp, &got)
			if !strings.HasSuffix(got.Identity, ":"+tc.wantPin) {
				t.Fatalf("pinned %q; want a %s attachment — the fixture is wrong, not the projection", got.Identity, tc.wantPin)
			}

			if tc.withdrawn == nil {
				if got.Retraction != nil {
					t.Fatalf("an attachment that screened cleanly answered with an advisory: %#v", got.Retraction)
				}
				// The key is absent from the bytes, not merely nil after decoding: a client
				// reading the payload as JSON must see nothing rather than an empty object.
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(resp.Result, &raw); err != nil {
					t.Fatalf("unmarshal the payload: %v", err)
				}
				if _, present := raw["retraction"]; present {
					t.Errorf("the payload carries a retraction key on an attachment that withdrew nothing: %s", resp.Result)
				}
				return
			}

			if got.Retraction == nil {
				t.Fatal("the answer carries no retraction; a reader handed an older version than the newest published one learns nothing about why")
			}
			if got.Retraction.Reference != wireRetractedRef {
				t.Errorf("retraction.reference = %q; want %q", got.Retraction.Reference, wireRetractedRef)
			}
			if got.Retraction.Selected != wireRetractedOlder {
				t.Errorf("retraction.selected = %q; want %q", got.Retraction.Selected, wireRetractedOlder)
			}
			if got.Retraction.Unscreened != "" {
				t.Errorf("retraction.unscreened = %q on a run whose markers read cleanly", got.Retraction.Unscreened)
			}
			// A count and equalities rather than a containment check over the payload
			// text: "the answer mentions 2.0.0" is satisfied by the version appearing
			// anywhere, the pinned identity included, so it would pass on a surface
			// carrying no advisory at all.
			if len(got.Retraction.Excluded) != 1 {
				t.Fatalf("retraction.excluded holds %d entries; want exactly 1: %#v", len(got.Retraction.Excluded), got.Retraction.Excluded)
			}
			if got.Retraction.Excluded[0].Version != wireRetractedNewest {
				t.Errorf("retraction.excluded[0].version = %q; want %q", got.Retraction.Excluded[0].Version, wireRetractedNewest)
			}
			if got.Retraction.Excluded[0].Reason != wireRetractedReason {
				t.Errorf("retraction.excluded[0].reason = %q; want %q — the recorded reason is the compiler's own text and is carried, not reworded",
					got.Retraction.Excluded[0].Reason, wireRetractedReason)
			}
		})
	}
}

// TestTheRequestSurfaceKeepsVersionSelectionScreened pins why the notices writer is
// a discarding one rather than absent, which is the kind of simplification a later
// reader makes on the grounds that nothing reads the prose.
//
// Version selection is screened only when there is somewhere to announce a
// substitution. Passing no writer would leave it unscreened, and this surface would
// then pin the withdrawn version — silently, with a green answer and an empty
// advisory, which is a worse outcome than reporting no advisory at all.
func TestTheRequestSurfaceKeepsVersionSelectionScreened(t *testing.T) {
	// Not parallel: the plugin layer's registry seams are process-global.
	withFakeRegistryForAttachWithdrawing(t, []string{wireRetractedOlder, wireRetractedNewest},
		map[string]string{wireRetractedNewest: wireRetractedReason})
	srv, _, wsPath := registryWriteFixture(t, `workspace:
  catalog_dirs:
    - catalog
default_registry: hub.aggrega.ai
`)
	var got pluginAddResult
	decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/pluginAdd", map[string]any{
		"type": "governance", "query": "tag-injector", "role": "label-transform",
	}), &got)

	// The file, not only the answer: a handler that answered with the older version
	// and wrote the withdrawn one would satisfy a response-only assertion.
	written := readConfigBytes(t, wsPath)
	if !strings.Contains(written, "governance/tag-injector:"+wireRetractedOlder) {
		t.Errorf("the configuration does not carry the substituted version:\n%s", written)
	}
	if strings.Contains(written, "governance/tag-injector:"+wireRetractedNewest) {
		t.Errorf("the withdrawn version was attached:\n%s", written)
	}
}

// TestTheAdvisoryProjectionCarriesEveryValueUnchanged holds the projection to
// carrying rather than deciding.
//
// It is a unit assertion beside the invocation proofs above rather than instead of
// them: what the method drives is one advisory shape, and a projection that dropped
// a field only the degraded posture fills would keep passing there. The absent case
// is asserted too, because a projection returning an empty value rather than nothing
// puts a retraction key on every answer.
func TestTheAdvisoryProjectionCarriesEveryValueUnchanged(t *testing.T) {
	t.Parallel()

	if got := pluginRetractionOf(nil); got != nil {
		t.Errorf("an absent advisory projected to %#v; want nil so the field is omitted", got)
	}

	got := pluginRetractionOf(&pluginops.RetractionAdvisory{
		Reference:  wireRetractedRef,
		Unscreened: "the registry refused the referrer listing",
	})
	if got == nil {
		t.Fatal("a degraded advisory projected to nothing")
	}
	if got.Unscreened != "the registry refused the referrer listing" {
		t.Errorf("unscreened = %q; the fault is what a reader can act on", got.Unscreened)
	}
	if got.Reference != wireRetractedRef {
		t.Errorf("reference = %q; want %q", got.Reference, wireRetractedRef)
	}
}
