// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
)

// These tests cover what keeps something an author wrote from disappearing from
// the assembled stack without a word: the Output-name derivation telling two
// authored entries apart, the refusal retained behind it, and the refusal of one
// mapping key written twice.
//
// Every negative row asserts the sentinel of the condition it pins rather than
// "an error", because the retained refusal and the merge's own refusal are
// separate gates over overlapping input. A row asserting only that something
// failed would keep passing after a different gate started rejecting it first, at
// which point it would have silently stopped covering its subject.

// TestEntriesThatUsedToMeetReachDistinctNames is the positive statement of the
// derivation's injectivity at the surface an author actually meets, one row per
// way two authored entries could once arrive at a single Output name.
//
// Every fixture here is legal input that the build refused: two components, or
// two entries, differing in nothing but a separator or a letter's case. Refusing
// them was not a corruption — the build halted rather than emit a stack missing
// one of the two — but it made an author rename something that was legal, and
// would have stayed legal under every other provider. So each row asserts the
// assembly now proceeds and that BOTH entries reach the stack under names of
// their own.
//
// The rows span both ways two entries could meet, because a change addressing
// one is not a change: the punctuation and case rows are the per-segment
// property, and the last row is the join — a component and entry pair against a
// differently-split pair, where neither authored string appears in the other.
func TestEntriesThatUsedToMeetReachDistinctNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// nodes is the whole assembled stack, so a row can put its two entries in
		// one component or in two.
		nodes []NodeFragment
		// wantOutputs is every Output name the assembled stack has to carry. Two
		// names, always: the point of each row is that the pair stayed a pair.
		wantOutputs []string
	}{
		{
			// The per-segment property: a separator is escaped rather than
			// dropped, so two provides keys differing only in punctuation stay
			// apart. This is the case a fragment can reach entirely on its own.
			name: "two provides keys differ only in punctuation",
			nodes: []NodeFragment{{NodeID: "orders", Files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nprovides:\n  db-host: first\n  db_host: second\n"),
			}}},
			wantOutputs: []string{"ordersQQdbQzhhost", "ordersQQdbQzuhost"},
		},
		{
			// A letter is carried in the case it was authored in, so two keys
			// differing only in case stay apart.
			name: "two provides keys differ only in a letter's case",
			nodes: []NodeFragment{{NodeID: "orders", Files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nprovides:\n  host: first\n  Host: second\n"),
			}}},
			wantOutputs: []string{"ordersQQHost", "ordersQQhost"},
		},
		{
			// The two sections reach the outputs space independently, so the pair
			// can straddle them. Bodies differ here.
			name: "an Outputs entry and a provides key differ only in punctuation, bodies differing",
			nodes: []NodeFragment{{NodeID: "orders", Files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nOutputs:\n  db_host:\n    Value: explicit\nprovides:\n  db-host: projected\n"),
			}}},
			wantOutputs: []string{"ordersQQdbQzhhost", "ordersQQdbQzuhost"},
		},
		{
			// The same straddle with bodies that agree. This is the row that
			// matters most: with equal bodies nothing downstream would have
			// refused it, so a derivation that folded the two would have left the
			// stack carrying a single Output where two were written.
			name: "an Outputs entry and a provides key differ only in punctuation, bodies agreeing",
			nodes: []NodeFragment{{NodeID: "orders", Files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nOutputs:\n  db_host:\n    Value: same\nprovides:\n  db-host: same\n"),
			}}},
			wantOutputs: []string{"ordersQQdbQzhhost", "ordersQQdbQzuhost"},
		},
		{
			// Two of one component's fragments, rather than one fragment's two
			// sections.
			name: "two fragments of one component carry keys differing only in punctuation",
			nodes: []NodeFragment{{NodeID: "orders", Files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nprovides:\n  db-host: first\n"),
				[]byte("Resources:\n  B:\n    Type: AWS::SQS::Queue\nprovides:\n  db.host: second\n"),
			}}},
			wantOutputs: []string{"ordersQQdbQzdhost", "ordersQQdbQzhhost"},
		},
		{
			// The join, which is a separate failure surface from the segment: the
			// component half and the entry half meet at a sequence no encoded
			// segment can carry, so a differently-split pair of the same letters
			// reaches its own name. Neither authored string appears in the other.
			name: "two components split one string differently between component and entry",
			nodes: []NodeFragment{
				{NodeID: "payments-db", Files: [][]byte{
					[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nprovides:\n  host: first\n"),
				}},
				{NodeID: "payments", Files: [][]byte{
					[]byte("Resources:\n  B:\n    Type: AWS::SQS::Queue\nprovides:\n  db-host: second\n"),
				}},
			},
			wantOutputs: []string{"paymentsQQdbQzhhost", "paymentsQzhdbQQhost"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := AssembleStack(tc.nodes)
			if err != nil {
				t.Fatalf("both entries are legal and differ, so the assembly must proceed: %v", err)
			}
			var top map[string]json.RawMessage
			if err := json.Unmarshal(out, &top); err != nil {
				t.Fatalf("assembled stack is not valid JSON: %v", err)
			}
			var outputs map[string]json.RawMessage
			if err := json.Unmarshal(top["Outputs"], &outputs); err != nil {
				t.Fatalf("Outputs is not an object: %v", err)
			}
			got := sortedCopy(keysOf(outputs))
			t.Logf("Outputs: %v", got)
			if !reflect.DeepEqual(got, tc.wantOutputs) {
				t.Fatalf("assembled Outputs %v, want %v — both entries must reach a name of their own", got, tc.wantOutputs)
			}
		})
	}
}

