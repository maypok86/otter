// Package linearizability checks whether the caches of the benchmarks are linearizable: whether
// some order of the operations of a concurrent history, each taking effect at one point between
// its call and its return, explains every result. It records histories of random Get, Set,
// Delete and Load operations on a few keys and checks them with porcupine against a sequential
// model of a cache, key by key.
//
// A Load is a get-or-load: it returns the key's value, or loads one, stores it and returns it
// if the key is absent. Written values are positive, loaded values negative, and both are unique,
// so the model tells a loaded value from a written one. No cache evicts or expires an entry.
//
// The tests report what they find for each cache and fail only for otter.
//
//	LINCHECK_DURATION=15s go test -race -v .
package linearizability

import (
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anishathalye/porcupine"
)

type opKind uint8

const (
	opGet opKind = iota
	opSet
	opDelete
	opLoad
)

type input struct {
	op    opKind
	key   int
	value int // the value written, for Set
}

type output struct {
	value  int
	ok     bool // Get: the key was found
	failed bool // Load: the loader failed
}

// model is a map of int keys, partitioned by key. A key's state is its value, 0 if absent.
var model = porcupine.Model{
	Partition: func(history []porcupine.Operation) [][]porcupine.Operation {
		byKey := map[int][]porcupine.Operation{}
		for _, op := range history {
			k := op.Input.(input).key
			byKey[k] = append(byKey[k], op)
		}
		partitions := make([][]porcupine.Operation, 0, len(byKey))
		for _, ops := range byKey {
			partitions = append(partitions, ops)
		}
		return partitions
	},
	Init: func() any { return 0 },
	Step: func(st, in, out any) (bool, any) {
		s, i, o := st.(int), in.(input), out.(output)
		switch i.op {
		case opGet:
			if s != 0 {
				return o.ok && o.value == s, s
			}
			return !o.ok, s
		case opSet:
			return true, i.value
		case opDelete:
			return true, 0
		case opLoad:
			if s != 0 {
				return !o.failed && o.value == s, s
			}
			if o.failed {
				return true, s
			}
			return o.value < 0, o.value
		}
		return false, s
	},
	DescribeOperation: func(in, out any) string {
		i, o := in.(input), out.(output)
		switch i.op {
		case opSet:
			return fmt.Sprintf("Set(%d, %d)", i.key, i.value)
		case opDelete:
			return fmt.Sprintf("Delete(%d)", i.key)
		case opLoad:
			if o.failed {
				return fmt.Sprintf("Load(%d) -> error", i.key)
			}
			return fmt.Sprintf("Load(%d) -> %d", i.key, o.value)
		default:
			if !o.ok {
				return fmt.Sprintf("Get(%d) -> miss", i.key)
			}
			return fmt.Sprintf("Get(%d) -> %d", i.key, o.value)
		}
	},
	DescribeState: func(st any) string {
		if st.(int) == 0 {
			return "absent"
		}
		return strconv.Itoa(st.(int))
	},
}

// record runs clients goroutines on c, each with ops random operations on keys keys, and
// returns their history. Loads are left out if withLoads is false.
func record(c *cache, keys, clients, ops int, withLoads bool, seed uint64) []porcupine.Operation {
	var clock, written, loaded atomic.Int64
	var kinds []opKind
	if c.get != nil {
		kinds = append(kinds, opGet, opGet)
	}
	kinds = append(kinds, opSet, opDelete)
	if withLoads {
		kinds = append(kinds, opLoad, opLoad, opLoad)
	}

	histories := make([][]porcupine.Operation, clients)
	var start, wg sync.WaitGroup
	start.Add(1)
	wg.Add(clients)
	for client := range clients {
		go func() {
			defer wg.Done()
			rnd := rand.New(rand.NewPCG(seed, uint64(client)))
			// A loader may run on another goroutine than the load that passed it, so it has a
			// generator of its own.
			var mu sync.Mutex
			loaderRnd := rand.New(rand.NewPCG(seed, uint64(client)+1<<32))
			load := func() (int, error) {
				mu.Lock()
				fails, slow, delay := loaderRnd.IntN(10) == 0, loaderRnd.IntN(4) == 0, loaderRnd.IntN(50)
				mu.Unlock()
				if slow {
					// A load in flight for a while leaves room for writes to race with it.
					time.Sleep(time.Duration(delay) * time.Microsecond)
				}
				if fails {
					return 0, errLoad
				}
				return int(-loaded.Add(1)), nil
			}

			start.Wait()
			h := make([]porcupine.Operation, 0, ops)
			for range ops {
				in := input{op: kinds[rnd.IntN(len(kinds))], key: rnd.IntN(keys)}
				k := strconv.Itoa(in.key)
				var out output
				call := clock.Add(1)
				switch in.op {
				case opGet:
					out.value, out.ok = c.get(k)
				case opSet:
					in.value = int(written.Add(1))
					c.set(k, in.value)
				case opDelete:
					c.del(k)
				case opLoad:
					v, err := c.load(k, load)
					out = output{value: v, failed: err != nil}
				}
				h = append(h, porcupine.Operation{ClientId: client, Input: in, Output: out, Call: call, Return: clock.Add(1)})
			}
			histories[client] = h
		}()
	}
	start.Done()
	wg.Wait()

	var all []porcupine.Operation
	for _, h := range histories {
		all = append(all, h...)
	}
	return all
}

