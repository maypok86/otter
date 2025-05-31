package generator

type Stream[T any] struct {
	stream chan T
}

func newStream[T any](capacity int) Stream[T] {
	return Stream[T]{
		stream: make(chan T, capacity),
	}
}

func (s Stream[T]) Next() (T, bool) {
	v, ok := <-s.stream
	return v, ok
}

func (s Stream[T]) close() {
	close(s.stream)
}

func (s Stream[T]) asSender() chan<- T {
	return s.stream
}
