package hashtable

import (
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/maypok86/otter/internal/node"
	"github.com/maypok86/otter/internal/xruntime"
)

func newNode[K comparable, V any](key K, value V) *node.Node[K, V] {
	return node.New[K, V](key, value, 0, 1)
}

func TestMap_PaddedBucketSize(t *testing.T) {
	size := unsafe.Sizeof(paddedBucket{})
	if size != xruntime.CacheLineSize {
		t.Fatalf("size of 128B (two cache lines) is expected, got: %d", size)
	}
}

func TestMap_EmptyStringKey(t *testing.T) {
	m := New[string, string]()
	m.Set(newNode[string, string]("", "foobar"))
	n, ok := m.Get("")
	if !ok {
		t.Fatal("value was expected")
	}
	if n.Value() != "foobar" {
		t.Fatalf("value does not match: %v", n.Value())
	}
}

func TestMap_SetNilValue(t *testing.T) {
	m := New[string, *struct{}]()
	m.Set(newNode[string, *struct{}]("foo", nil))
	n, ok := m.Get("foo")
	if !ok {
		t.Fatal("nil value was expected")
	}
	if n.Value() != nil {
		t.Fatalf("value was not nil: %v", n.Value())
	}
}

func TestMap_Set(t *testing.T) {
	const numberOfNodes = 128
	m := New[string, int]()
	for i := 0; i < numberOfNodes; i++ {
		m.Set(newNode[string, int](strconv.Itoa(i), i))
	}
	for i := 0; i < numberOfNodes; i++ {
		n, ok := m.Get(strconv.Itoa(i))
		if !ok {
			t.Fatalf("value not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n.Value())
		}
	}
}

func TestMap_SetWithCollisions(t *testing.T) {
	const numEntries = 1000
	m := New[int, int](WithHasher[int](func(i int) uint64 {
		// We intentionally use an awful hash function here to make sure
		// that the map copes with key collisions.
		return 42
	}))
	for i := 0; i < numEntries; i++ {
		m.Set(newNode(i, i))
	}
	for i := 0; i < numEntries; i++ {
		v, ok := m.Get(i)
		if !ok {
			t.Fatalf("value not found for %d", i)
		}
		if v.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, v)
		}
	}
}

func TestMap_SetThenDelete(t *testing.T) {
	const numberOfNodes = 1000
	m := New[string, int]()
	for i := 0; i < numberOfNodes; i++ {
		m.Set(newNode[string, int](strconv.Itoa(i), i))
	}
	for i := 0; i < numberOfNodes; i++ {
		m.Delete(strconv.Itoa(i))
		if _, ok := m.Get(strconv.Itoa(i)); ok {
			t.Fatalf("value was not expected for %d", i)
		}
	}
}

func TestMap_Size(t *testing.T) {
	const numberOfNodes = 1000
	m := New[string, int]()
	size := m.Size()
	if size != 0 {
		t.Fatalf("zero size expected: %d", size)
	}
	expectedSize := 0
	for i := 0; i < numberOfNodes; i++ {
		m.Set(newNode[string, int](strconv.Itoa(i), i))
		expectedSize++
		size := m.Size()
		if size != expectedSize {
			t.Fatalf("size of %d was expected, got: %d", expectedSize, size)
		}
	}
	for i := 0; i < numberOfNodes; i++ {
		m.Delete(strconv.Itoa(i))
		expectedSize--
		size := m.Size()
		if size != expectedSize {
			t.Fatalf("size of %d was expected, got: %d", expectedSize, size)
		}
	}
}

func TestMap_Clear(t *testing.T) {
	const numberOfNodes = 1000
	m := New[string, int]()
	for i := 0; i < numberOfNodes; i++ {
		m.Set(newNode[string, int](strconv.Itoa(i), i))
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

func parallelSeqSetter(t *testing.T, m *Map[string, int], storers, iterations, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	for i := 0; i < iterations; i++ {
		for j := 0; j < nodes; j++ {
			if storers == 0 || j%storers == 0 {
				m.Set(newNode[string, int](strconv.Itoa(j), j))
				n, ok := m.Get(strconv.Itoa(j))
				if !ok {
					t.Errorf("value was not found for %d", j)
					break
				}
				if n.Value() != j {
					t.Errorf("value was not expected for %d: %d", j, n.Value())
					break
				}
			}
		}
	}
	wg.Done()
}

func TestMap_ParallelSets(t *testing.T) {
	const storers = 4
	const iterations = 10_000
	const nodes = 100
	m := New[string, int]()

	wg := &sync.WaitGroup{}
	wg.Add(storers)
	for i := 0; i < storers; i++ {
		go parallelSeqSetter(t, m, i, iterations, nodes, wg)
	}
	wg.Wait()

	for i := 0; i < nodes; i++ {
		n, ok := m.Get(strconv.Itoa(i))
		if !ok {
			t.Fatalf("value not found for %d", i)
		}
		if n.Value() != i {
			t.Fatalf("values do not match for %d: %v", i, n.Value())
		}
	}
}

func parallelRandSetter(t *testing.T, m *Map[string, int], iteratinos, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < iteratinos; i++ {
		j := r.Intn(nodes)
		m.Set(newNode[string, int](strconv.Itoa(j), j))
	}
	wg.Done()
}

func parallelRandDeleter(t *testing.T, m *Map[string, int], iterations, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < iterations; i++ {
		j := r.Intn(nodes)
		if v := m.Delete(strconv.Itoa(j)); v != nil && v.Value() != j {
			t.Errorf("value was not expected for %d: %d", j, v.Value())
		}
	}
	wg.Done()
}

func parallelGetter(t *testing.T, m *Map[string, int], iterations, nodes int, wg *sync.WaitGroup) {
	t.Helper()

	for i := 0; i < iterations; i++ {
		for j := 0; j < nodes; j++ {
			if n, ok := m.Get(strconv.Itoa(j)); ok && n.Value() != j {
				t.Errorf("value was not expected for %d: %d", j, n.Value())
			}
		}
	}
	wg.Done()
}

func TestMap_ParallelGet(t *testing.T) {
	const iterations = 100_000
	const nodes = 100
	m := New[string, int]()

	wg := &sync.WaitGroup{}
	wg.Add(3)
	go parallelRandSetter(t, m, iterations, nodes, wg)
	go parallelRandDeleter(t, m, iterations, nodes, wg)
	go parallelGetter(t, m, iterations, nodes, wg)

	wg.Wait()
}

func TestMap_ParallelSetsAndDeletes(t *testing.T) {
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
