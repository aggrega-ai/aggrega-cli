package errors

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
