package node

const (
	deletedMask   = uint32(1 << 31)
	ghostMask     = uint32(1 << 30)
	mainMask      = uint32(1 << 29)
	smallMask     = uint32(1 << 28)
	frequencyMask = uint32(4 - 1)
)

type Meta uint32

func DefaultMeta() Meta {
	return Meta(0)
}

func (m Meta) IsDeleted() bool {
	return uint32(m)&deletedMask == deletedMask
}

func (m Meta) MarkDeleted() Meta {
	return Meta(uint32(m) | deletedMask)
}

func (m Meta) IsGhost() bool {
	return uint32(m)&ghostMask == ghostMask
}

func (m Meta) MarkGhost() Meta {
	return Meta(uint32(m) | ghostMask)
}

func (m Meta) UnmarkGhost() Meta {
	return Meta(uint32(m) &^ ghostMask)
}

func (m Meta) IsMain() bool {
	return uint32(m)&mainMask == mainMask
}

func (m Meta) MarkMain() Meta {
	return Meta(uint32(m) | mainMask)
}

func (m Meta) UnmarkMain() Meta {
	return Meta(uint32(m) &^ mainMask)
}

func (m Meta) IsSmall() bool {
	return uint32(m)&smallMask == smallMask
}

func (m Meta) MarkSmall() Meta {
	return Meta(uint32(m) | smallMask)
}

func (m Meta) UnmarkSmall() Meta {
	return Meta(uint32(m) &^ smallMask)
}

func (m Meta) IncrementFrequency() Meta {
	// TODO: speed up
	frequency := uint32(m) & frequencyMask
	frequency = minUint32(frequency+1, frequencyMask)
	return Meta((uint32(m) >> 2 << 2) | frequency)
}

func (m Meta) DecrementFrequency() Meta {
	frequency := uint32(m) & frequencyMask
	frequency--
	return Meta((uint32(m) >> 2 << 2) | frequency)
}

func (m Meta) ResetFrequency() Meta {
	return m >> 2 << 2
}

func (m Meta) GetFrequency() uint32 {
	return uint32(m) & frequencyMask
}

func minUint32(a, b uint32) uint32 {
	if a < b {
		return a
	}

	return b
}
