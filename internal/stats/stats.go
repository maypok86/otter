package stats

type Stats struct {
	hits   *counter
	misses *counter
	sets   *counter
}

func New() *Stats {
	return &Stats{
		hits:   newCounter(),
		misses: newCounter(),
		sets:   newCounter(),
	}
}

func (s *Stats) IncrementHits() {
	if s == nil {
		return
	}

	s.hits.increment()
}

func (s *Stats) Hits() int64 {
	if s == nil {
		return 0
	}

	return s.hits.value()
}

func (s *Stats) IncrementMisses() {
	if s == nil {
		return
	}

	s.misses.increment()
}

func (s *Stats) Misses() int64 {
	if s == nil {
		return 0
	}

	return s.misses.value()
}

func (s *Stats) IncrementSets() {
	if s == nil {
		return
	}

	s.sets.increment()
}

func (s *Stats) Sets() int64 {
	if s == nil {
		return 0
	}

	return s.sets.value()
}

func (s *Stats) Ratio() float64 {
	if s == nil {
		return 0.0
	}

	hits := s.hits.value()
	misses := s.misses.value()
	if hits == 0 && misses == 0 {
		return 0.0
	}
	return float64(hits) / float64(hits+misses)
}
