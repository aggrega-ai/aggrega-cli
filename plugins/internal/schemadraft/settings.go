// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	resourceTypesSetting = "resource_types"
	requestTypeKey       = "type"
	requestComponentKey  = "component"
)

// ReadSettings reads the settings a schema import takes off the settings
// document. resource_types is read off the document's own members, so that a
// value another setting holds cannot leave it unread, and a value it does not
// take halts the import rather than reading as none; where the document sets it
// more than once, the last is read. Every other setting is ignored, except
// target_namespace, which the importer kit reads.
func ReadSettings(raw json.RawMessage) (Settings, *aggrega.Diagnostic) {
	s := Settings{TargetNamespace: importer.TargetNamespace(raw)}
	var value json.RawMessage
	for _, e := range importer.ObjectEntries(raw) {
		if e.Key == resourceTypesSetting {
			value = e.Value
		}
	}
	if value == nil {
		return s, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(value, &entries); err != nil || entries == nil {
		return Settings{}, resourceTypesUnreadable(value, "it is not a list")
	}
	for _, entry := range entries {
		req, why := readRequest(entry)
		if why != "" {
			return Settings{}, resourceTypesUnreadable(entry, why)
		}
		s.ResourceTypes = append(s.ResourceTypes, req)
	}
	return s, nil
}

// readRequest reads one entry: a string naming a type, or an object with a type
// and optionally a component. The reason is empty where the entry is read.
//
// A component is held to the rule the Component schema declares for a
// metadata.name, the one rule a name meets wherever it is read: it is not empty.
// The compiler reads a name by no other rule, and the shape the catalog's names
// follow by convention is the publishing linter's to report, so an entry naming
// a Component the catalog can hold is never refused here for its shape. An empty
// component is refused rather than read as none.
func readRequest(entry json.RawMessage) (Request, string) {
	var name string
	if json.Unmarshal(entry, &name) == nil {
		return splitType(name, "")
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(entry, &probe) != nil || probe == nil {
		return Request{}, "an entry is neither a name nor an object with a type"
	}
	var typ, component string
	var hasType bool
	for _, e := range importer.ObjectEntries(entry) {
		switch e.Key {
		case requestTypeKey:
			if json.Unmarshal(e.Value, &typ) != nil {
				return Request{}, "the type of an entry is not text"
			}
			hasType = true
		case requestComponentKey:
			if json.Unmarshal(e.Value, &component) != nil {
				return Request{}, "the component of an entry is not text"
			}
			if component == "" {
				return Request{}, "the component of an entry is empty, and a Component's name is not; name the " +
					"Component to draft the type as, or leave component out"
			}
		default:
			return Request{}, "an entry has the member \"" + e.Key + "\", which is neither type nor component"
		}
	}
	if !hasType {
		return Request{}, "an entry has no type"
	}
	return splitType(typ, component)
}

// splitType splits name at its apiVersion, where it carries one.
func splitType(name, component string) (Request, string) {
	typ, version := name, ""
	if i := strings.LastIndex(name, "@"); i >= 0 {
		typ, version = name[:i], name[i+1:]
		if version == "" {
			return Request{}, "an entry names an apiVersion after @ and writes none"
		}
	}
	if typ == "" {
		return Request{}, "an entry names no type"
	}
	return Request{Type: typ, APIVersion: version, Component: component}, ""
}
