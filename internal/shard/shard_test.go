package shard

import (
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestShard_PaddedBucketSize(t *testing.T) {
	size := unsafe.Sizeof(paddedBucket{})
	if size != 64 {
		t.Fatalf("size of 64B (one cache line) is expected, got: %d", size)
	}
}

func TestShard_EmptyStringKey(t *testing.T) {
	s := New[string, string]()
	s.Set("", "foobar")
	v, e, ok := s.Get("")
	if !ok {
		t.Fatal("value was expected")
	}
	if v != "foobar" {
		t.Fatalf("value does not match: %v", v)
	}
	if e != nil {
		t.Fatal("evicted node wasn't expected")
	}
}

func TestShard_SetNilValue(t *testing.T) {
	m := New[string, *struct{}]()
	m.Set("foo", nil)
	v, e, ok := m.Get("foo")
	if !ok {
		t.Fatal("nil value was expected")
	}
	if v != nil {
		t.Fatalf("value was not nil: %v", v)
	}
	if e != nil {
		t.Fatal("evicted node wasn't expected")
	}
}

func TestShard_Set(t *testing.T) {
	const numberOfNodes = 128
	s := New[string, int]()
	for i := 0; i < numberOfNodes; i++ {
		s.Set(strconv.Itoa(i), i)
	}
	for i := 0; i < numberOfNodes; i++ {
		v, e, ok := s.Get(strconv.Itoa(i))
		if !ok {
			t.Fatalf("value not found for %d", i)
		}
		if v != i {
			t.Fatalf("values do not match for %d: %v", i, v)
		}
		if e != nil {
			t.Fatal("evicted node wasn't expected")
		}
	}
}

func TestShard_SetWithCollisions(t *testing.T) {
	s := New[int, int](WithHasher[int](func(i int) uint64 {
		// we intentionally use an awful hash function here to make sure
		// that the shard evict nodes.
		return 42
	}))
	ins, ev := s.Set(2, 2)
	if ins == nil {
		t.Fatal("inserted must be non null")
	}
	if ev != nil {
		t.Fatal("evicted must be null")
	}
	v, e, ok := s.Get(2)
	if !ok {
		t.Fatal("value not found for 2")
	}
	if v != 2 {
		t.Fatalf("values do not match for 2: %v", v)
	}
	if e != nil {
		t.Fatal("evicted node wasn't expected")
	}
	ins, ev = s.Set(1, 1)
	if ins == nil {
		t.Fatal("inserted must be non null")
	}
	if ev == nil || ev.Key == 0 {
		t.Fatal("evicted must be with key zero")
	}
	_, _, ok = s.Get(2)
	if ok {
		t.Fatal("value found for 2")
	}
}

func TestShard_SetThenDelete(t *testing.T) {
	const numberOfNodes = 1000
	s := New[string, int]()
	for i := 0; i < numberOfNodes; i++ {
		s.Set(strconv.Itoa(i), i)
	}
	for i := 0; i < numberOfNodes; i++ {
		s.Delete(strconv.Itoa(i))
		if _, _, ok := s.Get(strconv.Itoa(i)); ok {
			t.Fatalf("value was not expected for %d", i)
		}
	}
}

func TestShard_Size(t *testing.T) {
	const numberOfNodes = 1000
	s := New[string, int]()
	size := s.Size()
	if size != 0 {
		t.Fatalf("zero size expected: %d", size)
	}
	expectedSize := 0
	for i := 0; i < numberOfNodes; i++ {
		s.Set(strconv.Itoa(i), i)
		expectedSize++
		size := s.Size()
		if size != expectedSize {
			t.Fatalf("size of %d was expected, got: %d", expectedSize, size)
		}
	}
	for i := 0; i < numberOfNodes; i++ {
		s.Delete(strconv.Itoa(i))
		expectedSize--
		size := s.Size()
		if size != expectedSize {
			t.Fatalf("size of %d was expected, got: %d", expectedSize, size)
		}
	}
}

func TestShard_Clear(t *testing.T) {
	const numberOfNodes = 1000
	m := New[string, int]()
	for i := 0; i < numberOfNodes; i++ {
		m.Set(strconv.Itoa(i), i)
	}
	size := m.Size()
	if size != numberOfNodes {
		t.Fatalf("size of %d was expected, got: %d", numberOfNodes, size)
	}
	m.Clear()
	size = m.Size()
	if size != 0 {
		t.Fatalf("zero size was expected, got: %d", size)
	}
}

func parallelSeqSetter(t *testing.T, s *Shard[string, int], storers, iterations, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	for i := 0; i < iterations; i++ {
		for j := 0; j < nodes; j++ {
			if storers == 0 || j%storers == 0 {
				s.Set(strconv.Itoa(j), j)
				v, _, ok := s.Get(strconv.Itoa(j))
				if !ok {
					t.Errorf("value was not found for %d", j)
					break
				}
				if v != j {
					t.Errorf("value was not expected for %d: %d", j, v)
					break
				}
			}
		}
	}
	wg.Done()
}

func TestShard_ParallelSets(t *testing.T) {
	const storers = 4
	const iterations = 10_000
	const nodes = 100
	s := New[string, int]()

	wg := &sync.WaitGroup{}
	wg.Add(storers)
	for i := 0; i < storers; i++ {
		go parallelSeqSetter(t, s, i, iterations, nodes, wg)
	}
	wg.Wait()

	for i := 0; i < nodes; i++ {
		v, _, ok := s.Get(strconv.Itoa(i))
		if !ok {
			t.Fatalf("value not found for %d", i)
		}
		if v != i {
			t.Fatalf("values do not match for %d: %v", i, v)
		}
	}
}

func parallelRandSetter(t *testing.T, s *Shard[string, int], iteratinos, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < iteratinos; i++ {
		j := r.Intn(nodes)
		if v, _ := s.Set(strconv.Itoa(j), j); v.Value != j {
			t.Errorf("value was not expected for %d: %d", j, v.Value)
		}
	}
	wg.Done()
}

func parallelRandDeleter(t *testing.T, s *Shard[string, int], iterations, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < iterations; i++ {
		j := r.Intn(nodes)
		if v := s.Delete(strconv.Itoa(j)); v != nil && v.Value != j {
			t.Errorf("value was not expected for %d: %d", j, v.Value)
		}
	}
	wg.Done()
}

func parallelGetter(t *testing.T, s *Shard[string, int], iterations, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	for i := 0; i < iterations; i++ {
		for j := 0; j < nodes; j++ {
			if v, _, ok := s.Get(strconv.Itoa(j)); ok && v != j {
				t.Errorf("value was not expected for %d: %d", j, v)
			}
		}
	}
	wg.Done()
}

func TestShard_ParallelGet(t *testing.T) {
	const iterations = 100_000
	const nodes = 100
	s := New[string, int]()

	wg := &sync.WaitGroup{}
	wg.Add(3)
	go parallelRandSetter(t, s, iterations, nodes, wg)
	go parallelRandDeleter(t, s, iterations, nodes, wg)
	go parallelGetter(t, s, iterations, nodes, wg)

	wg.Wait()
}

func TestShard_ParallelSetsAndDeletes(t *testing.T) {
	const workers = 2
	const iterations = 100_000
	const nodes = 1000
	m := New[string, int]()
	wg := &sync.WaitGroup{}
	wg.Add(2 * workers)
	for i := 0; i < workers; i++ {
		go parallelRandSetter(t, m, iterations, nodes, wg)
		go parallelRandDeleter(t, m, iterations, nodes, wg)
	}

	wg.Wait()
}
