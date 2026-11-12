// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package editops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
)

// reportDelivery names the reporting delivery for the tests that drive it. It
// goes through the shared mapping rather than naming the constant, so a test
// selects the delivery the way a surface does.
func reportDelivery() authoredcontent.Delivery { return authoredcontent.DeliveryFor(true) }

// authoredAggregate is the fixture every format-preservation assertion is made
// against. It deliberately carries the things a full re-serialize destroys: a
// leading comment block, a hand-aligned trailing comment, blank lines inside a
// block, a literal block scalar whose own indentation is deeper than its key's,
// a single-quoted scalar beside an unquoted one, and a template expression that
// only survives as text.
const authoredAggregate = `# The storefront application.
#
# The ordering below is deliberate: the web tier reads first.
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: storefront        # the module's own name
  version: 1.4.2
spec:
  parameters:
    db_size:
      type: string
      default: '10Gi'

  components:

    web:
      type: core/api-server:1.0.0
      description: |
        The public web tier.
          This line is indented inside a literal block.
      values:
        replicas: 3
      uses:
        database:
          primary:
            target: db

    # The data tier is intentionally last.
    db:
      type: core/postgres:2.1.0
      values:
        size: '{{ .parameters.db_size }}'
`

// writeWorkspace lays the fixture down in a scratch workspace and returns the
// root and the document's workspace-relative path.
func writeWorkspace(t *testing.T, content string) (root, rel string) {
	t.Helper()
	root = t.TempDir()
	rel = "storefront.aggregate.yaml"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return root, rel
}

func readBack(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	return string(b)
}

// addedLines reports the lines present in after and absent from before, and the
// lines removed the other way. It is how every format-preservation assertion is
// made: an edit that preserves formatting adds lines and removes none.
func addedLines(before, after string) (added, removed []string) {
	b := strings.Split(before, "\n")
	a := strings.Split(after, "\n")
	count := map[string]int{}
	for _, l := range b {
		count[l]++
	}
	for _, l := range a {
		if count[l] > 0 {
			count[l]--
			continue
		}
		added = append(added, l)
	}
	rest := map[string]int{}
	for _, l := range a {
		rest[l]++
	}
	for _, l := range b {
		if rest[l] > 0 {
			rest[l]--
			continue
		}
		removed = append(removed, l)
	}
	return added, removed
}

// TestEveryEnumeratedKindReachesTheWholeEditPath pins that the enumeration a
// surface reads is the same set the request check admits and the composition
// handles.
//
// A surface that has to NAME the bound — a refusal telling a caller which edits
// it may ask for — reads Kinds rather than restating the set, so the enumeration
// becoming a superset of what the switches handle would advertise an edit that is
// then refused. Each member is driven through checkRequest, containerFor and
// entryValue, which are the three places a kind is switched on.
//
// 🔴 It sees one direction and not the other. A kind declared as a constant and
// omitted from Kinds is not caught here — the enumeration is what this reads, so
// a member missing from it is invisible to it — and Go offers no way to walk the
// constants of a string-based kind. What that direction costs is a surface
// advertising less than the layer applies, which is the safer of the two
// failures; the direction asserted here is the one that would advertise more.
func TestEveryEnumeratedKindReachesTheWholeEditPath(t *testing.T) {
	kinds := Kinds()
	if len(kinds) == 0 {
		t.Fatal("Kinds enumerates nothing, so every assertion below is vacuous")
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			opts := Options{
				Kind:   kind,
				Node:   []string{"spec", "components", "web"},
				Socket: "database",
				Name:   "probe",
				Value:  "core/api-server:1.0.0",
			}
			if err := checkRequest(opts); err != nil {
				t.Fatalf("checkRequest refuses an enumerated kind: %v", err)
			}
			container, err := containerFor(opts)
			if err != nil {
				t.Fatalf("containerFor refuses an enumerated kind: %v", err)
			}
			if len(container) == 0 {
				t.Error("containerFor returns no container for an enumerated kind")
			}
			if _, err := entryValue(opts); err != nil {
				t.Fatalf("entryValue refuses an enumerated kind: %v", err)
			}
		})
	}
}

// TestKindsReturnsACopy pins that a caller enumerating the bound cannot alter
// what a later reader sees.
func TestKindsReturnsACopy(t *testing.T) {
	first := Kinds()
	if len(first) == 0 {
		t.Fatal("Kinds enumerates nothing")
	}
	first[0] = Kind("clobbered")

	if again := Kinds(); again[0] == Kind("clobbered") {
		t.Fatal("mutating the returned slice reached the enumeration")
	}
}