// TestTwoEntriesAreDeclaredApartAtDeclaration asserts the same pair stays a pair
// on the declaration path, which is read before the stack is assembled and is
// the only path a sourced template reaches.
//
// The declaration is what the host compares across contributors, so a
// declaration carrying one name where the author wrote two entries would let the
// host's own collision check see nothing to compare — which is why this is
// asserted here as well as at the assembly.
func TestTwoEntriesAreDeclaredApartAtDeclaration(t *testing.T) {
	t.Parallel()
	frag := []byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nprovides:\n  db-host: first\n  db_host: second\n")
	want := []string{"ordersQQdbQzhhost", "ordersQQdbQzuhost"}

	t.Run("fragments", func(t *testing.T) {
		t.Parallel()
		normalized, err := NormalizeFragment(frag)
		if err != nil {
			t.Fatalf("NormalizeFragment: %v", err)
		}
		_, outs, err := DeclaredNames("orders", [][]byte{normalized})
		if err != nil {
			t.Fatalf("DeclaredNames: %v", err)
		}
		if got := sortedCopy(outs); !reflect.DeepEqual(got, want) {
			t.Fatalf("declared outputs %v, want %v", got, want)
		}
	})

	t.Run("sourced", func(t *testing.T) {
		t.Parallel()
		normalized, err := NormalizeSourced([]SourcedFile{{Path: "template.yaml", Content: frag}}, false)
		if err != nil {
			t.Fatalf("NormalizeSourced: %v", err)
		}
		_, outs, err := DeclaredNamesSourced("orders", normalized, false)
		if err != nil {
			t.Fatalf("DeclaredNamesSourced: %v", err)
		}
		if got := sortedCopy(outs); !reflect.DeepEqual(got, want) {
			t.Fatalf("declared outputs %v, want %v", got, want)
		}
	})
}

// TestOutputNameLedgerAdmitsOneEntryRestated is the positive control for the
// table above, and it is what keeps the refusal from swallowing the merge's own.
// One entry name restated — across a fragment's two sections, or across two of a
// component's fragments — is one thing the author named. Whether the restatements
// agree about its body is the merge's question, so these rows must reach the merge
// rather than being refused here.
func TestOutputNameLedgerAdmitsOneEntryRestated(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files [][]byte
		// wantMergeRefusal is true where the restatements disagree about the body,
		// which the merge refuses; false where they agree and the stack assembles.
		wantMergeRefusal bool
	}{
		{
			name: "one entry name in both sections, bodies agreeing",
			files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: same\nprovides:\n  host: same\n"),
			},
		},
		{
			name: "one entry name restated across two fragments, bodies agreeing",
			files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nprovides:\n  host: same\n"),
				[]byte("Resources:\n  B:\n    Type: AWS::SQS::Queue\nprovides:\n  host: same\n"),
			},
		},
		{
			name: "one entry name in both sections, bodies disagreeing",
			files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: explicit\n    Description: the explicit one\nprovides:\n  host: projected\n"),
			},
			wantMergeRefusal: true,
		},
		{
			name: "one entry name restated across two fragments, bodies disagreeing",
			files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nprovides:\n  host: first\n"),
				[]byte("Resources:\n  B:\n    Type: AWS::SQS::Queue\nprovides:\n  host: second\n"),
			},
			wantMergeRefusal: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := AssembleStack([]NodeFragment{{NodeID: "orders", Files: tc.files}})
			if errors.Is(err, ErrOutputNameCollision()) {
				t.Fatalf("one entry name restated was read as two entries colliding: %v", err)
			}
			switch {
			case tc.wantMergeRefusal && err == nil:
				t.Fatal("two bodies under one name assembled; one of them is not in the stack")
			case !tc.wantMergeRefusal && err != nil:
				t.Fatalf("an ordinary restatement was refused: %v", err)
			}
		})
	}
}

