// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The registries every case here names. None is a loopback address and none
// resolves, so nothing here reaches a registry even by accident: every case that
// gets as far as a registry injects its own in-memory target.
const (
	// keyedHost is a registry whose entry names a held signing key.
	keyedHost = "keyed.example.test"
	// secondKeyedHost is a registry whose entry names a different held key.
	secondKeyedHost = "second.example.test"
	// plainHost is a registry nothing names a key for, so it signs keyless.
	plainHost = "plain.example.test"
	// thirdHost is a second registry nothing names a key for.
	thirdHost = "third.example.test"
)

// credentialRef names a published version on host, under a repository of its own
// so two references on one host stay two references.
func credentialRef(host, name string) string { return host + "/component/core/" + name + ":1.0.0" }

// credentialWorkspace names keyA for keyedHost and keyB for secondKeyedHost, and
// nothing for any other registry. extra is appended verbatim: a row that adds a
// registry entry writes it as a further list item, a row that adds a top-level
// block writes it from the first column.
func credentialWorkspace(keyA, keyB, extra string) string {
	return "workspace: {}\nregistries:\n" +
		"  - host: " + keyedHost + "\n    signing:\n      key_path: " + keyA + "\n" +
		"  - host: " + secondKeyedHost + "\n    signing:\n      key_path: " + keyB + "\n" +
		extra
}

// publishedVersion is a store holding one published version under tag 1.0.0 —
// the subject every reference of a case resolves to — and its digest.
func publishedVersion(t *testing.T) (*memory.Store, string) {
	t.Helper()
	store := memory.New()
	digest, err := oci.Push(context.Background(), store, "1.0.0", oci.MediaTypeComponent, []byte("# a published version\n"), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	return store, digest
}

// batchInputs is what a caller supplies to a batch beyond its references.
type batchInputs struct {
	keyPath string
	idToken string
	// acquire is the interactive identity rung, reached only where nothing
	// supplied an identity; a person is reported watching, so a batch that asked
	// for an identity at all reaches it.
	acquire func() (string, error)
}

// batchVerb is one verb that signs several references at once, driven end to end
// against an injected target. It returns how many references the verb reported
// done — signed, or marked — and its error.
type batchVerb struct {
	name string
	run  func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, target oras.Target, refs []string, in batchInputs) (int, error)
	// attaches is the artifact type the verb attaches to each reference's subject.
	attaches string
}

// theBatchVerbs is every verb that signs several references in one operation.
func theBatchVerbs() []batchVerb {
	return []batchVerb{
		{
			name:     "the signing batch",
			attaches: signing.BundleMediaType,
			run: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, target oras.Target, refs []string, in batchInputs) (int, error) {
				t.Helper()
				subjects := make(map[string]string, len(refs))
				for _, ref := range refs {
					subjects[ref] = "1.0.0"
				}
				res, err := Sign(context.Background(), SignConfig{
					References: refs, SubjectRefs: subjects, Target: target, Workspace: rw,
					KeyPath: in.keyPath, IDToken: in.idToken,
					EnvLookup: lookMap(nil), IsTTY: func() bool { return true }, TokenGetter: in.acquire,
					Out: io.Discard,
				})
				return len(res.Signed), err
			},
		},
		{
			name:     "the deprecation batch",
			attaches: deprecation.ArtifactType,
			run: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, target oras.Target, refs []string, in batchInputs) (int, error) {
				t.Helper()
				// Each entry's reason names its own reference. Every entry attaches to the
				// one subject the case published, and a marker is content-addressed, so
				// entries carrying identical content would be one referrer rather than one
				// per entry and the per-entry count below could not hold.
				var b strings.Builder
				for _, ref := range refs {
					b.WriteString("- reference: " + ref + "\n  severity: warning\n  reason: \"superseded, was " + ref + "\"\n")
				}
				path := filepath.Join(t.TempDir(), "batch.yaml")
				if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
					t.Fatalf("write the batch file: %v", err)
				}
				res, err := Deprecate(context.Background(), DeprecateConfig{
					From: path, Target: target, SubjectRef: "1.0.0", Workspace: rw,
					KeyPath: in.keyPath, IDToken: in.idToken,
					EnvLookup: lookMap(nil), IsTTY: func() bool { return true }, TokenGetter: in.acquire,
					Out: io.Discard, Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
				})
				return len(res.Marked), err
			},
		},
	}
}