// TestEachEditKindLandsInTheDocument drives all four edits against a real file
// on disk and asserts the document reads back carrying what was asked for.
//
// It is the invocation-level proof: the capability is called, the bytes are
// written, and the assertion is made against what is on disk rather than against
// a returned value.
func TestEachEditKindLandsInTheDocument(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  Options
		want  []string
		where []string
	}{
		{
			name: "add a component at the top level",
			opts: Options{
				Kind:  KindAddComponent,
				Node:  []string{"spec", "components"},
				Name:  "cache",
				Value: "core/redis:6.2.0",
			},
			want:  []string{"    cache:", "      type: core/redis:6.2.0"},
			where: []string{"spec", "components"},
		},
		{
			name: "attach a feature to a component that has none",
			opts: Options{
				Kind:  KindAttachFeature,
				Node:  []string{"spec", "components", "web"},
				Name:  "scrape",
				Value: "core/observability/prometheus-scrape:1.0.0",
			},
			want: []string{
				"      features:",
				"        scrape:",
				"          type: core/observability/prometheus-scrape:1.0.0",
			},
			where: []string{"spec", "components", "web", "features"},
		},
		{
			name: "wire an edge inside a socket the document already carries",
			opts: Options{
				Kind:   KindWireEdge,
				Node:   []string{"spec", "components", "web"},
				Socket: "database",
				Name:   "replica",
				Value:  "db",
			},
			want:  []string{"          replica:", "            target: db"},
			where: []string{"spec", "components", "web", "uses", "database"},
		},
		{
			name: "wire an edge inside a socket the document does not carry",
			opts: Options{
				Kind:   KindWireEdge,
				Node:   []string{"spec", "components", "db"},
				Socket: "backups",
				Name:   "nightly",
				Value:  "cache",
			},
			want: []string{"      uses:", "        backups:", "          nightly:", "            target: cache"},
			// The socket is authored by the consumer; nothing here judges
			// whether the producer declares it.
			where: []string{"spec", "components", "db", "uses", "backups"},
		},
		{
			name: "set a value on a component",
			opts: Options{
				Kind:  KindSetValue,
				Node:  []string{"spec", "components", "web"},
				Name:  "timeout_seconds",
				Value: 30,
			},
			want:  []string{"        timeout_seconds: 30"},
			where: []string{"spec", "components", "web", "values"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, rel := writeWorkspace(t, authoredAggregate)
			opts := tc.opts
			opts.File = rel

			res, err := Apply(t.Context(), root, opts)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if len(res.Written) != 1 || res.Written[0] != rel {
				t.Fatalf("Written = %v, want exactly %q", res.Written, rel)
			}
			if strings.Join(res.Container, ".") != strings.Join(tc.where, ".") {
				t.Errorf("Container = %v, want %v", res.Container, tc.where)
			}

			on := readBack(t, root, rel)
			for _, line := range tc.want {
				if !strings.Contains(on, line+"\n") {
					t.Errorf("the document does not carry the line %q\n--- document ---\n%s", line, on)
				}
			}
			// The reported document and the bytes on disk are one answer.
			if len(res.Documents) != 1 || res.Documents[0].Content != on {
				t.Error("the reported document and the file on disk disagree")
			}
		})
	}
}

