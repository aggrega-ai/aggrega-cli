// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import "strings"

// MechanismProperty is the inject mechanism a draft reads a neighbour's value
// on where the value goes into a property of the resource: the Aggregate's uses
// edge delivers it under inject.property, and the Recipe reads it from
// .context.inject.property under the property's key.
const MechanismProperty = "property"

// InjectKey is the key a neighbour's value for the property at path is
// delivered under on the property mechanism: the snake_case of the whole path,
// as SnakeKey writes the path joined by dots, so each segment gives its own
// words and an array item's index, a segment written in decimal, gives a word of
// its digits. The path properties.configuration.registries[0].server, written
// [properties configuration registries 0 server], gives
// properties_configuration_registries_0_server.
func InjectKey(path []string) string {
	return SnakeKey(strings.Join(path, "."))
}

// injectRule is the rule of the property p a neighbour supplies: read off the
// property mechanism and written at p's path.
func injectRule(p *Property) string {
	return "inject: a value a neighbour publishes, delivered by the Aggregate's uses edge on the " + MechanismProperty +
		" mechanism and written at " + vendorPath(p)
}
