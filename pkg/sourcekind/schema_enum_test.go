// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcekind_test

import (
	"encoding/json"
	"slices"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
)

// kindEnum is the one `kind` property a schema records a resolved source's kind in.
type kindEnum struct {
	Enum []string `json:"enum"`
}

// TestSchemaSourceKindEnumsEqualTheServedSet holds the `kind` enum of the three
// schemas that record a resolved source — the entity lock's source digests, the
// lockfile's source artifacts and a cache slot's metadata — equal to the kinds this
// program serves. The schemas and the compiler are written in two repositories, so
// nothing else notices one gaining a kind the other lacks: a served kind no schema
// admits writes rows every load then refuses, and a kind a schema admits that
// nothing serves is a row no resolution can produce. Each side's missing words are
// named.
func TestSchemaSourceKindEnumsEqualTheServedSet(t *testing.T) {
	t.Parallel()

	var entityLock struct {
		Properties struct {
			SourceDigests struct {
				Items struct {
					Properties struct {
						Kind kindEnum `json:"kind"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"sourceDigests"`
		} `json:"properties"`
	}
	var lockfile struct {
		Defs struct {
			SourceArtifactEntry struct {
				Properties struct {
					Kind kindEnum `json:"kind"`
				} `json:"properties"`
			} `json:"SourceArtifactEntry"`
		} `json:"$defs"`
	}
	var cacheMetadata struct {
		Properties struct {
			Kind kindEnum `json:"kind"`
		} `json:"properties"`
	}
	for name, parse := range map[string]struct {
		document string
		into     any
	}{
		"the entity lock schema (sourceDigests.items)":    {schemas.EntityLockV1beta1, &entityLock},
		"the lockfile schema ($defs.SourceArtifactEntry)": {schemas.LockfileV1beta1, &lockfile},
		"the cache metadata schema (properties.kind)":     {schemas.CacheMetadataV1beta1, &cacheMetadata},
	} {
		if err := json.Unmarshal([]byte(parse.document), parse.into); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
	}

	enums := map[string][]string{
		"the entity lock schema (sourceDigests.items)":    entityLock.Properties.SourceDigests.Items.Properties.Kind.Enum,
		"the lockfile schema ($defs.SourceArtifactEntry)": lockfile.Defs.SourceArtifactEntry.Properties.Kind.Enum,
		"the cache metadata schema (properties.kind)":     cacheMetadata.Properties.Kind.Enum,
	}
	served := sourcekind.ServedKinds()
	if len(served) == 0 {
		t.Fatal("the served set is empty, so the comparison below would compare nothing")
	}
	for name, enum := range enums {
		if len(enum) == 0 {
			t.Errorf("%s enumerates no source kind, so the comparison would compare nothing", name)
			continue
		}
		for _, kind := range served {
			if !slices.Contains(enum, kind) {
				t.Errorf("this program serves the source kind %q and %s does not admit it", kind, name)
			}
		}
		for _, kind := range enum {
			if !slices.Contains(served, kind) {
				t.Errorf("%s admits the source kind %q and this program serves no such kind", name, kind)
			}
		}
	}
}