// TestTheRetainedLedgerReportsEveryCollisionDeterministically exercises the
// refusal the derivation's injectivity has made unreachable, so that the guard
// kept for a future regression is a guard that has been shown to work.
//
// The ledger is seeded directly rather than driven through the derivation,
// because there is no longer any authored input that makes two different entries
// meet — which is the whole point of the change and would otherwise leave this
// code covered by nothing. What is asserted is the half a regression would
// depend on: every collision reported rather than the first, both authored ends
// named, and the text byte-identical across runs of identical input. The names
// are accumulated in a map, whose iteration order Go randomises per run, so an
// unsorted report would differ between runs of the same build.
func TestTheRetainedLedgerReportsEveryCollisionDeterministically(t *testing.T) {
	t.Parallel()
	seeded := func() *outputNameLedger {
		return &outputNameLedger{sources: map[string][]authoredOutputEntry{
			"ordersQQdbQzhhost": {
				{component: "orders", section: sectionProvides, entry: "db-host"},
				{component: "orders", section: sectionExplicitOutputs, entry: "db_host"},
			},
			"ordersQQqueueQzhurl": {
				{component: "orders", section: sectionProvides, entry: "queue-url"},
				{component: "orders", section: sectionProvides, entry: "queue_url"},
			},
		}}
	}

	first := seeded().err()
	if first == nil {
		t.Fatal("the ledger reported nothing for two names each reached by two entries")
	}
	if !errors.Is(first, ErrOutputNameCollision()) {
		t.Fatalf("report is not the Output-name collision: %v", first)
	}
	t.Logf("halt: %s", first)
	for i := range 12 {
		again := seeded().err()
		if again == nil || again.Error() != first.Error() {
			t.Fatalf("run %d produced a different diagnostic:\nfirst: %v\nagain: %v", i, first, again)
		}
	}
	// Every collision is reported rather than the first one found, and each
	// carries both authored ends so an author would have something to search for.
	for _, want := range []string{
		`"ordersQQdbQzhhost"`, `"ordersQQqueueQzhurl"`,
		`"db-host"`, `"db_host"`, `"queue-url"`, `"queue_url"`,
		"provides", "Outputs", `"orders"`,
	} {
		if !strings.Contains(first.Error(), want) {
			t.Errorf("the report does not carry %s: %v", want, first)
		}
	}

	// The positive control: a ledger whose names were each reached once reports
	// nothing, so the assertions above are not passing on a report that fires
	// whatever it holds.
	clean := &outputNameLedger{}
	for _, e := range []authoredOutputEntry{
		{component: "orders", section: sectionProvides, entry: "db-host"},
		{component: "orders", section: sectionProvides, entry: "db_host"},
		{component: "payments-db", section: sectionProvides, entry: "host"},
		{component: "payments", section: sectionProvides, entry: "db-host"},
	} {
		clean.mint(e)
	}
	if err := clean.err(); err != nil {
		t.Fatalf("four entries the derivation tells apart were reported as colliding: %v", err)
	}
}

