// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestDepthRefusalNamesTheDocumentAndTheLimit holds the refusal of a document
// nested past the bound to the words its author reads: what the document does,
// and how deep it is read — never the program's own type name. The YAML read is
// the one an authored document takes.
func TestDepthRefusalNamesTheDocumentAndTheLimit(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	indent := ""
	for i := 0; i < orderedMapDecodeMaxDepth+10; i++ {
		fmt.Fprintf(&b, "%sk%d:\n", indent, i)
		indent += "  "
	}
	fmt.Fprintf(&b, "%sleaf: x\n", indent)

	var m OrderedMap[any]
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("decoding panicked: %v", r)
			}
		}()
		err = yaml.Unmarshal([]byte(b.String()), &m)
	}()
	if !errors.Is(err, ErrOrderedMapDecodeDepthExceeded()) {
		t.Fatalf("a document nested past the bound was not refused for its depth: %v", err)
	}
	want := fmt.Sprintf("a document nests too many levels deep: it is read at most %d levels deep", orderedMapDecodeMaxDepth)
	if !strings.HasSuffix(err.Error(), want) {
		t.Errorf("the refusal does not end %q:\n%v", want, err)
	}
	if strings.Contains(err.Error(), "OrderedMap") {
		t.Errorf("the refusal names the program's type, which the author never wrote:\n%v", err)
	}
}
