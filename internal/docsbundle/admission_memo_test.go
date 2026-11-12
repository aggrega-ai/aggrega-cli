// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package docsbundle

// How many verifications a sequence of reads of the documentation bundle costs.
//
// A read is held to the reading workspace's own signature check, and the check runs
// once per process for each copy and each check: what it admitted is remembered,
// and what it refused is not. These cases count the checks Admission actually ran,
// through a counter kept for tests, rather than timing anything.
//
// None of them is parallel, and that is what makes the counts exact: the counter
// is the process's, and a case that runs alone is the only one moving it. Every
// case publishes a bundle no other case or run shares, so nothing another case or
// an earlier run left remembered is ever the copy a case reads.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"sync"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// admissionRunCount reads the test-only counter of the read-time checks Admission
// ran rather than answered from what it remembered.
func admissionRunCount() int64 { return admissionRuns.Load() }

// runsDuring returns how many read-time checks ran while do did.
func runsDuring(do func()) int64 {
	before := admissionRunCount()
	do()
	return admissionRunCount() - before
}

// memoKey is a fresh signing key: the signer, and the public key a policy pins.
type memoKey struct {
	signer *signing.KeyedSigner
	pub    []byte
}

func freshKey(t *testing.T) memoKey {
	t.Helper()
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	return memoKey{signer: signer, pub: signer.PublicKeyPEM()}
}