func TestLinearizability(t *testing.T) {
	duration := 2 * time.Second
	if d := os.Getenv("LINCHECK_DURATION"); d != "" {
		var err error
		if duration, err = time.ParseDuration(d); err != nil {
			t.Fatalf("LINCHECK_DURATION: %v", err)
		}
	}

	for _, withLoads := range []bool{false, true} {
		ops := "Get-Set-Delete"
		if withLoads {
			ops = "Load"
		}
		t.Run(ops, func(t *testing.T) {
			for _, lib := range libraries {
				// A library without a plain read has nothing to check in histories without loads.
				if withLoads && !canLoad(lib) || !withLoads && !canGet(lib) {
					continue
				}
				t.Run(lib.name, func(t *testing.T) {
					checkLibrary(t, lib, withLoads, duration)
				})
			}
		})
	}
}

func canLoad(lib library) bool {
	c := lib.new()
	defer c.close()
	return c.load != nil
}

func canGet(lib library) bool {
	c := lib.new()
	defer c.close()
	return c.get != nil
}

func checkLibrary(t *testing.T, lib library, withLoads bool, duration time.Duration) {
	const (
		keys    = 2
		clients = 8
		ops     = 200
	)
	seed := uint64(time.Now().UnixNano())
	deadline := time.Now().Add(duration)
	histories, unknown := 0, 0
	for ; time.Now().Before(deadline); seed++ {
		c := lib.new()
		history := record(c, keys, clients, ops, withLoads, seed)
		c.close()
		histories++
		res, info := porcupine.CheckOperationsVerbose(model, history, 10*time.Second)
		switch res {
		case porcupine.Unknown:
			unknown++
		case porcupine.Illegal:
			report(t, lib.name, "not linearizable: history %d (seed %d)\n%s", histories, seed, explain(history, info))
			return
		}
	}
	t.Logf("linearizable: %d histories of %d operations, %d timed out", histories, clients*ops, unknown)
}

// report logs a violation, which fails the test only for otter: the other libraries are
// measured, not tested.
func report(t *testing.T, name, format string, args ...any) {
	t.Helper()
	if name == "otter" {
		t.Errorf(format, args...)
		return
	}
	t.Logf(format, args...)
}

// explain describes why a history is not linearizable: for each key that porcupine could not
// order, the end of the longest order it found and the operations that cannot follow it.
func explain(history []porcupine.Operation, info porcupine.LinearizationInfo) string {
	describe := func(op porcupine.Operation) string {
		return fmt.Sprintf("client %d [%d, %d] %s", op.ClientId, op.Call, op.Return, model.DescribeOperation(op.Input, op.Output))
	}
	byKey := map[int][]porcupine.Operation{}
	for _, op := range history {
		k := op.Input.(input).key
		byKey[k] = append(byKey[k], op)
	}

	var b strings.Builder
	for _, orders := range info.PartialLinearizationsOperations() {
		var longest []porcupine.Operation
		for _, order := range orders {
			if len(order) > len(longest) {
				longest = order
			}
		}
		if len(longest) == 0 {
			continue
		}
		key := longest[0].Input.(input).key
		ops := byKey[key]
		if len(longest) == len(ops) {
			continue
		}
		sort.Slice(ops, func(i, j int) bool { return ops[i].Call < ops[j].Call })
		ordered := map[int64]bool{}
		for _, op := range longest {
			ordered[op.Call] = true
		}

		fmt.Fprintf(&b, "key %d: %d of %d operations ordered; the order ends with\n", key, len(longest), len(ops))
		for _, op := range longest[max(0, len(longest)-6):] {
			fmt.Fprintf(&b, "  %s\n", describe(op))
		}
		// The candidates for the next step are the unordered operations that start before the
		// first of them returns.
		firstReturn := int64(1 << 62)
		for _, op := range ops {
			if !ordered[op.Call] {
				firstReturn = min(firstReturn, op.Return)
			}
		}
		b.WriteString("and none of these can follow it:\n")
		for _, op := range ops {
			if !ordered[op.Call] && op.Call < firstReturn {
				fmt.Fprintf(&b, "  %s\n", describe(op))
			}
		}
	}
	return b.String()
}
