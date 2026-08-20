package errors

// The fixture's stand-in for the package that declares the identity. Its Meta
// carries the short-code field the real one carries, so the registration below
// is a registration that OMITS the field rather than one that could not express
// it — which is the distinction this fixture exists to make.

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