// countingAcquirer is an interactive identity rung that counts how often it is
// asked and answers with what is not a usable token, so a batch that reached it
// fails later rather than signing.
func countingAcquirer(asked *int) func() (string, error) {
	return func() (string, error) {
		*asked++
		return "not-a-usable-token", nil
	}
}

// TestAMixedCredentialBatchIsRefusedBeforeAnythingIsSigned drives each batch verb
// over references whose registries need different credentials, in each order.
//
// Every row carries one fault — two credentials in one batch — and is held to the
// whole refusal: the condition is matched through its accessor, nothing is
// attached to the subject the references resolve to, nothing is reported done,
// and the identity ladder is never asked, which is what shows the refusal comes
// before the identity rather than after it. The message names every registry
// involved and the setting that decides, and never the path of a key.
//
// The two orders of the first pair are both rows because the defect this replaces
// took the whole batch's credential from its first reference: with a keyed
// registry first, a later keyless reference failed on an identity nobody had been
// asked for, and with a keyless registry first, the identity was asked for and a
// later reference was then signed with a key the caller never chose. A refusal
// decided from the first reference alone passes neither row.
func TestAMixedCredentialBatchIsRefusedBeforeAnythingIsSigned(t *testing.T) {
	t.Parallel()
	keyA, _ := keyPair(t)
	keyB, _ := keyPair(t)
	rw := resolveIn(t, credentialWorkspace(keyA, keyB, ""), nil)

	rows := []struct {
		name      string
		refs      []string
		wantHosts []string
	}{
		{
			name:      "a configured key first, then a registry configuring none",
			refs:      []string{credentialRef(keyedHost, "a"), credentialRef(plainHost, "b")},
			wantHosts: []string{keyedHost, plainHost},
		},
		{
			name:      "a registry configuring none first, then a configured key",
			refs:      []string{credentialRef(plainHost, "b"), credentialRef(keyedHost, "a")},
			wantHosts: []string{plainHost, keyedHost},
		},
		{
			name:      "two registries configuring different keys",
			refs:      []string{credentialRef(keyedHost, "a"), credentialRef(secondKeyedHost, "c")},
			wantHosts: []string{keyedHost, secondKeyedHost},
		},
		{
			name: "one keyless registry named twice around a configured key",
			refs: []string{
				credentialRef(plainHost, "b"), credentialRef(keyedHost, "a"), credentialRef(plainHost, "d"),
			},
			wantHosts: []string{plainHost, keyedHost},
		},
	}

	for _, verb := range theBatchVerbs() {
		for _, row := range rows {
			t.Run(verb.name+"/"+row.name, func(t *testing.T) {
				store, _ := publishedVersion(t)
				asked := 0
				done, err := verb.run(t, rw, store, row.refs, batchInputs{acquire: countingAcquirer(&asked)})

				if !errors.Is(err, signing.ErrMixedSigningCredentials()) {
					t.Fatalf("want the batch refused for needing different credentials, got %v", err)
				}
				if done != 0 {
					t.Errorf("a refused batch reported %d reference(s) done", done)
				}
				if got := countReferrers(t, store, "1.0.0", ""); got != 0 {
					t.Errorf("%d referrer(s) attached; a refused batch attaches nothing", got)
				}
				if asked != 0 {
					t.Errorf("the identity was asked for %d time(s); the refusal must come before it", asked)
				}
				msg := err.Error()
				for _, host := range row.wantHosts {
					if !strings.Contains(msg, host) {
						t.Errorf("the refusal does not name %s: %s", host, msg)
					}
				}
				if !strings.Contains(msg, "signing.key_path") {
					t.Errorf("the refusal does not name the setting that decides: %s", msg)
				}
				if !strings.Contains(msg, "one per credential") {
					t.Errorf("the refusal does not say to sign one run per credential: %s", msg)
				}
				for _, key := range []string{keyA, keyB} {
					if strings.Contains(msg, key) {
						t.Errorf("the refusal names the path of a key, which says where a key sits on this machine: %s", msg)
					}
				}
				if strings.Count(msg, plainHost) > 2 {
					t.Errorf("a registry named by two references is listed more than once per role: %s", msg)
				}
			})
		}
	}
}

