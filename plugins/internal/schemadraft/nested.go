// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// SetAt is the template text setting the value value evaluates to at the path
// below inside the object the variable target holds, its variables numbered n;
// prefix is the path of that object from the resource's body, by which the
// halts name each value.
// The first segment of below is a member of target, whose value is copied, or
// created as an object where target lacks it; a later segment of decimal digits
// is an item of the list it reads, any other a member of the object it reads,
// created where the object lacks it. Where a value on the path is not the list
// or object a segment reads, or the list holds no such item, it halts naming the
// path. The value replaces the member or item the path ends at; a list whose
// item it replaces is rebuilt and set where the list was read, up to the first
// object. Its last action is a set, ending " }}".
func SetAt(target string, prefix, below []string, value string, n int) string {
	where := func(i int) string {
		return strings.Join(append(slices.Clone(prefix), below[:i]...), ".")
	}
	whole := where(len(below))
	suffix := strconv.Itoa(n)
	held := func(i int) string { // the variable holding the value at below[:i]
		if i == 0 {
			return target
		}
		return "$at" + suffix + "_" + strconv.Itoa(i)
	}
	set := "$set" + suffix
	fail := func(text string) string { return "{{ fail " + strconv.Quote(text) + " }}" }
	isMap := func(i int) string {
		return "{{ if not (kindIs \"map\" " + held(i) + ") }}" + fail(fmt.Sprintf("%s is not an object, and this "+
			"Recipe writes %s inside it", where(i), whole)) + "{{ end }}"
	}
	isList := func(i, item int) string {
		return "{{ if not (and (kindIs \"slice\" " + held(i) + ") (gt (len " + held(i) + ") " + strconv.Itoa(item) +
			")) }}" + fail(fmt.Sprintf("%s is not a list holding the item %d, and this Recipe writes %s into that "+
			"item: keep the item in the value the Release sets", where(i), item, whole)) + "{{ end }}"
	}

	var b strings.Builder
	b.WriteString("{{ " + set + " := " + value + " }}")
	first := strconv.Quote(below[0])
	b.WriteString("{{ if hasKey " + target + " " + first + " }}{{ $_ := set " + target + " " + first +
		" (deepCopy (index " + target + " " + first + ")) }}{{ else }}{{ $_ := set " + target + " " + first +
		" dict }}{{ end }}{{ " + held(1) + " := index " + target + " " + first + " }}")
	last := len(below) - 1
	for i := 1; i < last; i++ {
		if item, ok := ItemIndex(below[i]); ok {
			b.WriteString(isList(i, item) + "{{ " + held(i+1) + " := index " + held(i) + " " + strconv.Itoa(item) + " }}")
			continue
		}
		member := strconv.Quote(below[i])
		b.WriteString(isMap(i) + "{{ if not (hasKey " + held(i) + " " + member + ") }}{{ $_ := set " + held(i) + " " +
			member + " dict }}{{ end }}{{ " + held(i+1) + " := index " + held(i) + " " + member + " }}")
	}

	// From the path's end up: an object takes its member in place and ends the
	// walk; a list is rebuilt with its item replaced, and the rebuilt list is
	// set where the list was read.
	replacing := set
	for i := last; ; i-- {
		item, ok := ItemIndex(below[i])
		if !ok || i == 0 {
			if i > 0 {
				b.WriteString(isMap(i))
			}
			b.WriteString("{{ $_ := set " + held(i) + " " + strconv.Quote(below[i]) + " " + replacing + " }}")
			return b.String()
		}
		list := "$list" + suffix + "_" + strconv.Itoa(i)
		if i == last {
			b.WriteString(isList(i, item))
		}
		b.WriteString("{{ " + list + " := list }}{{ range $i, $item := " + held(i) + " }}{{ if eq $i " +
			strconv.Itoa(item) + " }}{{ " + list + " = append " + list + " " + replacing + " }}{{ else }}{{ " + list +
			" = append " + list + " $item }}{{ end }}{{ end }}")
		replacing = list
	}
}

// ItemIndex reads segment, one segment of a property path, as the index of an
// array item, written in decimal, as a path names an item; false where it is a
// member's name.
func ItemIndex(segment string) (int, bool) {
	if segment == "" || strings.Trim(segment, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(segment)
	return n, err == nil
}
