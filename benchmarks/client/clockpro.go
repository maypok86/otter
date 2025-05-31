package client

import (
	"strconv"

	"github.com/dgryski/go-clockpro"
)

type ClockPro struct {
	client *clockpro.Cache
}

func (c *ClockPro) Init(capacity int) {
	client := clockpro.New(capacity)
	c.client = client
}

func (c *ClockPro) Name() string {
	return "clockpro"
}

func (c *ClockPro) Get(key uint64) (uint64, bool) {
	v := c.client.Get(strconv.FormatUint(key, 10))
	if v == nil {
		return 0, false
	}

	return v.(uint64), true
}

func (c *ClockPro) Set(key, value uint64) {
	c.client.Set(strconv.FormatUint(key, 10), value)
}

func (c *ClockPro) Close() {
	c.client = nil
}
