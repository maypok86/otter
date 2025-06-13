package product

import (
	"strconv"
	"time"

	"github.com/viccon/sturdyc"
)

type Sturdyc struct {
	client *sturdyc.Client[uint64]
}

func (c *Sturdyc) Init(capacity int) {
	c.client = sturdyc.New[uint64](capacity, 10, time.Hour, 10)
}

func (c *Sturdyc) Name() string {
	return "sturdyc"
}

func (c *Sturdyc) Get(key uint64) (uint64, bool) {
	return c.client.Get(strconv.FormatUint(key, 10))
}

func (c *Sturdyc) Set(key uint64, value uint64) {
	c.client.Set(strconv.FormatUint(key, 10), value)
}

func (c *Sturdyc) Close() {
	c.client = nil
}
