// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// Key gives the instance key of a resource from its logical ID: the ID's
// words, lowercased and joined by hyphens, read as [schemadraft.SnakeKey]
// reads the words of a vendor name — a word ends at a lower-case letter or a
// digit an upper-case letter follows, and before the last upper-case letter of
// a run a lower-case letter follows. So OrdersDb gives orders-db, DBInstance
// db-instance, S3Bucket s3-bucket and MyQueue2 my-queue2.
//
// The cloudformation Recipes name the resource an instance deploys by the
// capitalised words of its key, which give the logical ID back wherever the ID
// is written as capitalised words: OrdersDb builds back as OrdersDb, and
// DBInstance as DbInstance.
func Key(logicalID string) string {
	return strings.ReplaceAll(schemadraft.SnakeKey(logicalID), "_", "-")
}

// keyNote returns the comment line written above the instance keyed key that
// is drafted from the resource of the logical ID id, where the stack the build
// writes names that resource otherwise than the template does: the
// cloudformation Recipes name it by the capitalised words of its key, read by
// [alphanumeric.CapitalizedWords]. It is empty where those words are the ID.
func keyNote(id, key string) string {
	built := alphanumeric.CapitalizedWords(key)
	if built == id {
		return ""
	}
	return "The stack the build writes names it " + built + ", the capitalised words of its key, where the " +
		"template names it " + draftyaml.Comment(id) + "."
}