// TestTheEditPreservesEveryByteItDidNotTouch is the format-preservation proof.
//
// It asserts the strong property rather than a sample of it: after the edit, no
// line of the original document has been removed or altered, and the only
// difference is the lines the entry added. A full re-serialize fails this on the
// comments, the blank lines, the quoting and the literal block at once.
func TestTheEditPreservesEveryByteItDidNotTouch(t *testing.T) {
	root, rel := writeWorkspace(t, authoredAggregate)

	if _, err := Apply(t.Context(), root, Options{
		File:  rel,
		Kind:  KindAddComponent,
		Node:  []string{"spec", "components"},
		Name:  "cache",
		Value: "core/redis:6.2.0",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after := readBack(t, root, rel)

	added, removed := addedLines(authoredAggregate, after)
	if len(removed) != 0 {
		t.Errorf("the edit removed or altered %d line(s), and it must alter none: %q", len(removed), removed)
	}
	if len(added) != 2 {
		t.Errorf("the edit added %d line(s), want exactly the 2 the entry needs: %q", len(added), added)
	}

	// Each of these is a distinct thing a re-serialize destroys, asserted
	// individually so a failure names which one moved.
	for _, survivor := range []string{
		"# The storefront application.",
		"# The ordering below is deliberate: the web tier reads first.",
		"  name: storefront        # the module's own name",
		"      default: '10Gi'",
		"          This line is indented inside a literal block.",
		"    # The data tier is intentionally last.",
		"        size: '{{ .parameters.db_size }}'",
	} {
		if !strings.Contains(after, survivor+"\n") {
			t.Errorf("the edit did not preserve %q", survivor)
		}
	}
	// A blank line inside the components block is content the author wrote.
	if !strings.Contains(after, "  components:\n\n    web:") {
		t.Error("the edit did not preserve the blank line the author wrote inside the components block")
	}
	// The trailing-newline shape is part of the document's bytes.
	if !strings.HasSuffix(after, "\n") || strings.HasSuffix(after, "\n\n") {
		t.Error("the edit changed the document's trailing-newline shape")
	}
}

// TestIdenticalEditsProduceIdenticalBytes is the determinism proof: capture,
// re-run against a fresh copy of the same input, compare.
//
// It runs the edit several times rather than twice, because a single repeat can
// agree by luck where the ordering it depends on happens to repeat.
func TestIdenticalEditsProduceIdenticalBytes(t *testing.T) {
	opts := Options{
		Kind:  KindAttachFeature,
		Node:  []string{"spec", "components", "web"},
		Name:  "scrape",
		Value: "core/observability/prometheus-scrape:1.0.0",
	}

	var first string
	for i := 0; i < 8; i++ {
		root, rel := writeWorkspace(t, authoredAggregate)
		o := opts
		o.File = rel
		if _, err := Apply(t.Context(), root, o); err != nil {
			t.Fatalf("Apply on run %d: %v", i, err)
		}
		got := readBack(t, root, rel)
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d produced different bytes from run 0", i)
		}
	}
	if first == authoredAggregate {
		t.Fatal("the document is unchanged, so the determinism comparison covers nothing")
	}
}

// TestRepeatingAnEditWritesNothing pins idempotence: the same edit applied twice
// leaves the file byte-for-byte as the first application left it, and the second
// application reports having written nothing.
//
// It matters for a caller that cannot tell whether its edit already landed —
// re-applying must not append a second copy and must not rewrite the file.
func TestRepeatingAnEditWritesNothing(t *testing.T) {
	root, rel := writeWorkspace(t, authoredAggregate)
	opts := Options{
		File:  rel,
		Kind:  KindAddComponent,
		Node:  []string{"spec", "components"},
		Name:  "cache",
		Value: "core/redis:6.2.0",
	}

	if _, err := Apply(t.Context(), root, opts); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	once := readBack(t, root, rel)

	res, err := Apply(t.Context(), root, opts)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(res.Written) != 0 {
		t.Errorf("the repeated edit reported writing %v, want nothing", res.Written)
	}
	if twice := readBack(t, root, rel); twice != once {
		t.Error("the repeated edit changed the document")
	}
	if strings.Count(once, "cache:") != 1 {
		t.Errorf("the entry appears %d times, want 1", strings.Count(once, "cache:"))
	}
}

// TestAnEntryPresentCarryingSomethingElseIsRefused pins the bound: this
// capability adds entries and never replaces one an author wrote.
func TestAnEntryPresentCarryingSomethingElseIsRefused(t *testing.T) {
	root, rel := writeWorkspace(t, authoredAggregate)
	before := readBack(t, root, rel)

	_, err := Apply(t.Context(), root, Options{
		File:  rel,
		Kind:  KindAddComponent,
		Node:  []string{"spec", "components"},
		Name:  "db",
		Value: "core/mysql:8.0.0",
	})
	if !errors.Is(err, errEntryPresentDiffers) {
		t.Fatalf("want the present-and-differing refusal; got %v", err)
	}
	if readBack(t, root, rel) != before {
		t.Error("a refused edit changed the document")
	}
}

// TestTheReportingDeliveryWritesNothing pins that the reporting delivery hands
// back the content the write would have produced and leaves the file alone.
//
// It is the delivery a caller holding its own document model uses, so the author
// sees the change in their own buffer, can amend it, and can undo it.
func TestTheReportingDeliveryWritesNothing(t *testing.T) {
	root, rel := writeWorkspace(t, authoredAggregate)

	res, err := Apply(t.Context(), root, Options{
		File:     rel,
		Kind:     KindAddComponent,
		Node:     []string{"spec", "components"},
		Name:     "cache",
		Value:    "core/redis:6.2.0",
		Delivery: reportDelivery(),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Written) != 0 {
		t.Errorf("the reporting delivery wrote %v, want nothing", res.Written)
	}
	if readBack(t, root, rel) != authoredAggregate {
		t.Error("the reporting delivery changed the document on disk")
	}
	if len(res.Documents) != 1 {
		t.Fatalf("Documents = %d, want 1", len(res.Documents))
	}
	if !strings.Contains(res.Documents[0].Content, "      type: core/redis:6.2.0\n") {
		t.Error("the reported content does not carry the entry the edit would have written")
	}
	if res.Documents[0].Path != rel {
		t.Errorf("the reported document names %q, want %q", res.Documents[0].Path, rel)
	}
}

// TestTheReportedContentEqualsWhatTheWriteProduces pins the two deliveries as
// one computation rather than two that agree today.
func TestTheReportedContentEqualsWhatTheWriteProduces(t *testing.T) {
	opts := Options{
		Kind:   KindWireEdge,
		Node:   []string{"spec", "components", "web"},
		Socket: "database",
		Name:   "replica",
		Value:  "db",
	}

	rootA, relA := writeWorkspace(t, authoredAggregate)
	oa := opts
	oa.File, oa.Delivery = relA, reportDelivery()
	reported, err := Apply(t.Context(), rootA, oa)
	if err != nil {
		t.Fatalf("reporting Apply: %v", err)
	}

	rootB, relB := writeWorkspace(t, authoredAggregate)
	ob := opts
	ob.File = relB
	if _, err := Apply(t.Context(), rootB, ob); err != nil {
		t.Fatalf("writing Apply: %v", err)
	}

	if reported.Documents[0].Content != readBack(t, rootB, relB) {
		t.Error("the reported content and the written bytes differ")
	}
}
