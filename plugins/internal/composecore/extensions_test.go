// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// extensionFragment writes extension members at the top level and on a service,
// each level holding two in an order that is not alphabetical, around and
// between the keys the types model, so the assembled order can only come from
// the order written.
const extensionFragment = `x-zeta:
  b: 2
  a: 1
services:
  web:
    x-second: two
    image: app:1
    x-first:
      - one
x-alpha: kept
`

// TestDecodeFragment_KeepsExtensionMembersInWrittenOrder pins the decode: every
// extension member is held, at its own level, in the order the fragment wrote
// it, and none of them is refused.
func TestDecodeFragment_KeepsExtensionMembersInWrittenOrder(t *testing.T) {
	t.Parallel()

	frag, err := decodeFragment([]byte(extensionFragment), "web", `fragment "f.yaml"`)
	if err != nil {
		t.Fatalf("decode refused extension members: %v", err)
	}
	if got, want := frag.Extensions.Keys(), []string{"x-zeta", "x-alpha"}; !reflect.DeepEqual(got, want) {
		t.Errorf("top-level members = %v, want %v", got, want)
	}
	if got, want := frag.Services["web"].Extensions.Keys(), []string{"x-second", "x-first"}; !reflect.DeepEqual(got, want) {
		t.Errorf("service members = %v, want %v", got, want)
	}
	if frag.Services["web"].Image != "app:1" {
		t.Errorf("the modelled keys beside the members were not decoded: %+v", frag.Services["web"])
	}
}

// TestComposeAssemble_ExtensionMembersFollowTheModelledKeys drives the members
// through the merge: each level writes them after every key it models — the
// network the merge attaches included — in the order the fragment wrote them,
// with each value's own key order kept.
func TestComposeAssemble_ExtensionMembersFollowTheModelledKeys(t *testing.T) {
	t.Parallel()

	assembled, err := assembleOne(t, modelFragment("alpha", extensionFragment))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	serviceTail := "    x-second: two\n    x-first:\n      - one\n"
	topTail := "x-zeta:\n  b: 2\n  a: 1\nx-alpha: kept\n"
	for _, want := range []string{serviceTail, topTail} {
		if !strings.Contains(assembled, want) {
			t.Fatalf("assembled file does not carry %q in written order:\n%s", want, assembled)
		}
	}
	if !strings.HasSuffix(assembled, topTail) {
		t.Errorf("the top-level members are not written after every section:\n%s", assembled)
	}
	attached := strings.Index(assembled, "    networks:\n")
	if attached < 0 || attached > strings.Index(assembled, serviceTail) {
		t.Errorf("the service's members are not written after the keys it models:\n%s", assembled)
	}
	if strings.Count(assembled, "x-") != 4 {
		t.Errorf("want each member written once:\n%s", assembled)
	}
}

// TestComposeAssemble_ExtensionMembersAreWrittenWithoutAnchors pins that a
// member reached through an alias, or holding an anchor, is written out in full:
// the assembled file holds no alias whose anchor it does not also hold.
func TestComposeAssemble_ExtensionMembersAreWrittenWithoutAnchors(t *testing.T) {
	t.Parallel()

	assembled, err := assembleOne(t, modelFragment("alpha",
		"x-base: &base\n  image: app:1\nx-copy: *base\nservices:\n  web:\n    <<: *base\n"))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	for _, want := range []string{"x-base:\n  image: app:1\n", "x-copy:\n  image: app:1\n", "    image: app:1\n"} {
		if !strings.Contains(assembled, want) {
			t.Errorf("assembled file does not carry %q:\n%s", want, assembled)
		}
	}
	if strings.Contains(assembled, "&base") || strings.Contains(assembled, "*base") {
		t.Errorf("assembled file carries an anchor or an alias:\n%s", assembled)
	}
}

// TestComposeAssemble_ATopLevelExtensionMemberTwoContributorsWrite pins the
// merge rule the volumes section follows: two contributors writing one member
// identically leave one entry, and writing it differently halt naming the
// member and both contributors. Each row is one case; the identical rows are the
// positive controls of the differing ones.
func TestComposeAssemble_ATopLevelExtensionMemberTwoContributorsWrite(t *testing.T) {
	t.Parallel()

	withMember := func(node, member string) renderer.EmittedFile {
		return modelFragment(node, "services:\n  "+node+":\n    image: app:1\n"+member)
	}

	for _, tc := range []struct {
		name        string
		alpha, zeta string
		wantHalt    bool
	}{
		{"identically", "x-common:\n  tier: web\n", "x-common:\n  tier: web\n", false},
		// Presentation is not content: a flow mapping and a block mapping holding
		// one key are one declaration.
		{"identically, one in flow style", "x-common: {tier: web}\n", "x-common:\n  tier: web\n", false},
		{"differently", "x-common:\n  tier: web\n", "x-common:\n  tier: db\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := assembleTwo(withMember("alpha", tc.alpha), withMember("zeta", tc.zeta))
			if !tc.wantHalt {
				if err != nil {
					t.Fatalf("assemble refused two contributors agreeing about one member: %v", err)
				}
				assembled := string(out[AssembledOutputFile])
				if strings.Count(assembled, "x-common:") != 1 || !strings.HasSuffix(assembled, "x-common:\n  tier: web\n") {
					t.Errorf("want the one member both contributors described, once:\n%s", assembled)
				}
				return
			}
			if err == nil {
				t.Fatal("assemble accepted two contributors disagreeing about one member; want a halt")
			}
			for _, want := range []string{
				"conflicting extension declarations",
				`extension "x-common"`,
				`node="alpha"`,
				`node="zeta"`,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("halt does not name %s: %s", want, err.Error())
				}
			}
		})
	}
}

