// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// SnakeKey writes a vendor property name as a snake_case key: words split at
// every character other than an ASCII letter or digit, at a lower-case letter
// or digit followed by an upper-case one, and before the last upper-case letter
// of a run that a lower-case letter follows, then lowercased and joined by one
// underscore. So KmsKeyId and KMSKeyId both give kms_key_id, VPCSecurityGroupIds
// gives vpc_security_group_ids and a name already in snake_case is kept.
func SnakeKey(vendor string) string {
	runes := []rune(vendor)
	var words []string
	var word []rune
	flush := func() {
		if len(word) > 0 {
			words = append(words, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	for i, r := range runes {
		switch {
		case isUpper(r):
			if len(word) > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && isLower(runes[i+1])
				if isLower(prev) || isDigit(prev) || (isUpper(prev) && nextLower) {
					flush()
				}
			}
			word = append(word, r)
		case isLower(r), isDigit(r):
			word = append(word, r)
		default:
			flush()
		}
	}
	flush()
	return strings.Join(words, "_")
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
func isLower(r rune) bool { return r >= 'a' && r <= 'z' }
func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// keySpace hands out the keys of one object of a draft: a pillar, or the
// members of one nested object. A key is the vendor name's SnakeKey, or that
// with the kit's numeric suffix where a name taken earlier holds it.
type keySpace struct {
	// used is the kit's record of the keys taken, in its own spelling.
	used map[string]bool
	// takers names the vendor name that took each key, empty for a key the
	// space holds for itself.
	takers map[string]string
}

// newKeySpace returns a space in which the keys reserved are taken already.
func newKeySpace(reserved ...string) *keySpace {
	s := &keySpace{used: map[string]bool{}, takers: map[string]string{}}
	for _, key := range reserved {
		importer.UniqueKey(s.used, key)
		s.takers[key] = ""
	}
	return s
}

// take returns the key vendor takes, and where it is not the vendor name's
// SnakeKey, composed text naming both the vendor name and what holds that key.
//
// The kit's UniqueKey writes a key with hyphens. A SnakeKey is words of a-z and
// 0-9 joined by underscores, so the kit's reading of it is the same words
// joined by hyphens and writing its hyphens back as underscores gives the
// snake_case key with the kit's suffix.
func (s *keySpace) take(vendor string) (key, renamed string) {
	base := SnakeKey(vendor)
	key = strings.ReplaceAll(importer.UniqueKey(s.used, base), "-", "_")
	if key != base && base != "" {
		if by := s.takers[base]; by != "" {
			renamed = fmt.Sprintf("%s is written %s, since %s takes %s", vendor, key, by, base)
		} else {
			renamed = fmt.Sprintf("%s is written %s, since %s holds the writable settings in the vendor's spelling",
				vendor, key, base)
		}
	}
	s.takers[key] = vendor
	return key, renamed
}

// memberKey is one member of a nested object and the key it is drafted under.
type memberKey struct {
	Prop *Property
	Key  string
	// Renamed is the composed text naming both vendor names where the key
	// carries the kit's suffix, empty otherwise.
	Renamed string
}

// memberKeys returns the members of the object p a draft describes, in the
// vendor's order, each with its key. Carry describes these members and a
// Respeller reads them, so the Component and the Recipe name each member alike.
func memberKeys(p *Property) []memberKey {
	space := newKeySpace()
	var out []memberKey
	for _, m := range p.Properties {
		if readBack(m) {
			continue
		}
		key, renamed := space.take(m.Name)
		out = append(out, memberKey{Prop: m, Key: key, Renamed: renamed})
	}
	return out
}

// describesMembers reports whether a draft describes the members of p one by
// one: a closed object, neither cut nor of several kinds, with a member that is
// an input. Any other value is passed to the vendor whole.
func describesMembers(p *Property) bool {
	return kindAlone(p, KindObject) && !p.Open && p.Cut == "" && len(memberKeys(p)) > 0
}

// rebuildsList reports whether p is a list whose items a draft rebuilds.
func rebuildsList(p *Property) bool {
	return kindAlone(p, KindArray) && p.Items != nil && (describesMembers(p.Items) || rebuildsList(p.Items))
}

// Respell returns the template text that writes, as JSON, the vendor-spelled
// value of p built from the snake_case pillar value expr evaluates to: the
// statements a fresh Respeller's Value gives, followed by toJson of its value.
// It holds no line break and no trim marker, so it stands after a key as
// {{ toJson <expr> }} would, which is what it is for a value passed whole: a
// scalar, an open or cut object, or a list of such values. Two Respell texts
// written into one template each number their variables from one, so the
// second redeclares the first's; a text building several values that it reads
// together takes one Respeller instead.
func Respell(p *Property, expr string) string {
	var r Respeller
	statements, value := r.Value(p, expr)
	return statements + "{{ toJson " + value + " }}"
}

// Respeller rebuilds vendor-spelled values within one template text, numbering
// the variables of every value it rebuilds in turn, so the values of one text
// never share a variable. A writer building a whole body as a dict takes one
// Respeller for the text and sets each value Value gives into the dict.
type Respeller struct{ n int }

// Value returns the statements that build the vendor-spelled value of p from the
// snake_case pillar value expr evaluates to, and the template expression
// holding it once they have run. A value passed whole — a scalar, an open or cut
// object, or a list of such values — takes no statement, and its expression is
// expr.
//
// A closed object is rebuilt member by member under the vendor's names, and a
// list of such objects item by item. A member is copied where the value holds
// its key, tested with hasKey and read with index, so a member set to false, 0
// or "" is copied as set and an unset one is left out. The statements hold no
// line break and no trim marker, and only assign, so they write nothing; the
// expression is read in the scope they run in. Their variables are named $in,
// $out and $item followed by a number, and they discard what set returns into
// $_; text they are written into declares none of the numbered names.
func (r *Respeller) Value(p *Property, expr string) (statements, value string) {
	var b strings.Builder
	value = respell(&b, p, expr, &r.n)
	return b.String(), value
}

// respell writes the statements building the vendor-spelled value of p from
// expr and returns the expression holding it. n numbers the variables, so no
// two statements of one text share one.
func respell(b *strings.Builder, p *Property, expr string, n *int) string {
	switch {
	case describesMembers(p):
		*n++
		in, out := "$in"+strconv.Itoa(*n), "$out"+strconv.Itoa(*n)
		b.WriteString("{{ " + in + " := " + expr + " }}{{ " + out + " := dict }}")
		for _, m := range memberKeys(p) {
			key := strconv.Quote(m.Key)
			b.WriteString("{{ if hasKey " + in + " " + key + " }}")
			value := respell(b, m.Prop, "(index "+in+" "+key+")", n)
			b.WriteString("{{ $_ := set " + out + " " + strconv.Quote(m.Prop.Name) + " " + value + " }}{{ end }}")
		}
		return out
	case rebuildsList(p):
		*n++
		item, out := "$item"+strconv.Itoa(*n), "$out"+strconv.Itoa(*n)
		b.WriteString("{{ " + out + " := list }}{{ range " + item + " := " + expr + " }}")
		value := respell(b, p.Items, item, n)
		b.WriteString("{{ " + out + " = append " + out + " " + value + " }}{{ end }}")
		return out
	}
	return expr
}

// FirstParagraph is text up to its first blank line, trimmed: the part of a
// vendor description a draft carries.
func FirstParagraph(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if i := strings.Index(text, "\n\n"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}