// unreadableTarget answers every read with an error, so a batch that reached a
// registry at all fails on that error rather than on the refusal asserted.
type unreadableTarget struct {
	*memory.Store
}

var errRegistryRead = errors.New("the test registry was read")

func (unreadableTarget) Resolve(context.Context, string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, errRegistryRead
}

func (unreadableTarget) Exists(context.Context, ocispec.Descriptor) (bool, error) {
	return false, errRegistryRead
}

// TestAMixedBatchIsRefusedBeforeAnyRegistryIsRead holds the refusal ahead of the
// registry: a batch whose references would be read before the refusal fails on the
// read instead, which this target makes visible. It also settles how the refusal
// meets a reference already carrying a signature — that is learned by reading the
// registry, and no registry is read, so a mixed batch is refused whatever its
// references already carry.
func TestAMixedBatchIsRefusedBeforeAnyRegistryIsRead(t *testing.T) {
	t.Parallel()
	keyA, _ := keyPair(t)
	keyB, _ := keyPair(t)
	rw := resolveIn(t, credentialWorkspace(keyA, keyB, ""), nil)
	refs := []string{credentialRef(keyedHost, "a"), credentialRef(plainHost, "b")}

	for _, verb := range theBatchVerbs() {
		t.Run(verb.name, func(t *testing.T) {
			asked := 0
			_, err := verb.run(t, rw, unreadableTarget{Store: memory.New()}, refs, batchInputs{acquire: countingAcquirer(&asked)})
			if errors.Is(err, errRegistryRead) || !errors.Is(err, signing.ErrMixedSigningCredentials()) {
				t.Fatalf("want the refusal with no registry read, got %v", err)
			}
		})
	}
}

// TestABatchUnderOneCredentialSignsAsBefore is the positive control beside the
// refusal: a batch whose references all need the same credential is signed — every
// reference, with that credential — however many registries it spans.
//
// The signing batch's rows verify each signature against the public half of the
// key that should have made it, so a batch signing with the wrong key fails here
// rather than passing on the count alone.
func TestABatchUnderOneCredentialSignsAsBefore(t *testing.T) {
	t.Parallel()
	keyA, pubA := keyPair(t)
	keyB, _ := keyPair(t)
	keyG, pubG := keyPair(t)
	callerKey, callerPub := keyPair(t)

	rows := []struct {
		name    string
		ws      string
		refs    []string
		keyPath string
		wantPub string
	}{
		{
			name:    "one registry's configured key, two references on it",
			ws:      credentialWorkspace(keyA, keyB, ""),
			refs:    []string{credentialRef(keyedHost, "a"), credentialRef(keyedHost, "e")},
			wantPub: pubA,
		},
		{
			name:    "the workspace-wide key, across two registries naming none of their own",
			ws:      credentialWorkspace(keyA, keyB, "signing:\n  key_path: "+keyG+"\n"),
			refs:    []string{credentialRef(plainHost, "b"), credentialRef(thirdHost, "f")},
			wantPub: pubG,
		},
		{
			name: "the caller's own key, across registries configuring different keys and none",
			ws:   credentialWorkspace(keyA, keyB, ""),
			refs: []string{
				credentialRef(keyedHost, "a"), credentialRef(plainHost, "b"), credentialRef(secondKeyedHost, "c"),
			},
			keyPath: callerKey,
			wantPub: callerPub,
		},
	}

	for _, verb := range theBatchVerbs() {
		for _, row := range rows {
			t.Run(verb.name+"/"+row.name, func(t *testing.T) {
				store, digest := publishedVersion(t)
				asked := 0
				done, err := verb.run(t, resolveIn(t, row.ws, nil), store, row.refs,
					batchInputs{keyPath: row.keyPath, acquire: countingAcquirer(&asked)})
				if err != nil {
					t.Fatalf("a batch under one credential must sign: %v", err)
				}
				if done != len(row.refs) {
					t.Fatalf("reported %d reference(s) done, want %d", done, len(row.refs))
				}
				if asked != 0 {
					t.Errorf("a keyed batch asked for an identity %d time(s)", asked)
				}
				if got := countReferrers(t, store, "1.0.0", verb.attaches); got != len(row.refs) {
					t.Fatalf("%d attached, want one per reference (%d)", got, len(row.refs))
				}
				if verb.attaches != signing.BundleMediaType {
					return
				}
				refs, err := oci.ListReferrers(context.Background(), store, "1.0.0", signing.BundleMediaType)
				if err != nil {
					t.Fatalf("ListReferrers: %v", err)
				}
				for _, r := range refs {
					payload, ferr := oci.FetchReferrerPayload(context.Background(), store, r)
					if ferr != nil {
						t.Fatalf("FetchReferrerPayload: %v", ferr)
					}
					if verr := signing.VerifyKeyedBundle(payload, []byte(row.wantPub), digest); verr != nil {
						t.Errorf("a signature does not verify against the key that should have made it: %v", verr)
					}
				}
			})
		}
	}
}

