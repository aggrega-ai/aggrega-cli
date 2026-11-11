// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The fixture both arms below run over: two published versions of one plugin, of
// which the newer is withdrawn and the older is good.
//
// The assertions are derived from these constants — what the test PLANTED — rather
// than from the operation's own answer or from the wire projection of it. An
// expectation read off either side is computed by the thing under test and agrees
// with it whatever it does, which is how a single-sourcing assertion goes blind.
//
// The host and the repository are named apart and the reference composed from them,
// because the snapshot fixture below addresses the two separately — an index is
// stored per host and per repository — while the answers name the whole reference.
// Composing it is what keeps the two spellings from drifting.
const (
	wireRetractedNewest = "2.0.0"
	wireRetractedOlder  = "1.2.0"
	wireRetractedReason = "withdrawn in favour of a successor"
	wireRetractedHost   = "hub.aggrega.ai"
	wireRetractedRepo   = "plugin/governance/tag-injector"
	wireRetractedRef    = wireRetractedHost + "/" + wireRetractedRepo
)

// The provenance spellings the wire is asserted against.
//
// They are written out rather than read off registryindex.ProvenanceSource, because
// an expectation taken from the constant the projection carries across agrees with
// it however it is respelled — and the spelling is the contract a client matches on,
// so a rename must fail here rather than pass quietly.
const (
	wireProvenanceRegistry   = "registry"
	wireProvenanceLocalIndex = "local-index"
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
				if _, present := raw[wireOptionalProperty(t, "#/$defs/PluginAddResult", "retraction")]; present {
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
			// This fixture builds no local snapshot, so the withdrawal was read from the
			// registry while the request ran. The source is what says so, and asserting it
			// here is what separates this run from the snapshot-backed one below: without
			// it, both would assert only that SOME provenance arrived.
			if got.Retraction.Provenance == nil {
				t.Fatal("the answer carries no provenance; a caller cannot tell a reading taken just now from a snapshot a refresh would settle")
			}
			if got.Retraction.Provenance.Source != wireProvenanceRegistry {
				t.Errorf("retraction.provenance.source = %q; want %q — these facts were read during the request",
					got.Retraction.Provenance.Source, wireProvenanceRegistry)
			}
			if got.Retraction.Provenance.LastUpdated != "" {
				t.Errorf("retraction.provenance.lastUpdated = %q on a reading taken during the request; there is no snapshot age to report",
					got.Retraction.Provenance.LastUpdated)
			}
			// The key is absent from the bytes, not merely empty after decoding: a client
			// offered a blank age cannot tell "there is none" from "this surface failed to
			// fill it".
			provenance, present := wireRetractionProvenanceKeys(t, resp.Result)
			if !present {
				t.Fatalf("the payload carries no provenance key: %s", resp.Result)
			}
			// The whole key set against the schema, rather than a probe for the age
			// alone: facts read during the request have no staleness to report, so
			// the object must be exactly what the position requires and nothing
			// more — and asserting the set is also what catches the source key
			// arriving under a spelling the contract does not declare, which a
			// decode into the struct cannot see.
			assertWireKeys(t, "the advisory's provenance", "#/$defs/PluginRetractionProvenance",
				provenance, nil, resp.Result)
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

// wireRetractionProvenanceKeys reads the answer's retraction provenance back out of
// the payload BYTES as raw keys, and reports whether the object was there at all.
//
// It reads the bytes rather than the decoded value because absence and an empty
// value are indistinguishable once decoded, and which of the two a client receives is
// the contract: an age that is simply not reported must be a missing key, since a
// client handed a blank one cannot tell it from a field this surface failed to fill.
//
// The two keys it descends through are named against the shipped schema rather than
// written out, so a respelling on either position fails this read instead of leaving
// it hunting a key the published contract no longer has — and the same resolution
// asserts each is OPTIONAL, which is what makes reporting its absence meaningful
// rather than a claim that the wire broke its own contract.
func wireRetractionProvenanceKeys(t *testing.T, payload json.RawMessage) (map[string]any, bool) {
	t.Helper()

	var answer map[string]json.RawMessage
	if err := json.Unmarshal(payload, &answer); err != nil {
		t.Fatalf("unmarshal the payload: %v", err)
	}
	rawRetraction, present := answer[wireOptionalProperty(t, "#/$defs/PluginAddResult", "retraction")]
	if !present {
		t.Fatalf("the payload carries no retraction at all, so there is no provenance to read: %s", payload)
	}
	var retraction map[string]json.RawMessage
	if err := json.Unmarshal(rawRetraction, &retraction); err != nil {
		t.Fatalf("unmarshal the retraction: %v", err)
	}
	rawProvenance, present := retraction[wireOptionalProperty(t, "#/$defs/PluginRetraction", "provenance")]
	if !present {
		return nil, false
	}
	var provenance map[string]any
	if err := json.Unmarshal(rawProvenance, &provenance); err != nil {
		t.Fatalf("unmarshal the retraction provenance: %v", err)
	}
	return provenance, true
}

// seedWithdrawingSnapshot writes a local registry-index snapshot into the workspace
// the server holds, recording the two published versions with the newer withdrawn.
//
// It addresses the snapshot through the same path helpers and the same record type
// the index reader uses, and takes the root and the cache directory off the
// resolution the server itself resolves through — so the fixture cannot disagree with
// production about where a snapshot lives or what one looks like. Everything it
// writes lands under the workspace root the fixture created, and nothing outside it.
//
// observedAt is the build time recorded beside the snapshot, and an empty one writes
// no timestamp sibling at all — which is the second of the two snapshot shapes, and
// the one that must not read as a snapshot built at the zero instant.
//
// The recorded withdrawal carries a verified signer, because only a
// signature-verified record screens a version: an unsigned one is passed over by the
// same rule production applies, so a snapshot without one would exercise an
// unscreened selection while reading as a screened one.
func seedWithdrawingSnapshot(t *testing.T, srv *Server, observedAt string) {
	t.Helper()

	rw := srv.resolvedWorkspace
	root, cacheDir := rw.Root(), rw.Config().ResolvedArtifactCacheDir()

	record := registryindex.Record{
		URI:      registryindex.URIForReference(wireRetractedRef),
		Name:     "tag-injector",
		Version:  wireRetractedNewest,
		Versions: []string{wireRetractedOlder, wireRetractedNewest},
		Deprecations: []registryindex.VersionDeprecation{{
			Version: wireRetractedNewest,
			Marker:  deprecation.Marker{Severity: deprecation.SeverityRetracted, Reason: wireRetractedReason},
			Signer:  &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"},
		}},
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal the snapshot record: %v", err)
	}
	recordPath := oci.IndexRepoPath(root, cacheDir, wireRetractedHost, wireRetractedRepo)
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o755); err != nil {
		t.Fatalf("make the snapshot directory: %v", err)
	}
	if err := os.WriteFile(recordPath, body, 0o600); err != nil {
		t.Fatalf("write the snapshot record: %v", err)
	}
	if observedAt == "" {
		return
	}
	stampPath := oci.IndexBasePath(root, cacheDir, wireRetractedHost) + ".updated"
	if err := os.WriteFile(stampPath, []byte(observedAt+"\n"), 0o600); err != nil {
		t.Fatalf("write the snapshot timestamp: %v", err)
	}
}

// TestThePluginAddAnswerNamesTheSnapshotItScreenedAgainst is the invocation-level
// proof of the other source: a withdrawal read from a local snapshot is reported as
// one, with the snapshot's own age when it has one.
//
// 🔴 The live registry seams are deliberately told that NOTHING is withdrawn, which
// makes this a control rather than a repeat of the test above. Had selection fallen
// through to the live path, it would have pinned the newer version and answered with
// no retraction at all — so the arms below cannot pass unless the snapshot is what was
// read. Asserting the source alone would not have shown that: a surface reporting the
// snapshot source unconditionally would satisfy it.
//
// The two arms differ in one thing, the timestamp sibling beside the snapshot, which
// is the distinction between a snapshot whose age is known and one whose age is not.
// A reader is owed the difference: an age is something to act on, and its absence must
// not arrive as an age of zero.
func TestThePluginAddAnswerNamesTheSnapshotItScreenedAgainst(t *testing.T) {
	// Not parallel: the plugin layer's registry seams are process-global.
	const observedAt = "2026-03-04T05:06:07Z"

	for _, tc := range []struct {
		name           string
		observedAt     string
		wantLastUpdate string
	}{
		{
			name:           "a snapshot recording when it was built",
			observedAt:     observedAt,
			wantLastUpdate: observedAt,
		},
		{
			name:           "a snapshot carrying no build time",
			observedAt:     "",
			wantLastUpdate: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFakeRegistryForAttachWithdrawing(t, []string{wireRetractedOlder, wireRetractedNewest}, nil)
			srv, _, _ := registryWriteFixture(t, `workspace:
  catalog_dirs:
    - catalog
default_registry: hub.aggrega.ai
`)
			seedWithdrawingSnapshot(t, srv, tc.observedAt)

			resp := callDaemonMethod(t, srv, "aggrega/pluginAdd", map[string]any{
				"type": "governance", "query": "tag-injector", "role": "label-transform",
			})
			if resp.Error != nil {
				t.Fatalf("expected a result; got error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			t.Logf("aggrega/pluginAdd answered:\n%s", resp.Result)

			var got pluginAddResult
			decodeDaemonResult(t, resp, &got)
			if !strings.HasSuffix(got.Identity, ":"+wireRetractedOlder) {
				t.Fatalf("pinned %q; want a %s attachment — the live seams withdraw nothing, so this pin says the snapshot was not what selection screened against",
					got.Identity, wireRetractedOlder)
			}
			if got.Retraction == nil {
				t.Fatal("the answer carries no retraction, so the snapshot's withdrawal record reached nobody")
			}
			if got.Retraction.Provenance == nil {
				t.Fatal("the answer carries no provenance; a caller cannot tell that this withdrawal came from a snapshot a refresh would settle")
			}
			if got.Retraction.Provenance.Source != wireProvenanceLocalIndex {
				t.Errorf("retraction.provenance.source = %q; want %q — the withdrawal was recorded in the local snapshot, not read during the request",
					got.Retraction.Provenance.Source, wireProvenanceLocalIndex)
			}
			if got.Retraction.Provenance.LastUpdated != tc.wantLastUpdate {
				t.Errorf("retraction.provenance.lastUpdated = %q; want %q",
					got.Retraction.Provenance.LastUpdated, tc.wantLastUpdate)
			}

			provenance, present := wireRetractionProvenanceKeys(t, resp.Result)
			if !present {
				t.Fatalf("the payload carries no provenance key: %s", resp.Result)
			}
			// The age is the one optional property this position declares, so
			// which arm the snapshot is decides whether the key is there at all,
			// and the rest of the set comes from the schema. Asserting the whole
			// set rather than probing the age alone is what additionally holds the
			// source key to the spelling the contract declares.
			var carries []string
			if tc.wantLastUpdate != "" {
				carries = []string{"lastUpdated"}
			}
			assertWireKeys(t, "the advisory's provenance", "#/$defs/PluginRetractionProvenance",
				provenance, carries, resp.Result)
		})
	}
}

// TestAnUnscreenedPluginAddAnswerNamesNoProvenance is the absence control the two
// tests above need, driven rather than reasoned.
//
// A failed withdrawal read forms no screen at all, so there is no evidence to name
// where it came from. Reporting a source here would be reporting one nothing
// consulted — and it would do so on exactly the answer whose point is that no claim
// is being made about the version that was pinned, which is the reading a caller
// would be most damaged by getting wrong.
//
// It asserts the key is missing from the bytes rather than that the decoded source is
// empty, because a provenance object with a blank source is what a projection that
// filled the shape unconditionally would send, and that is indistinguishable from the
// correct answer once decoded.
func TestAnUnscreenedPluginAddAnswerNamesNoProvenance(t *testing.T) {
	// Not parallel: the plugin layer's registry seams are process-global.
	const fault = "the registry refused the referrer listing"

	withFakeRegistryForAttachWithdrawing(t, []string{wireRetractedOlder, wireRetractedNewest},
		map[string]string{wireRetractedNewest: wireRetractedReason})
	// Set after the fixture, so the fixture's own restore puts the true lister back.
	pluginops.RepoWithdrawalLister = func(context.Context, workspaceconfig.Config, string, string, []string, io.Writer) ([]registryindex.VersionDeprecation, error) {
		return nil, errors.New(fault)
	}

	srv, _, _ := registryWriteFixture(t, `workspace:
  catalog_dirs:
    - catalog
default_registry: hub.aggrega.ai
`)
	resp := callDaemonMethod(t, srv, "aggrega/pluginAdd", map[string]any{
		"type": "governance", "query": "tag-injector", "role": "label-transform",
	})
	if resp.Error != nil {
		t.Fatalf("a failed withdrawal read degrades rather than halting; got error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	t.Logf("aggrega/pluginAdd answered:\n%s", resp.Result)

	var got pluginAddResult
	decodeDaemonResult(t, resp, &got)
	if got.Retraction == nil {
		t.Fatal("selection ran unscreened and the answer says nothing; a caller cannot tell a degraded answer from a screened one")
	}
	if got.Retraction.Unscreened != fault {
		t.Fatalf("retraction.unscreened = %q; want %q — without the degraded posture this test asserts nothing about it",
			got.Retraction.Unscreened, fault)
	}
	if got.Retraction.Provenance != nil {
		t.Errorf("an unscreened selection answered with provenance %#v; nothing was consulted, so there is no source to name",
			got.Retraction.Provenance)
	}
	if _, present := wireRetractionProvenanceKeys(t, resp.Result); present {
		t.Errorf("the payload carries a provenance key on a selection nothing screened: %s", resp.Result)
	}
}

// TestThePluginAddAnswerSaysANamedPinIsWithdrawn is the invocation-level proof that
// a named version a withdrawal covers reaches a caller over the request surface as
// a value, read from the payload BYTES, so "absent" is asserted rather than "false".
//
// The planted defects each row catches:
//   - "named withdrawn": the daemon dropping the operation's Withdrawn, or the key
//     sent under a spelling the shipped schema does not declare (the key is resolved
//     against the schema, which also asserts it is optional);
//   - "named ordinary": the key sent unconditionally, or sent as false — an absent
//     key and a false one read differently to a client matching on presence;
//   - "named, markers unreadable": a named pin's failed read answered with nothing,
//     which a caller cannot tell from a version nothing withdrew — the answer must
//     carry no withdrawn key and retraction.unscreened naming the fault.
func TestThePluginAddAnswerSaysANamedPinIsWithdrawn(t *testing.T) {
	// Not parallel: the plugin layer's registry seams are process-global.
	const fault = "the registry refused the referrer listing"
	for _, tc := range []struct {
		name           string
		version        string
		failMarkers    bool
		wantWithdrawn  bool
		wantUnscreened string
	}{
		{name: "named withdrawn", version: wireRetractedNewest, wantWithdrawn: true},
		{name: "named ordinary", version: wireRetractedOlder},
		{name: "named, markers unreadable", version: wireRetractedNewest, failMarkers: true, wantUnscreened: fault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFakeRegistryForAttachWithdrawing(t, []string{wireRetractedOlder, wireRetractedNewest},
				map[string]string{wireRetractedNewest: wireRetractedReason})
			if tc.failMarkers {
				// Set after the fixture, so the fixture's own restore puts the true lister back.
				pluginops.RepoWithdrawalLister = func(context.Context, workspaceconfig.Config, string, string, []string, io.Writer) ([]registryindex.VersionDeprecation, error) {
					return nil, errors.New(fault)
				}
			}
			srv, _, _ := registryWriteFixture(t, `workspace:
  catalog_dirs:
    - catalog
default_registry: hub.aggrega.ai
`)
			resp := callDaemonMethod(t, srv, "aggrega/pluginAdd", map[string]any{
				"type": "governance", "query": "tag-injector:" + tc.version, "role": "label-transform",
			})
			if resp.Error != nil {
				t.Fatalf("expected a result; got error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			t.Logf("aggrega/pluginAdd answered:\n%s", resp.Result)

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(resp.Result, &raw); err != nil {
				t.Fatalf("unmarshal the payload: %v", err)
			}
			var identity string
			if err := json.Unmarshal(raw["identity"], &identity); err != nil || !strings.HasSuffix(identity, ":"+tc.version) {
				t.Fatalf("pinned %q (%v); want the named %s — the fixture is wrong, not the projection", identity, err, tc.version)
			}
			key := wireOptionalProperty(t, "#/$defs/PluginAddResult", "withdrawn")
			value, present := raw[key]
			switch {
			case tc.wantWithdrawn && (!present || string(value) != "true"):
				t.Errorf("the payload carries %s = %s (present %v); want true: %s", key, value, present, resp.Result)
			case !tc.wantWithdrawn && present:
				t.Errorf("the payload carries %s = %s; want the key absent: %s", key, value, resp.Result)
			}

			var got pluginAddResult
			decodeDaemonResult(t, resp, &got)
			if tc.wantUnscreened == "" {
				if got.Retraction != nil {
					t.Errorf("a named pin answered with an advisory %#v; nothing was substituted and nothing failed", got.Retraction)
				}
				return
			}
			if got.Retraction == nil || got.Retraction.Unscreened != tc.wantUnscreened || got.Retraction.Reference != wireRetractedRef {
				t.Fatalf("retraction = %#v; want unscreened %q on %s", got.Retraction, tc.wantUnscreened, wireRetractedRef)
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
	if got.Provenance != nil {
		t.Errorf("a degraded advisory projected provenance %#v; an advisory resting on no screen has no source to name", got.Provenance)
	}
}

// TestTheProvenanceProjectionCarriesBothAxesUnchanged holds the provenance half of
// the projection to carrying rather than deciding, across every shape a screen
// produces.
//
// It is a table beside the invocation proofs above rather than instead of them,
// because what a request drives is one screen at a time: an arm reached only by a
// shape those requests do not produce would go unexercised there. The absent arm is
// asserted too, since a projection answering with an empty shape rather than nothing
// puts a blank source on an answer that has none.
//
// The expectations are the spellings a client matches on, written out rather than
// read back off the provenance the projection was handed — an expectation taken from
// the input agrees with the projection however it respells it.
func TestTheProvenanceProjectionCarriesBothAxesUnchanged(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	for _, tc := range []struct {
		name           string
		provenance     registryindex.RetractionProvenance
		wantAbsent     bool
		wantSource     string
		wantLastUpdate string
	}{
		{
			name:       "a selection nothing screened",
			provenance: registryindex.RetractionProvenance{},
			wantAbsent: true,
		},
		{
			name:       "facts read from the registry during the operation",
			provenance: registryindex.LiveProvenance(),
			wantSource: wireProvenanceRegistry,
			// No age, and that asymmetry is the subject: facts read during the operation
			// have no staleness to report, so an age on them would invite a reader to weigh
			// a currency question that does not arise.
			wantLastUpdate: "",
		},
		{
			name:           "a snapshot recording when it was built",
			provenance:     registryindex.IndexProvenance(observedAt, true),
			wantSource:     wireProvenanceLocalIndex,
			wantLastUpdate: "2026-03-04T05:06:07Z",
		},
		{
			name:       "a snapshot carrying no build time",
			provenance: registryindex.IndexProvenance(time.Time{}, false),
			wantSource: wireProvenanceLocalIndex,
			// Absent rather than the zero instant rendered: a snapshot whose age is not
			// recorded and one built at the epoch must not read alike.
			wantLastUpdate: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := pluginRetractionOf(&pluginops.RetractionAdvisory{
				Reference:  wireRetractedRef,
				Selected:   wireRetractedOlder,
				Provenance: tc.provenance,
			})
			if got == nil {
				t.Fatal("an advisory projected to nothing")
			}
			if tc.wantAbsent {
				if got.Provenance != nil {
					t.Fatalf("provenance projected to %#v; want nothing so the field is omitted", got.Provenance)
				}
				return
			}
			if got.Provenance == nil {
				t.Fatal("provenance projected to nothing, so a caller learns nothing about where the withdrawal was read")
			}
			if got.Provenance.Source != tc.wantSource {
				t.Errorf("provenance.source = %q; want %q", got.Provenance.Source, tc.wantSource)
			}
			if got.Provenance.LastUpdated != tc.wantLastUpdate {
				t.Errorf("provenance.lastUpdated = %q; want %q", got.Provenance.LastUpdated, tc.wantLastUpdate)
			}
		})
	}
}