// TestDuplicateMappingKeyRefused covers the backstop no derivation produces: a
// key written twice in one mapping. The parsed tree carries both bodies as a flat
// run of pairs, and the value tree holds one per key, so the second body would
// displace the first — and where the mapping is a Resources block, a resource that
// was written would simply not be in the stack.
func TestDuplicateMappingKeyRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// fragment is the authored bytes. Each fixture carries exactly one
		// duplicated key so the row pins that fault and no other.
		fragment string
		wantKey  string
		// wantLines are the two lines the key was written at, as the halt renders
		// them.
		wantLines []string
	}{
		{
			name:      "one resource logical id written twice",
			fragment:  "Resources:\n  Bucket:\n    Type: AWS::S3::Bucket\n  Bucket:\n    Type: AWS::SQS::Queue\n",
			wantKey:   "Bucket",
			wantLines: []string{"line 2", "line 4"},
		},
		{
			name:      "one Outputs entry written twice",
			fragment:  "Resources:\n  A:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: first\n  host:\n    Value: second\n",
			wantKey:   "host",
			wantLines: []string{"line 5", "line 7"},
		},
		{
			name:      "one provides key written twice",
			fragment:  "Resources:\n  A:\n    Type: AWS::S3::Bucket\nprovides:\n  host: first\n  host: second\n",
			wantKey:   "host",
			wantLines: []string{"line 5", "line 6"},
		},
		{
			name:      "one property written twice inside a resource",
			fragment:  "Resources:\n  Bucket:\n    Type: AWS::S3::Bucket\n    Properties:\n      BucketName: first\n      BucketName: second\n",
			wantKey:   "BucketName",
			wantLines: []string{"line 5", "line 6"},
		},
		{
			name:      "one top-level section written twice",
			fragment:  "Resources:\n  A:\n    Type: AWS::S3::Bucket\nResources:\n  B:\n    Type: AWS::SQS::Queue\n",
			wantKey:   "Resources",
			wantLines: []string{"line 1", "line 4"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// The refusal belongs to the conversion, so it fires wherever a
			// fragment is read: at normalization, at assembly, and at declaration.
			for _, probe := range []struct {
				where string
				run   func() error
			}{
				{"NormalizeFragment", func() error {
					_, err := NormalizeFragment([]byte(tc.fragment))
					return err
				}},
				{"AssembleStack", func() error {
					_, err := AssembleStack([]NodeFragment{{NodeID: "orders", Files: [][]byte{[]byte(tc.fragment)}}})
					return err
				}},
				{"DeclaredNames", func() error {
					_, _, err := DeclaredNames("orders", [][]byte{[]byte(tc.fragment)})
					return err
				}},
			} {
				err := probe.run()
				if err == nil {
					t.Fatalf("%s accepted a fragment writing %q twice; one of the two bodies is gone", probe.where, tc.wantKey)
				}
				if !errors.Is(err, ErrDuplicateMappingKey()) {
					t.Fatalf("%s halt is not the duplicate-key refusal: %v", probe.where, err)
				}
				msg := err.Error()
				if probe.where == "NormalizeFragment" {
					t.Logf("halt: %s", msg)
				}
				if !strings.Contains(msg, `"`+tc.wantKey+`"`) {
					t.Errorf("%s halt does not name the repeated key: %s", probe.where, msg)
				}
				for _, line := range tc.wantLines {
					if !strings.Contains(msg, line) {
						t.Errorf("%s halt does not report %s, so the author cannot find both: %s", probe.where, line, msg)
					}
				}
			}
		})
	}
}