// TestTheCredentialAsksForAKeylessIdentityOnceForEveryReference is the acquire-once
// property, read at the one home every verb reaches: a keyless operation over
// references on several registries asks for its identity a single time, and every
// reference's signer carries that one identity against the endpoints its own
// registry configures.
//
// It reads the home through each verb's own projection of its inputs, because the
// projection is the part of the path that differs per verb.
func TestTheCredentialAsksForAKeylessIdentityOnceForEveryReference(t *testing.T) {
	t.Parallel()
	const thirdFulcio = "https://fulcio.third.example.test"
	ws := "workspace: {}\nregistries:\n  - host: " + thirdHost +
		"\n    signing:\n      fulcio_url: " + thirdFulcio + "\n"
	refs := []string{credentialRef(plainHost, "b"), credentialRef(thirdHost, "f")}
	token := signingtest.SubjectfulIDToken(t)

	for _, row := range []struct {
		name    string
		request func(rw *workspaceconfig.ResolvedWorkspace, acquire func() (string, error)) SignerRequest
	}{
		{"the signing batch", func(rw *workspaceconfig.ResolvedWorkspace, acquire func() (string, error)) SignerRequest {
			return SignConfig{Workspace: rw, EnvLookup: lookMap(nil), IsTTY: func() bool { return true }, TokenGetter: acquire}.signerRequest()
		}},
		{"the deprecation batch", func(rw *workspaceconfig.ResolvedWorkspace, acquire func() (string, error)) SignerRequest {
			return DeprecateConfig{Workspace: rw, EnvLookup: lookMap(nil), IsTTY: func() bool { return true }, TokenGetter: acquire}.signerRequest()
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			asked := 0
			acquire := func() (string, error) { asked++; return token, nil }
			cred, err := resolveCredential(row.request(resolveIn(t, ws, nil), acquire), refs, nil)
			if err != nil {
				t.Fatalf("resolveCredential: %v", err)
			}
			wantFulcio := map[string]string{refs[0]: signing.DefaultFulcioURL, refs[1]: thirdFulcio}
			for _, ref := range refs {
				signer, serr := cred.signerFor(ref)
				if serr != nil {
					t.Fatalf("signerFor %s: %v", ref, serr)
				}
				keyless, ok := signer.(*signing.KeylessSigner)
				if !ok {
					t.Fatalf("%s: built a %T, want the keyless signer", ref, signer)
				}
				if keyless.FulcioURL != wantFulcio[ref] {
					t.Errorf("%s signs against %q, want its own registry's %q", ref, keyless.FulcioURL, wantFulcio[ref])
				}
			}
			if asked != 1 {
				t.Errorf("the identity was asked for %d time(s) across %d references, want exactly 1", asked, len(refs))
			}
		})
	}
}