// TestComposeAssemble_TwoFragmentsOfOneNodeWritingOneMemberDifferentlyHalt pins
// that every fragment is a contributor of its own: two artifacts: fragments of
// one node disagreeing about one member halt as two nodes' fragments do, rather
// than one of them being kept, and the halt tells the two apart by fragment.
func TestComposeAssemble_TwoFragmentsOfOneNodeWritingOneMemberDifferentlyHalt(t *testing.T) {
	t.Parallel()

	first := modelFragment("alpha", "services:\n  web:\n    image: app:1\nx-common: one\n")
	second := renderer.EmittedFile{
		Name: "extra.yaml", Origin: renderer.EmitOriginArtifactsInline,
		Body: []byte("x-common: two\n"), Intermediate: true,
	}
	_, _, err := AssembleBoundaryCore(
		map[string][]renderer.EmittedFile{"alpha": {first, second}},
		map[string]map[string]string{"alpha": {}},
		nil, nil, "default", 2,
	)
	if err == nil {
		t.Fatal("assemble accepted two fragments of one node disagreeing about one member; want a halt")
	}
	for _, want := range []string{
		`extension "x-common"`,
		`node="alpha" fragment="alpha.compose-fragment.yaml"`,
		`node="alpha" fragment="extra.yaml"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt does not name %s: %s", want, err.Error())
		}
	}
}

// TestDecodeFragment_RefusesAFragmentOfSeveralDocuments pins the refusal of a
// fragment holding more than one YAML document, one fault per row: the halt
// carries the cannot-express condition, names the node, the fragment and how
// many documents it holds, and tells the author both repairs. A document holding
// nothing is not counted.
func TestDecodeFragment_RefusesAFragmentOfSeveralDocuments(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, body, count string
	}{
		{"two documents", "services:\n  one:\n    image: a\n---\nservices:\n  two:\n    image: a\n", "2"},
		{"three documents", "services:\n  one:\n    image: a\n---\nvolumes:\n  data: {}\n---\nx-extra: kept\n", "3"},
		{"two documents around an empty one", "services:\n  one:\n    image: a\n---\n---\nservices:\n  two:\n    image: a\n", "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("decode panicked: %v", r)
					}
				}()
				_, err = decodeFragment([]byte(tc.body), "web", `fragment "extra.compose-fragment.yaml"`)
			}()
			if err == nil {
				t.Fatal("decode accepted a fragment holding several documents")
			}
			if !errors.Is(err, wasm.ErrPluginCannotExpressInput()) {
				t.Errorf("refusal does not carry the cannot-express condition: %v", err)
			}
			msg := err.Error()
			for _, want := range []string{
				`node "web" fragment "extra.compose-fragment.yaml" holds ` + tc.count + " YAML documents",
				"write the fragment as one document",
				"give each document an artifacts: entry of its own",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal does not carry %q:\n%s", want, msg)
				}
			}
		})
	}
}

// TestComposeAssemble_AFragmentOfSeveralDocumentsHaltsTheMerge drives the
// refusal through the merge: the assembly halts naming the fragment rather than
// writing a file holding the first document's service alone.
func TestComposeAssemble_AFragmentOfSeveralDocumentsHaltsTheMerge(t *testing.T) {
	t.Parallel()

	_, err := assembleOne(t, modelFragment("alpha", "services:\n  one:\n    image: a\n---\nservices:\n  two:\n    image: a\n"))
	if !errors.Is(err, wasm.ErrPluginCannotExpressInput()) {
		t.Fatalf("got %v, want the cannot-express refusal of a fragment of several documents", err)
	}
	if !strings.Contains(err.Error(), `node "alpha" fragment "alpha.compose-fragment.yaml" holds 2 YAML documents`) {
		t.Errorf("refusal does not name the fragment and the count:\n%v", err)
	}
}