// TestDuplicateMappingKeyAdmitsOneKeyPerMapping is the positive control: the same
// key name at two different places in the tree is ordinary CloudFormation and must
// pass. Only a repeat within one mapping loses a body.
func TestDuplicateMappingKeyAdmitsOneKeyPerMapping(t *testing.T) {
	t.Parallel()
	// Type appears once per resource, and Value once per output, so each of these
	// mappings is well formed however often the name recurs in the document.
	fragment := []byte("Resources:\n" +
		"  Bucket:\n    Type: AWS::S3::Bucket\n    Properties:\n      Tags:\n        - Key: a\n          Value: 1\n        - Key: b\n          Value: 2\n" +
		"  Queue:\n    Type: AWS::SQS::Queue\n" +
		"Outputs:\n  first:\n    Value: a\n  second:\n    Value: b\n")
	if _, err := NormalizeFragment(fragment); err != nil {
		t.Fatalf("a fragment repeating a key name across separate mappings was refused: %v", err)
	}
	out, err := AssembleStack([]NodeFragment{{NodeID: "orders", Files: [][]byte{fragment}}})
	if err != nil {
		t.Fatalf("AssembleStack: %v", err)
	}
	for _, want := range []string{`"Bucket"`, `"Queue"`, `"ordersQQfirst"`, `"ordersQQsecond"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("assembled stack is missing %s:\n%s", want, out)
		}
	}
}

// TestSourcedDuplicateMappingKeyRefused is the sourced-template half of the
// guarantee TestDuplicateMappingKeyRefused pins for a rendered fragment, and it is
// a separate test because the two input paths reach their refusal from different
// places. A rendered fragment goes through this package's own conversion, which
// refuses the repeat itself ([ErrDuplicateMappingKey]). A sourced template never
// reaches that conversion: NormalizeSourced, NormalizeSourcedTemplate and
// ValidateSourced hand the authored bytes to the intrinsics processor, and the
// processor refuses the document before this package inspects a single key. The
// property an author experiences is the same on both paths — the build halts, and
// the halt names the repeated key and both lines it was written at — which is what
// this test pins.
//
// Two things about the assertions are deliberate, against the convention stated at
// the head of this file.
//
// It asserts the halt's text rather than a sentinel, and specifically does NOT
// match [ErrDuplicateMappingKey]. Matching it would assert that this package holds
// a guarantee it does not hold on this path; the assertion would pass for the wrong
// reason, and it would keep passing if the processor's own check were ever the thing
// that went away.
//
// And it is agnostic about which component delivers the refusal, because the
// guarantee is the refusal rather than its author. The refusal presently arrives
// from a dependency, so it is not covered by any test of the conversion this package
// owns: a dependency upgrade that dropped the duplicate-key check would let the
// template through with one of its two bodies silently gone, and normalizing to
// long-form JSON collapses the repeat, so nothing downstream could see it either.
// This test is what reports that.
//
// It runs serially, unlike every sibling in this file, and the reason is measured
// rather than stylistic: the intrinsics processor keeps its tag resolvers in a
// package-level map and rewrites that map on each call, so two goroutines inside it
// abort the whole test binary with a concurrent map access — a runtime fatal error
// no recover can catch and no assertion can attribute. This test enters it several
// times per row, so marking it parallel is what turns it into an unattributable
// flake. That is a hazard of driving the processor from the host test binary, where
// the goroutines share one address space; the only production consumer is the
// single-threaded Wasm guest, whose instances share no Go map. Do not add
// t.Parallel here or to a sibling that reads a sourced template.
func TestSourcedDuplicateMappingKeyRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		// template is the authored bytes. Each fixture carries exactly one
		// duplicated key so the row pins that fault and no other.
		template string
		wantKey  string
		// wantLines are the two lines the key was written at. Both must appear,
		// whichever component refuses, or the author cannot find the second body.
		wantLines []string
	}{
		{
			name:      "one resource logical id written twice",
			template:  "Resources:\n  Bucket:\n    Type: AWS::S3::Bucket\n  Bucket:\n    Type: AWS::SQS::Queue\n",
			wantKey:   "Bucket",
			wantLines: []string{"line 2", "line 4"},
		},
		{
			name:      "one Outputs entry written twice",
			template:  "Resources:\n  A:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: first\n  host:\n    Value: second\n",
			wantKey:   "host",
			wantLines: []string{"line 5", "line 7"},
		},
		{
			name:      "one property written twice inside a resource",
			template:  "Resources:\n  Bucket:\n    Type: AWS::S3::Bucket\n    Properties:\n      BucketName: first\n      BucketName: second\n",
			wantKey:   "BucketName",
			wantLines: []string{"line 5", "line 6"},
		},
		{
			name:      "one top-level section written twice",
			template:  "Resources:\n  A:\n    Type: AWS::S3::Bucket\nResources:\n  B:\n    Type: AWS::SQS::Queue\n",
			wantKey:   "Resources",
			wantLines: []string{"line 1", "line 4"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Every exported entry point that reads a sourced template's authored
			// bytes. Two of them are what the guest reaches: render_node normalizes
			// the packed source, and validate_node_input parses the same bytes ahead
			// of render. A row has to hold at each, because a refusal present at one
			// and absent at another is a path that renders a template the other
			// rejected. NormalizeSourcedTemplate has no caller outside these tests,
			// which is a reason to hold it to the same property rather than a reason
			// to skip it. DeclaredNamesSourced is not among them: render_node hands
			// it what normalization returned, so on this path it never sees authored
			// bytes at all.
			assertRefusal := func(where string, err error) {
				t.Helper()
				if err == nil {
					t.Fatalf("%s accepted a template writing %q twice; one of the two bodies is gone", where, tc.wantKey)
				}
				msg := err.Error()
				if !strings.Contains(msg, `"`+tc.wantKey+`"`) {
					t.Errorf("%s halt does not name the repeated key: %s", where, msg)
				}
				for _, line := range tc.wantLines {
					if !strings.Contains(msg, line) {
						t.Errorf("%s halt does not report %s, so the author cannot find both: %s", where, line, msg)
					}
				}
			}

			_, err := NormalizeSourcedTemplate([]byte(tc.template))
			assertRefusal("NormalizeSourcedTemplate", err)

			_, err = NormalizeSourced([]SourcedFile{{Path: "template.yaml", Content: []byte(tc.template)}}, false)
			assertRefusal("NormalizeSourced", err)

			// A directory source, where the refusal has to name the file it came
			// from as well: the non-template file beside it is vendored untouched
			// and must not suppress the finding.
			_, err = NormalizeSourced([]SourcedFile{
				{Path: "README.md", Content: []byte("notes\n")},
				{Path: "template.yaml", Content: []byte(tc.template)},
			}, true)
			assertRefusal("NormalizeSourced (directory source)", err)
			if err != nil && !strings.Contains(err.Error(), "template.yaml") {
				t.Errorf("NormalizeSourced (directory source) halt does not name the file: %s", err)
			}

			// validate_node_input reports through diagnostics rather than an error,
			// so the same property is asserted against the halt-severity diagnostic
			// the host gates on.
			diags := ValidateSourced([]SourcedFile{{Path: "template.yaml", Content: []byte(tc.template)}}, false)
			if len(diags) != 1 {
				t.Fatalf("ValidateSourced produced %d diagnostics for a template writing %q twice; want exactly 1", len(diags), tc.wantKey)
			}
			if diags[0].Severity != abi.SeverityHalt {
				t.Errorf("ValidateSourced diagnostic severity is %v, so the build would not halt", diags[0].Severity)
			}
			if diags[0].Code != templateParseCode {
				t.Errorf("ValidateSourced diagnostic code is %q; a consumer matching %q would miss it", diags[0].Code, templateParseCode)
			}
			assertRefusal("ValidateSourced", errors.New(diags[0].Message))
		})
	}
}

// TestSourcedDuplicateMappingKeyAdmitsOneKeyPerMapping is the positive control for
// the test above: the same key name at two different places in the tree is ordinary
// CloudFormation and must pass on the sourced path too. Without it, a dependency
// that started refusing every template would keep the negative rows green.
//
// Serial for the same measured reason as the test above.
func TestSourcedDuplicateMappingKeyAdmitsOneKeyPerMapping(t *testing.T) {
	// Type appears once per resource and Value once per output, so this mapping is
	// well formed however often either name recurs in the document.
	template := []byte("Resources:\n" +
		"  Bucket:\n    Type: AWS::S3::Bucket\n    Properties:\n      Tags:\n        - Key: a\n          Value: 1\n        - Key: b\n          Value: 2\n" +
		"  Queue:\n    Type: AWS::SQS::Queue\n" +
		"Outputs:\n  first:\n    Value: a\n  second:\n    Value: b\n")
	if _, err := NormalizeSourcedTemplate(template); err != nil {
		t.Errorf("a template repeating a key name across separate mappings was refused: %v", err)
	}
	if _, err := NormalizeSourced([]SourcedFile{{Path: "template.yaml", Content: template}}, false); err != nil {
		t.Errorf("NormalizeSourced refused a well-formed template: %v", err)
	}
	if diags := ValidateSourced([]SourcedFile{{Path: "template.yaml", Content: template}}, false); len(diags) != 0 {
		t.Errorf("ValidateSourced reported %d diagnostics for a well-formed template: %v", len(diags), diags)
	}
}

// TestRefusalsReturnErrorsRatherThanPanicking asserts the standing property for
// hostile input: a malformed fragment comes back as an error the caller can wrap
// and report, never as a panic. testing.T does not fail on a recovered panic and a
// panic emits no diagnostic, so the assertion is made explicitly.
func TestRefusalsReturnErrorsRatherThanPanicking(t *testing.T) {
	t.Parallel()
	for _, fragment := range []string{
		"Resources:\n  A:\n    Type: x\n  A:\n    Type: y\n",
		"Resources:\n  A:\n    Type: x\nprovides:\n  a-b: 1\n  a_b: 2\n",
		"Resources:\n  A:\n    Type: x\nOutputs:\n  a b:\n    Value: 1\n  a-b:\n    Value: 2\n",
		"Resources: {}\nprovides:\n  '': 1\n  '-': 2\n",
		"Resources:\n  A:\n    Type: x\nprovides:\n  '': 1\n",
	} {
		t.Run(fragment, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked on hostile input rather than returning an error: %v", r)
				}
			}()
			_, _ = NormalizeFragment([]byte(fragment))
			_, _ = AssembleStack([]NodeFragment{{NodeID: "orders", Files: [][]byte{[]byte(fragment)}}})
			_, _, _ = DeclaredNames("orders", [][]byte{[]byte(fragment)})
		})
	}
}
