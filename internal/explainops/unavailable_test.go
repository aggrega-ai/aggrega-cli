// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package explainops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestTheWalkThroughIsSaidToBeRefusedRatherThanAbsent drives the read-only page
// read an explanation takes its walk-through from, over a machine holding no copy
// of the bundle, over one holding a copy the reader's check admits, and over one
// holding a copy the reader's check refuses. The first is told the bundle is not
// cached; the second is served both pages; the third is told the copy here is not
// admitted — not that it is missing, which would send the reader to fetch a bundle
// the machine already holds rather than to the trust setting that refused it.
func TestTheWalkThroughIsSaidToBeRefusedRatherThanAbsent(t *testing.T) {
	t.Parallel()
	const ref = "reg.example.invalid/docs/aggrega-docs"
	const version = "1.0.0"

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, p := range []string{haltref.ReferencePagePath, haltref.PagePath} {
		body := "# " + p + "\n"
		if err := tw.WriteHeader(&tar.Header{Name: p, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	store := memory.New()
	if _, err := oci.Push(context.Background(), store, version, oci.MediaTypeDocs, buf.Bytes(), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}

	errUntrusted := errors.New("the reader trusts no signer of this copy")
	admit := func(string, [][]byte) error { return nil }
	refuse := func(string, [][]byte) error { return errUntrusted }

	empty := t.TempDir()
	if _, err := ReadPages(empty, ref, version, admit); err == nil || !strings.Contains(err.Error(), "is not cached on this machine") {
		t.Errorf("a machine with no copy was answered %v, want the bundle reported as not cached", err)
	}

	held := t.TempDir()
	if _, err := docscache.ResolveAndCache(context.Background(), store, ref, version, held, nil); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	pages, err := ReadPages(held, ref, version, admit)
	if err != nil || len(pages.Reference) == 0 || len(pages.Halts) == 0 {
		t.Fatalf("a copy the reader admits was answered (%d, %d bytes, %v), want both pages", len(pages.Reference), len(pages.Halts), err)
	}

	_, err = ReadPages(held, ref, version, refuse)
	if err == nil {
		t.Fatal("a copy the reader's check refuses was served")
	}
	if !strings.Contains(err.Error(), "this workspace's signature check does not admit that copy") {
		t.Errorf("the reason does not say the copy here was refused:\n%v", err)
	}
	if strings.Contains(err.Error(), "is not cached on this machine") {
		t.Errorf("a refused copy is said to be absent:\n%v", err)
	}
	if !strings.Contains(err.Error(), errUntrusted.Error()) {
		t.Errorf("the reason does not carry the check's own refusal:\n%v", err)
	}
}
