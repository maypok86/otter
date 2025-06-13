package product

import fifo "github.com/scalalang2/golang-fifo/v2"

type S3FIFO[K comparable, V any] struct {
	client fifo.Cache[K, V]
}

func (c *S3FIFO[K, V]) Init(capacity int) {
	client := fifo.NewS3FIFO[K, V](capacity)
	c.client = client
}

func (c *S3FIFO[K, V]) Get(key K) (V, bool) {
	return c.client.Get(key)
}

func (c *S3FIFO[K, V]) Set(key K, value V) {
	c.client.Set(key, value)
}

func (c *S3FIFO[K, V]) Name() string {
	return "s3-fifo"
}

func (c *S3FIFO[K, V]) Close() {
	c.client.Clean()
	c.client = nil
}
