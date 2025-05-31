package event

type AccessEvent struct {
	key uint64
}

func NewAccessEvent(key uint64) AccessEvent {
	return AccessEvent{
		key: key,
	}
}

func (ae AccessEvent) Key() uint64 {
	return ae.key
}
