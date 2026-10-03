// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the connection half of the per-attachment facts clause: an
// attachment written on one of a node's connections names that connection on
// its own line, read from the entry's connection field, while an attachment of
// the node's own block names none. Every row that prints the half sits beside
// one that must not, over the same fixture shape, so a display that printed the
// half for every attachment — or for none — fails here.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestAttachmentFactsBracket_ConnectionHalf enumerates the connection half
// against every state of the two facts it shares the clause with, and asserts
// the exact clause rather than its presence, so the half's position and
// separators are pinned along with its content.
//
// Two rows are hostile on purpose. The control row moves only the connection
// and keeps the slot and the mark, so a display that keyed the half on the slot
// or on the mark rather than on the connection fails it. The swapped row puts
// each coordinate in the other's field, so a display that printed the same
// field twice, or read the two in the wrong order, prints a different clause
// from the one asserted.
func TestAttachmentFactsBracket_ConnectionHalf(t *testing.T) {
	t.Parallel()
	gpt := &types.AttachmentConnection{Socket: "models", Alias: "gpt"}
	for _, tc := range []struct {
		name  string
		entry types.IRFeatureEntry
		want  string
	}{
		{
			name:  "on a connection, filling the socket's slot, marked",
			entry: types.IRFeatureEntry{Connection: gpt, Binding: "credential", Sensitive: true},
			want:  "[connection: gpt through socket models, slot: credential, sensitive]",
		},
		{
			name:  "on a connection, filling the socket's slot, unmarked",
			entry: types.IRFeatureEntry{Connection: gpt, Binding: "credential"},
			want:  "[connection: gpt through socket models, slot: credential]",
		},
		{
			name:  "on a connection, slot set unresolved, unmarked",
			entry: types.IRFeatureEntry{Connection: gpt},
			want:  "[connection: gpt through socket models]",
		},
		{
			name:  "on a connection, slot set unresolved, marked",
			entry: types.IRFeatureEntry{Connection: gpt, Sensitive: true},
			want:  "[connection: gpt through socket models, sensitive]",
		},
		{
			name:  "control: the node's own attachment, same slot name and mark",
			entry: types.IRFeatureEntry{Binding: "credential", Sensitive: true},
			want:  "[slot: credential, sensitive]",
		},
		{
			name: "socket and alias each read from their own field",
			entry: types.IRFeatureEntry{
				Connection: &types.AttachmentConnection{Socket: "gpt", Alias: "models"},
				Binding:    "credential",
			},
			want: "[connection: models through socket gpt, slot: credential]",
		},
	} {
		if got := attachmentFactsBracket(tc.entry); got != tc.want {
			t.Errorf("%s: attachmentFactsBracket = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRenderFeatureAttachment_ConnectionIsReadFromTheEntryNotTheName holds the
// attachment's qualified name fixed and moves only the entry's connection field,
// in both layouts. The name carries the alias, so a display that read the
// connection out of the name would print the half for both entries; one that
// read the field prints it for the entry that carries one and for no other. The
// name is also passed unchanged into the line, which the assertions pin, so the
// half is not achieved by rewriting the name.
func TestRenderFeatureAttachment_ConnectionIsReadFromTheEntryNotTheName(t *testing.T) {
	t.Parallel()
	const qualified = "gpt.credential"
	for _, layout := range []struct {
		name  string
		build func(binding string, sensitive bool) types.IRFeatureEntry
	}{
		{"compact", uniformAttachment},
		{"per-leaf", heterogeneousAttachment},
	} {
		onConnection := layout.build("credential", true)
		onConnection.Connection = &types.AttachmentConnection{Socket: "models", Alias: "gpt"}
		own := layout.build("credential", true)

		withConnection := renderAttachmentNamed(t, qualified, onConnection)
		withoutConnection := renderAttachmentNamed(t, qualified, own)

		wantWith := "    - gpt.credential (feature.test/db-credential)    [connection: gpt through socket models, slot: credential, sensitive]"
		if !strings.HasPrefix(withConnection, wantWith) {
			t.Errorf("%s: an attachment carrying a connection\n got: %q\nwant prefix: %q", layout.name, withConnection, wantWith)
		}
		wantWithout := "    - gpt.credential (feature.test/db-credential)    [slot: credential, sensitive]"
		if !strings.HasPrefix(withoutConnection, wantWithout) {
			t.Errorf("%s: an attachment carrying no connection\n got: %q\nwant prefix: %q", layout.name, withoutConnection, wantWithout)
		}
		if strings.Contains(withoutConnection, "connection:") {
			t.Errorf("%s: a connection was read out of the attachment's name\n--- output ---\n%s", layout.name, withoutConnection)
		}
	}
}

// renderAttachmentNamed renders one attachment under the given name, with no
// declaration marking any of its leaves.
func renderAttachmentNamed(t *testing.T, name string, entry types.IRFeatureEntry) string {
	t.Helper()
	var buf bytes.Buffer
	renderFeatureAttachment(&buf, name, entry, resolver.SensitiveLeaves{})
	return buf.String()
}

// TestInspectRelease_ConnectionAttachmentOverRealPipeline is the end-to-end
// half. It resolves a Release through the funnel the command uses, over a
// fixture whose component carries one attachment of its own block and one
// written on a connection, each filling a slot declared sensitive — the first a
// slot the Component declares for its own attachments, the second a slot the
// connection's socket declares. So the two lines differ in the connection half
// and in nothing else the clause prints, and both facts on the connection's
// line are ones the compiler derived from declarations rather than literals.
func TestInspectRelease_ConnectionAttachmentOverRealPipeline(t *testing.T) {
	t.Parallel()
	const (
		release = "../../internal/compiler/testdata/connection-attachment/release.yaml"
		catalog = "../../internal/compiler/testdata/connection-attachment/catalog/base"
	)
	var buf bytes.Buffer
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), release, []string{catalog}, testInspectOptions(&buf)); err != nil {
		t.Fatalf("dispatchInspect(%q): %v", release, err)
	}
	got := buf.String()

	for _, want := range []string{
		"    - gpt.credential (feature.conn/token:1.0.0)    [connection: gpt through socket models, slot: credential, sensitive]    [explicit]\n",
		"    - db_password (feature.conn/token:1.0.0)    [slot: db_password, sensitive]    [explicit]\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("resolved Release view missing %q\n--- output ---\n%s", want, got)
		}
	}
	// The connection is named once, on the one attachment written on it.
	if n := strings.Count(got, "connection: "); n != 1 {
		t.Errorf("the view names a connection %d times, want once\n--- output ---\n%s", n, got)
	}
}
