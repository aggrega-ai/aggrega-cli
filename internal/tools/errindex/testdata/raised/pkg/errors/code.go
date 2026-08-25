package errors

// The fixture's stand-in for the package that declares the identity. Only its
// import path matters to the scan, which recognises a registration by the
// package it is qualified with.
//
// The short codes below are this fixture module's own and belong to no
// allocation: a fixture tree is a separate module with a separate registry, so
// they sit in a high band no real condition occupies.

type Meta struct {
	ShortCode     string
	Message       string
	DeclaredAhead string
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
