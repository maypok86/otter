package simulator

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"runtime"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/maypok86/otter/v2/benchmarks/client"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/config"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/policy"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/report"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/report/simulation"
)

func getClients() map[string]client.Client[uint64, uint64] {
	cl := []client.Client[uint64, uint64]{
		&client.Otter[uint64, uint64]{},
		&client.Theine[uint64, uint64]{},
		&client.TinyLFU[uint64, uint64]{},
		&client.ClockPro{},
		&client.Ristretto[uint64, uint64]{},
		&client.LRU[uint64, uint64]{},
		&client.ARC[uint64, uint64]{},
	}

	clients := make(map[string]client.Client[uint64, uint64], len(cl))
	for _, c := range cl {
		clients[c.Name()] = c
	}
	return clients
}

type Simulator struct {
	cfg     config.Config
	results chan simulation.Result
}

func New(cfg config.Config) (Simulator, error) {
	return Simulator{
		cfg: cfg,
	}, nil
}

func (s Simulator) Simulate() error {
	eg, _ := errgroup.WithContext(context.Background())
	if s.cfg.Capacities[0] < 10_000 {
		eg.SetLimit(1)
	} else {
		eg.SetLimit(runtime.NumCPU())
	}
	size := 0
	for i, capacity := range s.cfg.Capacities {
		policies := make([]policyContract, 0, len(s.cfg.Caches))
		clients := getClients()
		for _, cache := range s.cfg.Caches {
			c, ok := clients[cache]
			if !ok {
				return fmt.Errorf("not valid cache name: %s", cache)
			}

			policies = append(policies, policy.NewPolicy(c))
		}
		if i == 0 {
			size = len(s.cfg.Capacities) * len(policies)
			s.results = make(chan simulation.Result, size)
		}

		for _, p := range policies {
			p := p
			capacity := capacity
			eg.Go(func() error {
				return s.simulatePolicy(p, capacity)
			})
		}
	}

	cacheToPriority := make(map[string]int, len(s.cfg.Caches))
	for i, cache := range s.cfg.Caches {
		cacheToPriority[cache] = i
	}

	table := make([][]simulation.Result, len(s.cfg.Caches))
	for i := 0; i < len(table); i++ {
		table[i] = make([]simulation.Result, 0, len(s.cfg.Capacities))
	}

	i := 0
	for r := range s.results {
		i++
		prior := cacheToPriority[r.Name()]
		table[prior] = append(table[prior], r)
		if i == size {
			close(s.results)
			break
		}
	}

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("simulate: %w", err)
	}

	log.Println("All simulations are complete")

	for _, results := range table {
		slices.SortFunc(results, func(a, b simulation.Result) int {
			return cmp.Compare(a.Capacity(), b.Capacity())
		})
	}

	reporter := report.NewReporter(s.cfg.Name, table)
	if err := reporter.Report(); err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	return nil
}

func (s Simulator) simulatePolicy(p policyContract, unsignedCapacity uint) error {
	//nolint:gosec // there will never be an overflow
	capacity := int(unsignedCapacity)
	p.Init(capacity)

	traceGenerator, err := newGenerator(s.cfg)
	if err != nil {
		return err
	}

	stream := traceGenerator.Generate()
	for {
		e, ok := stream.Next()
		if !ok {
			break
		}

		p.Record(e)
	}

	r := simulation.NewResult(p.Name(), capacity, p.Ratio())
	s.results <- r

	p.Close()

	log.Printf(
		"Simulation for cache %s at capacity %d completed with hit ratio %0.2f%%\n",
		r.Name(),
		r.Capacity(),
		r.Ratio(),
	)

	return nil
}