// publishUnique publishes a documentation bundle whose page carries a value no
// other case or run shares, so the copy it becomes is one nothing in this process
// has checked before, and returns the store and the bundle's manifest digest.
func publishUnique(t *testing.T) (*memory.Store, string) {
	t.Helper()
	body := testBody + rand.Text() + "\n"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: testPage, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	store := memory.New()
	dg, err := oci.Push(context.Background(), store, testVersion, oci.MediaTypeDocs, buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	return store, dg
}

// signWith attaches a signature by k over the manifest dg to the store.
func signWith(t *testing.T, store *memory.Store, dg string, k memoKey) {
	t.Helper()
	sig, artifactType, err := k.signer.Sign(context.Background(), dg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oci.AttachReferrer(context.Background(), store, testVersion, artifactType, sig); err != nil {
		t.Fatal(err)
	}
}

// acquireUnder caches the store's bundle in root under cfg's own pull-time check,
// so the slot keeps what that check verified.
func acquireUnder(t *testing.T, store *memory.Store, root string, cfg workspaceconfig.Config) {
	t.Helper()
	verify := Verifier(cfg, testRef, testVersion, store, nil)
	if _, err := docscache.ResolveAndCache(context.Background(), store, testRef, testVersion, root, verify); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
}

// readUnder reads the page out of the copy in root as a workspace configured as
// cfg reads it.
func readUnder(root string, cfg workspaceconfig.Config, warn func(string)) error {
	_, _, err := docscache.ReadPage(root, testRef, testVersion, testPage, Admission(context.Background(), cfg, testRef, warn))
	return err
}

// pinning is a configuration that pins key for the test registry without
// requiring a signature: a different check from requiring(key) that admits the
// same signature.
func pinning(key []byte) workspaceconfig.Config {
	return workspaceconfig.Config{Registries: []workspaceconfig.RegistryConfig{
		{Host: testHost, PublicKey: string(key)},
	}}
}

// signedCopy publishes a unique bundle signed by k and caches it in a fresh root
// under requiring(k), and returns the root, the store and the bundle's manifest
// digest.
func signedCopy(t *testing.T, k memoKey) (string, *memory.Store, string) {
	t.Helper()
	store, dg := publishUnique(t)
	signWith(t, store, dg, k)
	root := t.TempDir()
	acquireUnder(t, store, root, requiring(k.pub))
	return root, store, dg
}

// TestTwoReadsOfOneCopyUnderOneCheckVerifyOnce is the saving itself: the second
// read of a copy under the check that admitted it runs no verification. The two
// configurations are built separately, so what is remembered is the check's
// content rather than one configuration value.
func TestTwoReadsOfOneCopyUnderOneCheckVerifyOnce(t *testing.T) {
	a := freshKey(t)
	root, _, _ := signedCopy(t, a)

	runs := runsDuring(func() {
		for i := range 2 {
			if err := readUnder(root, requiring(a.pub), nil); err != nil {
				t.Fatalf("read %d of a copy its check admits was refused: %v", i+1, err)
			}
		}
	})
	if runs != 1 {
		t.Errorf("two reads of one copy under one check ran %d verifications, want 1", runs)
	}
}

// TestADifferentCheckVerifiesTheCopyAgain pins that what one check admitted is
// never another check's verdict: a different check that admits the copy verifies
// it for itself, and a check that refuses it is refused — not served the copy the
// first check admitted. The first check's admission still stands afterwards.
func TestADifferentCheckVerifiesTheCopyAgain(t *testing.T) {
	a, other := freshKey(t), freshKey(t)
	root, _, _ := signedCopy(t, a)

	if runs := runsDuring(func() {
		if err := readUnder(root, requiring(a.pub), nil); err != nil {
			t.Fatalf("the check that fetched the copy refuses it: %v", err)
		}
	}); runs != 1 {
		t.Fatalf("the first read ran %d verifications, want 1", runs)
	}

	if runs := runsDuring(func() {
		if err := readUnder(root, pinning(a.pub), nil); err != nil {
			t.Errorf("a check pinning the signer's key refuses the copy: %v", err)
		}
	}); runs != 1 {
		t.Errorf("a different check admitting the copy ran %d verifications, want its own 1", runs)
	}

	if runs := runsDuring(func() {
		var refused *docscache.NotAdmittedError
		if err := readUnder(root, requiring(other.pub), nil); !errors.As(err, &refused) {
			t.Errorf("a check trusting another key was answered %v, want the copy refused: "+
				"it was served what a different check admitted", err)
		}
	}); runs != 1 {
		t.Errorf("a check refusing the copy ran %d verifications, want its own 1", runs)
	}

	if runs := runsDuring(func() {
		if err := readUnder(root, requiring(a.pub), nil); err != nil {
			t.Errorf("the first check refuses the copy it admitted: %v", err)
		}
	}); runs != 0 {
		t.Errorf("the first check, read again, ran %d verifications, want none", runs)
	}
}

// TestTheSameCheckReadOfflineVerifiesAgain pins that an admission made online is
// not an offline reader's verdict. A keyless check with no pinned trust root
// obtains the live one online and cannot offline, so an offline reader of such a
// check is answered by its own verification, never by an online reader's. The
// case uses a pinned key, which verifies either way, so that both reads admit and
// the count alone says whether the offline read was answered by its own check.
func TestTheSameCheckReadOfflineVerifiesAgain(t *testing.T) {
	a := freshKey(t)
	root, _, _ := signedCopy(t, a)
	offline := requiring(a.pub)
	offline.Offline = true

	if err := readUnder(root, requiring(a.pub), nil); err != nil {
		t.Fatalf("the online read refuses the copy: %v", err)
	}
	if runs := runsDuring(func() {
		if err := readUnder(root, offline, nil); err != nil {
			t.Errorf("the offline read of a pinned key refuses the copy: %v", err)
		}
	}); runs != 1 {
		t.Errorf("the offline read ran %d verifications after an online admission, want its own 1", runs)
	}
	if runs := runsDuring(func() {
		if err := readUnder(root, offline, nil); err != nil {
			t.Errorf("the offline read, again, refuses the copy: %v", err)
		}
	}); runs != 0 {
		t.Errorf("the offline read, again, ran %d verifications, want none", runs)
	}
}

// TestAReplacedCopyVerifiesAgain pins that a copy fetched since is not the copy
// that was admitted: the same bundle fetched again keeping one more signature, and
// a different bundle under the same reference and version, are each verified
// afresh by a check that admitted what the slot held before.
func TestAReplacedCopyVerifiesAgain(t *testing.T) {
	t.Run("the same bundle keeping one more signature", func(t *testing.T) {
		a, b := freshKey(t), freshKey(t)
		root, store, dg := signedCopy(t, a)
		if err := readUnder(root, requiring(a.pub), nil); err != nil {
			t.Fatalf("the copy's own check refuses it: %v", err)
		}

		// A workspace trusting b fetches again: the slot now keeps a's signature
		// and b's, over the same manifest.
		signWith(t, store, dg, b)
		acquireUnder(t, store, root, requiring(b.pub))

		if runs := runsDuring(func() {
			if err := readUnder(root, requiring(a.pub), nil); err != nil {
				t.Errorf("the copy keeping one more signature is refused by a's check: %v", err)
			}
		}); runs != 1 {
			t.Errorf("a's check ran %d verifications over the replaced copy, want 1", runs)
		}
	})

	t.Run("a different bundle under the same reference and version", func(t *testing.T) {
		// Both copies keep no signature and are read under a skipping check, so the
		// two keys differ in the manifest alone.
		root := t.TempDir()
		first, _ := publishUnique(t)
		acquireUnder(t, first, root, skipping())
		if err := readUnder(root, skipping(), nil); err != nil {
			t.Fatalf("a skipping check refuses the first copy: %v", err)
		}

		second, _ := publishUnique(t)
		acquireUnder(t, second, root, skipping())
		if runs := runsDuring(func() {
			if err := readUnder(root, skipping(), nil); err != nil {
				t.Errorf("a skipping check refuses the second copy: %v", err)
			}
		}); runs != 1 {
			t.Errorf("the check ran %d verifications over a different bundle, want 1", runs)
		}
	})
}

// TestARefusalIsNotRemembered pins the half that is deliberately absent. A fetch can
// write back the very copy a read refused — the same manifest and the same
// signatures — when the refusal came of a trust root that could not be obtained at
// that moment, so a remembered refusal would go on refusing what the fetch admitted.
// Each read under a refusing check runs it.
func TestARefusalIsNotRemembered(t *testing.T) {
	a, other := freshKey(t), freshKey(t)
	root, _, _ := signedCopy(t, a)

	runs := runsDuring(func() {
		for i := range 2 {
			var refused *docscache.NotAdmittedError
			if err := readUnder(root, requiring(other.pub), nil); !errors.As(err, &refused) {
				t.Fatalf("read %d under a check trusting another key was answered %v, want refused", i+1, err)
			}
		}
	})
	if runs != 2 {
		t.Errorf("two refused reads ran %d verifications, want 2: a refusal was remembered", runs)
	}
}

// TestARememberedAdmissionSaysWhatItsCheckSaid pins that a read answered from what
// was remembered reports what the check reported when it ran, so a skip, or an
// advisory registry's failure, is said on every read and not only the first.
func TestARememberedAdmissionSaysWhatItsCheckSaid(t *testing.T) {
	root := t.TempDir()
	store, _ := publishUnique(t)
	acquireUnder(t, store, root, skipping())

	var said [2][]string
	runs := runsDuring(func() {
		for i := range said {
			if err := readUnder(root, skipping(), func(notice string) { said[i] = append(said[i], notice) }); err != nil {
				t.Fatalf("read %d under a skipping check was refused: %v", i+1, err)
			}
		}
	})
	if runs != 1 {
		t.Errorf("two reads ran %d verifications, want 1", runs)
	}
	if len(said[0]) == 0 {
		t.Fatal("the check said nothing about the skip, so comparing the two reads would assert nothing")
	}
	if !slices.Equal(said[0], said[1]) {
		t.Errorf("the remembered read said %q, the read that ran the check said %q", said[1], said[0])
	}
}

// TestConcurrentFirstReadsShareOneVerification pins the memory's safety under the
// reads a server answers at once: every read is admitted, and reads that arrive
// while the check runs wait for its verdict rather than running it again. It is
// the race detector's case as much as this one's.
func TestConcurrentFirstReadsShareOneVerification(t *testing.T) {
	a := freshKey(t)
	root, _, _ := signedCopy(t, a)

	const readers = 16
	errs := make([]error, readers)
	runs := runsDuring(func() {
		var wg sync.WaitGroup
		for i := range readers {
			wg.Go(func() { errs[i] = readUnder(root, requiring(a.pub), nil) })
		}
		wg.Wait()
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent read %d was refused: %v", i, err)
		}
	}
	if runs != 1 {
		t.Errorf("%d concurrent reads of one copy under one check ran %d verifications, want 1", readers, runs)
	}
}
