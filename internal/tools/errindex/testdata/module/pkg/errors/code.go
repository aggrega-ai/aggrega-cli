package errors

// The fixture's stand-in for the package that declares the identity. Only its
// import path matters to the scan, which recognises a registration by the
// package it is qualified with.

type Meta struct{ Message string }

type sealed struct {
	code    string
	message string
}

func (s *sealed) Error() string { return s.message }

func Register(code string, meta Meta) *sealed {
	return &sealed{code: code, message: meta.Message}
}
