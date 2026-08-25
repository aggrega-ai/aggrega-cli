package errors

// The fixture's stand-in for the package that declares the identity. Only its
// import path matters to the scan, which recognises a registration by the
// package it is qualified with.
//
// The short codes the fixtures below declare are this fixture module's own and
// belong to no allocation: a fixture tree is a separate module with a separate
// registry, so they sit in a high band that no real condition occupies and
// cannot be mistaken for a number read off the published mapping.

type Meta struct {
	ShortCode string
	Message   string
}

type sealed struct {
	code      string
	shortCode string
	message   string
}

func (s *sealed) Error() string { return s.message }

func Register(code string, meta Meta) *sealed {
	return &sealed{code: code, shortCode: meta.ShortCode, message: meta.Message}
}

// The fixture's stand-in for the retirement record: the declaration the scan
// reads to learn which numbers are spent. It carries both spellings a literal
// element can be written in — positional and by field name — because the scan
// reads either, and a fixture exercising only one would leave the other proven
// by nothing.
type retirement struct {
	shortCode string
	note      string
}

var retiredShortCodes = []retirement{
	{"E9901", "It named a fixture condition that no longer exists."},
	{shortCode: "E9902", note: "It named a second fixture condition, recorded by field name."},
}