// TestTheRefusalSaysWhereASignatureIsRequired holds the one sentence the refusal
// adds where a registry in the batch requires signatures: that the refusal holds
// back signing which would otherwise have gone ahead. Each row changes only where
// the requirement is stated; the first is the control, with no requirement and so
// no such sentence.
func TestTheRefusalSaysWhereASignatureIsRequired(t *testing.T) {
	t.Parallel()
	keyA, _ := keyPair(t)
	keyB, _ := keyPair(t)
	const clause = "holds back signing that would otherwise have gone ahead"

	for _, row := range []struct {
		name         string
		extra        string
		keylessHost  string
		wantRequired []string
	}{
		{name: "control: no registry requires signatures", keylessHost: plainHost},
		{
			name:         "the keyless registry's own entry requires them",
			extra:        "  - host: " + plainHost + "\n    signing:\n      required: true\n",
			keylessHost:  plainHost,
			wantRequired: []string{plainHost},
		},
		{
			name:         "the keyless registry is the hub, whose baseline requires them",
			keylessHost:  workspaceconfig.HubBaselineHost,
			wantRequired: []string{workspaceconfig.HubBaselineHost},
		},
		{
			name:         "the workspace-wide signing policy requires them everywhere",
			extra:        "signing:\n  required: true\n",
			keylessHost:  plainHost,
			wantRequired: []string{keyedHost, plainHost},
		},
		{
			name:        "a requirement scoped to named kinds is not read as covering a reference",
			extra:       "  - host: " + plainHost + "\n    signing:\n      required: true\n      kinds: [Plugin]\n",
			keylessHost: plainHost,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			rw := resolveIn(t, credentialWorkspace(keyA, keyB, row.extra), nil)
			refs := []string{credentialRef(keyedHost, "a"), credentialRef(row.keylessHost, "b")}
			_, err := resolveCredential(SignerRequest{Workspace: rw, EnvLookup: lookMap(nil)}, refs, nil)
			if !errors.Is(err, signing.ErrMixedSigningCredentials()) {
				t.Fatalf("want the mixed-credential refusal, got %v", err)
			}
			msg := err.Error()
			if (len(row.wantRequired) > 0) != strings.Contains(msg, clause) {
				t.Fatalf("the refusal carries the required-signature sentence = %v, want %v: %s",
					strings.Contains(msg, clause), len(row.wantRequired) > 0, msg)
			}
			for _, host := range row.wantRequired {
				if !strings.Contains(msg, host+" require") && !strings.Contains(msg, host+" and ") {
					t.Errorf("the required-signature sentence does not name %s: %s", host, msg)
				}
			}
		})
	}
}

