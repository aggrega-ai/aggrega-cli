// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// TestLogicalIdKeysRoundTrip keys each resource by its logical ID's words,
// hyphenated and lowercased, and says above the instance whether the stack the
// build writes names the resource as the template does: the Recipes name it by
// the capitalised words of its key, which give OrdersDb, S3Bucket and MyQueue2
// back and turn DBInstance into DbInstance.
func TestLogicalIdKeysRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id, key, note string
	}{
		{"OrdersDb", "orders-db", ""},
		{"DBInstance", "db-instance", "The stack the build writes names it DbInstance, the capitalised words of its " +
			"key, where the template names it DBInstance, so updating a stack the template deployed replaces the " +
			"resource."},
		{"S3Bucket", "s3-bucket", ""},
		{"MyQueue2", "my-queue2", ""},
	}
	for _, c := range cases {
		if got := Key(c.id); got != c.key {
			t.Errorf("Key(%q) = %q, want %q", c.id, got, c.key)
		}
		if got := keyNote(c.id, c.key); got != c.note {
			t.Errorf("keyNote(%q, %q) = %q, want %q", c.id, c.key, got, c.note)
		}
	}

	nodes := []*cloudimport.Node{{ID: "Db"}, {ID: "DB"}}
	keys := cloudimport.Format{Key: Key}.Keys(nodes)
	if keys[0] != "db" || keys[1] != "db-2" {
		t.Fatalf("the keys of Db and DB are %q, want [db db-2]", keys)
	}
	if got, want := keyNote("Db", keys[0]), ""; got != want {
		t.Errorf("keyNote(Db, db) = %q, want %q", got, want)
	}
	if got, want := keyNote("DB", keys[1]), "The stack the build writes names it Db2, the capitalised words of its "+
		"key, where the template names it DB, so updating a stack the template deployed replaces the resource."; got != want {
		t.Errorf("keyNote(DB, db-2) = %q, want %q", got, want)
	}
}
