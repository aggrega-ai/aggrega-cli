// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourceresolver_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
)

// fakeAdapter is a configurable test double satisfying the
// Adapter interface. The kind is parameterised at construction;
// Resolve records its argument so assertions can verify the
// dispatcher forwarded the SourceSpec faithfully.
type fakeAdapter struct {
	kind       string
	calledWith sourceresolver.SourceSpec
	result     *sourceresolver.ResolvedSource
	resultErr  error
}

func (f *fakeAdapter) Kind() string { return f.kind }

func (f *fakeAdapter) Resolve(_ context.Context, spec sourceresolver.SourceSpec) (*sourceresolver.ResolvedSource, error) {
	f.calledWith = spec
	return f.result, f.resultErr
}

// Compile-time assertion: fakeAdapter satisfies the Adapter
// interface. This is the same shape per-kind adapters in
// downstream sub-packages will use.
var _ sourceresolver.Adapter = (*fakeAdapter)(nil)

func TestDispatcherRegisterAndResolve(t *testing.T) {
	t.Parallel()
	d := sourceresolver.NewDispatcher()

	fakeA := &fakeAdapter{
		kind: "test-kind-a",
		result: &sourceresolver.ResolvedSource{
			Kind:          "test-kind-a",
			URL:           "oci://example.com/a",
			PinnedVersion: "1.0.0",
			Digest:        "sha256:aaaa",
		},
	}
	fakeB := &fakeAdapter{
		kind: "test-kind-b",
		result: &sourceresolver.ResolvedSource{
			Kind:          "test-kind-b",
			URL:           "https://example.com/b.tgz",
			PinnedVersion: "2.0.0",
			Digest:        "sha256:bbbb",
		},
	}

	if err := d.Register(fakeA); err != nil {
		t.Fatalf("Register(fakeA): %v", err)
	}
	if err := d.Register(fakeB); err != nil {
		t.Fatalf("Register(fakeB): %v", err)
	}

	specA := sourceresolver.SourceSpec{
		Kind:    "test-kind-a",
		URL:     "oci://example.com/a",
		Version: "1.0.0",
	}
	resA, err := d.Resolve(context.Background(), specA)
	if err != nil {
		t.Fatalf("Resolve(specA): %v", err)
	}
	if resA.URL != fakeA.result.URL {
		t.Errorf("dispatcher returned wrong ResolvedSource: got %+v; want %+v", resA, fakeA.result)
	}
	if fakeA.calledWith.Kind != "test-kind-a" {
		t.Errorf("fakeA called with wrong spec: %+v", fakeA.calledWith)
	}
	// fakeB MUST NOT have been invoked.
	if fakeB.calledWith.Kind != "" {
		t.Errorf("fakeB was wrongly invoked: %+v", fakeB.calledWith)
	}

	// Now route to fakeB; fakeA's prior call doesn't carry over.
	specB := sourceresolver.SourceSpec{Kind: "test-kind-b", URL: "https://example.com/b.tgz", Version: "2.0.0"}
	resB, err := d.Resolve(context.Background(), specB)
	if err != nil {
		t.Fatalf("Resolve(specB): %v", err)
	}
	if resB.URL != fakeB.result.URL {
		t.Errorf("fakeB returned wrong ResolvedSource: %+v", resB)
	}
}

// TestDispatcherResolveUnregisteredKindReturnsErrNoAdapterRegistered holds the
// dispatcher's own refusal: a kind no adapter is registered for is refused under
// the program-assembly condition, naming the kind, and not under the authored-kind
// rule — which is a check over the document and is raised by it alone. A
// registered kind beside it is the control.
func TestDispatcherResolveUnregisteredKindReturnsErrNoAdapterRegistered(t *testing.T) {
	t.Parallel()
	d := sourceresolver.NewDispatcher()
	registered := &fakeAdapter{kind: "registered-kind", result: &sourceresolver.ResolvedSource{URL: "u"}}
	if err := d.Register(registered); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := d.Resolve(context.Background(), sourceresolver.SourceSpec{Kind: "registered-kind"}); err != nil {
		t.Fatalf("control: a registered kind was refused: %v", err)
	}

	_, err := d.Resolve(context.Background(), sourceresolver.SourceSpec{Kind: "no-such-kind"})
	if err == nil {
		t.Fatal("expected ErrNoAdapterRegistered; got nil")
	}
	if !errors.Is(err, sourceresolver.ErrNoAdapterRegistered()) {
		t.Errorf("error should wrap ErrNoAdapterRegistered; got %v", err)
	}
	if errors.Is(err, sourcekind.ErrUnknownKind()) {
		t.Errorf("the dispatcher raised the authored-kind condition, which only its check raises: %v", err)
	}
	if code, _ := cerrors.ShortCodeOf(err); code == "" {
		t.Errorf("the refusal carries no short code: %v", err)
	}
	if !strings.Contains(err.Error(), `"no-such-kind"`) {
		t.Errorf("the refusal does not name the kind: %v", err)
	}
}

func TestDispatcherRegisterDuplicateKindReturnsError(t *testing.T) {
	t.Parallel()
	d := sourceresolver.NewDispatcher()
	first := &fakeAdapter{kind: "duplicate-kind"}
	second := &fakeAdapter{kind: "duplicate-kind"}

	if err := d.Register(first); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := d.Register(second); err == nil {
		t.Fatal("second Register: expected error; got nil")
	}
}

func TestDispatcherKindsReturnsLexicographicOrder(t *testing.T) {
	t.Parallel()
	d := sourceresolver.NewDispatcher()
	for _, k := range []string{"c-kind", "a-kind", "b-kind"} {
		if err := d.Register(&fakeAdapter{kind: k}); err != nil {
			t.Fatalf("Register(%s): %v", k, err)
		}
	}
	got := d.Kinds()
	want := []string{"a-kind", "b-kind", "c-kind"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Kinds(): got %v; want %v", got, want)
	}
}
