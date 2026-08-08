package errors

type Meta struct{ Message string }

type sealed struct {
	code    string
	message string
}

func (s *sealed) Error() string { return s.message }

func Register(code string, meta Meta) *sealed {
	return &sealed{code: code, message: meta.Message}
}
