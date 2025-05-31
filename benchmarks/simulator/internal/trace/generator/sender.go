package generator

type sender[T any] struct {
	s      Stream[T]
	events uint
	limit  *uint
}

func newSender[T any](s Stream[T], limit *uint) *sender[T] {
	return &sender[T]{
		s:     s,
		limit: limit,
	}
}

func (s *sender[T]) Send(event T) bool {
	if s.limit != nil && s.events >= *s.limit {
		return true
	}

	s.s.asSender() <- event

	s.events++
	return false
}
