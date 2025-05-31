package simulation

type Result struct {
	name     string
	capacity int
	ratio    float64
}

func NewResult(name string, capacity int, ratio float64) Result {
	return Result{
		name:     name,
		capacity: capacity,
		ratio:    ratio,
	}
}

func (r Result) Name() string {
	return r.name
}

func (r Result) Capacity() int {
	return r.capacity
}

func (r Result) Ratio() float64 {
	return r.ratio
}
