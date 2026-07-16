// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// errIndexNotBuilt reports that a registry host has no local searchable index on
// disk. It is distinct from a read or parse failure so a caller can tell "the
// index has not been built yet" from a genuine on-disk fault and fail loudly with
// the right guidance.
var errIndexNotBuilt = errors.New("no local index has been built for this registry")

// loadedIndex is one host's local searchable index read back off disk: the host
// it was built from, its entity records in the deterministic order the index
// writer stored them, and the freshness timestamp from the sibling file.
// HasTimestamp distinguishes a genuinely absent timestamp sibling (LastUpdated is
// the zero time) from one that happens to parse to the zero time, so a caller can
// decide how to present freshness.
type loadedIndex struct {
	Host         string
	Records      []IndexRecord
	LastUpdated  time.Time
	HasTimestamp bool
}

// loadHostIndex reads one host's local searchable index from the artifact cache
// and returns its records plus the freshness timestamp. The records file and its
// timestamp sibling are located through the single index-path helper, so a reader
// and the writer cannot disagree about a host's on-disk layout.
//
// An absent records file returns errIndexNotBuilt, distinct from a read or parse
// fault, so the caller can distinguish "run the index build first" from a
// corrupt cache. A records file that is present but cannot be read or unmarshalled
// returns a wrapped error naming the path.
//
// A missing timestamp sibling is not fatal: the records are returned with
// HasTimestamp false and the zero time, since the records stand on their own and
// only the freshness display depends on the timestamp. A timestamp sibling that
// is present but unreadable or not a valid RFC3339 line is a genuine fault and
// returns a wrapped error.
func loadHostIndex(workspaceRoot, cacheDir, host string) (loadedIndex, error) {
	base := oci.IndexBasePath(workspaceRoot, cacheDir, host)
	recordsPath := base + ".json"
	data, err := os.ReadFile(recordsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return loadedIndex{}, errIndexNotBuilt
		}
		return loadedIndex{}, fmt.Errorf("read index %s: %w", recordsPath, err)
	}
	var idx RegistryIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return loadedIndex{}, fmt.Errorf("parse index %s: %w", recordsPath, err)
	}
	result := loadedIndex{Host: idx.Host, Records: idx.Records}

	timestampPath := base + ".updated"
	stamp, err := os.ReadFile(timestampPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return result, nil
		}
		return loadedIndex{}, fmt.Errorf("read index timestamp %s: %w", timestampPath, err)
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(string(stamp)))
	if err != nil {
		return loadedIndex{}, fmt.Errorf("parse index timestamp %s: %w", timestampPath, err)
	}
	result.LastUpdated = ts
	result.HasTimestamp = true
	return result, nil
}
