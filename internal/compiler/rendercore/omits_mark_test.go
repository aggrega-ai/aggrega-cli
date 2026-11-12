// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"bytes"
	"slices"
	"testing"
)

// TestOmitEntriesOf_MarksAnEntryAnyOfWhoseFieldsIsWithheld holds the one place an
// omit entry's mark is derived to the record the host already keeps of which fields
// a declared value produced: an entry is marked exactly when that record names any
// field, whichever field it is, and an entry it names none of carries no mark.
func TestOmitEntriesOf_MarksAnEntryAnyOfWhoseFieldsIsWithheld(t *testing.T) {
	t.Parallel()
	entry := OmitEntry{Kind: "Secret", Name: "token", Namespace: "apps", Subchart: "auth"}
	for _, tc := range []struct {
		name     string
		withheld OmitFieldsWithheld
		marked   bool
	}{
		{name: "no field withheld", withheld: OmitFieldsWithheld{}, marked: false},
		{name: "the kind withheld", withheld: OmitFieldsWithheld{Kind: true}, marked: true},
		{name: "the name withheld", withheld: OmitFieldsWithheld{Name: true}, marked: true},
		{name: "the namespace withheld", withheld: OmitFieldsWithheld{Namespace: true}, marked: true},
		{name: "the subchart withheld", withheld: OmitFieldsWithheld{Subchart: true}, marked: true},
		{name: "every field withheld", withheld: OmitFieldsWithheld{Kind: true, Name: true, Namespace: true, Subchart: true}, marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := OmitEntriesOf([]ResolvedOmit{{Entry: entry, Position: 4, Withheld: tc.withheld}})
			want := entry
			want.Sensitive = tc.marked
			if len(got) != 1 || got[0] != want {
				t.Fatalf("got %+v, want [%+v]", got, want)
			}
		})
	}
	if got := OmitEntriesOf(nil); got != nil {
		t.Errorf("no entries built a list: %+v", got)
	}
}

// TestOmitEntriesOf_AnUnmarkedEntryEncodesAsItDidWithoutTheMark pins the other side of
// the mark being absent when false: an entry no declared value produced has the
// bytes it would have if the mark did not exist, so a render input carrying only
// such entries is unchanged by the mark's existence, and a marked one differs from
// it by the mark alone.
func TestOmitEntriesOf_AnUnmarkedEntryEncodesAsItDidWithoutTheMark(t *testing.T) {
	t.Parallel()
	entry := OmitEntry{Kind: "Secret", Name: "token"}
	plain, err := OmitEntriesOf([]ResolvedOmit{{Entry: entry}})[0].MarshalJSON()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if want := []byte(`{"kind":"Secret","name":"token"}`); !bytes.Equal(plain, want) {
		t.Errorf("an unmarked entry encodes as %s, want %s", plain, want)
	}
	marked, err := OmitEntriesOf([]ResolvedOmit{{Entry: entry, Withheld: OmitFieldsWithheld{Name: true}}})[0].MarshalJSON()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if want := []byte(`{"kind":"Secret","name":"token","sensitive":true}`); !bytes.Equal(marked, want) {
		t.Errorf("a marked entry encodes as %s, want %s", marked, want)
	}
}

// TestOmitEntriesOf_ASubchartIsCarriedOnlyWhereTheEntryNamesOne pins the subchart on
// the wire: an entry naming one carries it after its namespace, and an entry naming
// none carries no key for it, so every entry written before the field existed keeps
// the bytes it had.
func TestOmitEntriesOf_ASubchartIsCarriedOnlyWhereTheEntryNamesOne(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		entry OmitEntry
		want  string
	}{
		{name: "no subchart", entry: OmitEntry{Kind: "Secret", Name: "token", Namespace: "apps"},
			want: `{"kind":"Secret","name":"token","namespace":"apps"}`},
		{name: "a subchart", entry: OmitEntry{Kind: "Secret", Name: "token", Namespace: "apps", Subchart: "auth/webhooks"},
			want: `{"kind":"Secret","name":"token","namespace":"apps","subchart":"auth/webhooks"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := OmitEntriesOf([]ResolvedOmit{{Entry: tc.entry}})[0].MarshalJSON()
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("encodes as %s, want %s", got, tc.want)
			}
		})
	}
}

// TestResolvedOmitsOf_AMarkedEntryWithholdsEveryField holds the receiving direction:
// a guest told an entry is marked is not told which field a declared value produced,
// so it withholds them all, while an unmarked entry withholds none. Rebuilding the
// wire list from what was received gives back the list that was sent, so the two
// directions stay one correspondence.
func TestResolvedOmitsOf_AMarkedEntryWithholdsEveryField(t *testing.T) {
	t.Parallel()
	wire := []OmitEntry{
		{Kind: "Secret", Name: "token", Namespace: "apps", Subchart: "auth", Sensitive: true},
		{Kind: "ConfigMap", Name: "settings"},
	}
	got := ResolvedOmitsOf(wire)
	want := []ResolvedOmit{
		{Entry: OmitEntry{Kind: "Secret", Name: "token", Namespace: "apps", Subchart: "auth"}, Position: 0,
			Withheld: OmitFieldsWithheld{Kind: true, Name: true, Namespace: true, Subchart: true}},
		{Entry: OmitEntry{Kind: "ConfigMap", Name: "settings"}, Position: 1},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if back := OmitEntriesOf(got); !slices.Equal(back, wire) {
		t.Errorf("the list rebuilt from what was received differs from what was sent:\ngot  %+v\nwant %+v", back, wire)
	}
	if got := ResolvedOmitsOf(nil); got != nil {
		t.Errorf("an input carrying no entries rebuilt some: %+v", got)
	}
}