// TestAWithheldConfiguredKeyLeavesNoSecondCredential reads the withholding through
// the one home: a caller that may not spend the keys the workspace names sees none
// on any registry, so a workspace configuring a key for one registry and none for
// another is one keyless credential to that caller rather than two. The control
// row is the same workspace and inputs for a caller that may spend the keys, which
// is refused — without it, the withheld row would pass against a home that never
// read the configuration.
func TestAWithheldConfiguredKeyLeavesNoSecondCredential(t *testing.T) {
	t.Parallel()
	keyA, _ := keyPair(t)
	keyB, _ := keyPair(t)
	rw := resolveIn(t, credentialWorkspace(keyA, keyB, ""), nil)
	refs := []string{credentialRef(keyedHost, "a"), credentialRef(plainHost, "b")}
	identity := signingtest.SubjectfulIDToken(t)

	t.Run("control: a caller that may spend the keys is refused", func(t *testing.T) {
		_, err := resolveCredential(SignerRequest{Workspace: rw, IDToken: identity, EnvLookup: lookMap(nil)}, refs, nil)
		if !errors.Is(err, signing.ErrMixedSigningCredentials()) {
			t.Fatalf("want the mixed-credential refusal, got %v", err)
		}
	})

	t.Run("a caller that may not spend them signs every reference keyless", func(t *testing.T) {
		cred, err := resolveCredential(SignerRequest{
			Workspace: rw, IDToken: identity, EnvLookup: lookMap(nil), ConfiguredKeyNotConsulted: true,
		}, refs, nil)
		if err != nil {
			t.Fatalf("resolveCredential: %v", err)
		}
		for _, ref := range refs {
			signer, serr := cred.signerFor(ref)
			if serr != nil {
				t.Fatalf("signerFor %s: %v", ref, serr)
			}
			if _, keyless := signer.(*signing.KeylessSigner); !keyless {
				t.Errorf("%s: built a %T from a key this caller may not spend", ref, signer)
			}
		}
	})
}

// TestASuppliedSignerIsTheWholeCredential pins what a caller handing in its own
// signer gets: that signer for every reference, with no configuration consulted
// and no identity asked for — so references a configuration would split across
// two credentials are not refused, because no credential is resolved from it.
func TestASuppliedSignerIsTheWholeCredential(t *testing.T) {
	t.Parallel()
	keyA, _ := keyPair(t)
	keyB, _ := keyPair(t)
	refs := []string{credentialRef(keyedHost, "a"), credentialRef(plainHost, "b")}
	supplied := &signingtest.FakeSigner{}

	cred, err := resolveCredential(SignerRequest{
		Workspace: resolveIn(t, credentialWorkspace(keyA, keyB, ""), nil), Signer: supplied,
		AcquireIdentityToken: func() (string, bool, error) {
			t.Error("a supplied signer asked for an identity")
			return "", false, nil
		},
	}, refs, nil)
	if err != nil {
		t.Fatalf("resolveCredential: %v", err)
	}
	for _, ref := range refs {
		signer, serr := cred.signerFor(ref)
		if serr != nil || signer != signing.Signer(supplied) {
			t.Errorf("%s: got %v (%v), want the supplied signer", ref, signer, serr)
		}
	}
}

// TestASignerIsHandedOutOnlyForAReferenceTheCredentialCovers holds the home to the
// references it was resolved over: the one-credential rule was checked against
// those and no others, so a signer for any other reference would sign outside it.
// The first row is the control.
func TestASignerIsHandedOutOnlyForAReferenceTheCredentialCovers(t *testing.T) {
	t.Parallel()
	keyA, _ := keyPair(t)
	keyB, _ := keyPair(t)
	rw := resolveIn(t, credentialWorkspace(keyA, keyB, ""), nil)
	resolvedFor := credentialRef(keyedHost, "a")
	cred, err := resolveCredential(SignerRequest{Workspace: rw, EnvLookup: lookMap(nil)}, []string{resolvedFor}, nil)
	if err != nil {
		t.Fatalf("resolveCredential: %v", err)
	}

	for _, row := range []struct {
		name    string
		ref     string
		wantErr bool
	}{
		{"control: the reference it was resolved for", resolvedFor, false},
		{"a reference it was not resolved for", credentialRef(plainHost, "b"), true},
	} {
		t.Run(row.name, func(t *testing.T) {
			signer, serr := cred.signerFor(row.ref)
			if row.wantErr {
				if serr == nil || !strings.Contains(serr.Error(), "not among the references") {
					t.Fatalf("got %T (%v), want a refusal naming a reference outside the credential", signer, serr)
				}
				return
			}
			if serr != nil {
				t.Fatalf("signerFor: %v", serr)
			}
			if _, keyed := signer.(*signing.KeyedSigner); !keyed {
				t.Errorf("built a %T, want the keyed signer the registry's key selects", signer)
			}
		})
	}
}
