package prometheus

import (
	"github.com/maypok86/otter"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"
	"time"
)

func TestCollector_Describe(t *testing.T) {
	cache, err := otter.MustBuilder[string, int](1024).
		WithTTL(time.Hour).
		CollectStats().
		Build()
	if err != nil {
		t.Fatal(err)
	}
	collector := NewCollector("test", "cache", cache)
	descsCh := make(chan *prometheus.Desc, 5)

	collector.Describe(descsCh)

	close(descsCh)

	descs := testutil.CollectAndCount(
		collector,
		"test_cache_hits",
		"test_cache_misses",
		"test_cache_rejected_sets",
		"test_cache_evicted_count",
		"test_cache_evicted_cost",
	)
	if descs != 5 {
		t.Errorf("unexpected number of descs: %d", descs)
	}
}

func TestCollector_Collect(t *testing.T) {
	cache, err := otter.MustBuilder[string, int](1024).
		WithTTL(time.Hour).
		CollectStats().
		Build()
	if err != nil {
		t.Fatal(err)
	}
	collector := NewCollector("test", "cache", cache)
	metricsCh := make(chan prometheus.Metric, 5)

	collector.Collect(metricsCh)

	close(metricsCh)

	metrics := testutil.CollectAndCount(
		collector,
		"test_cache_hits",
		"test_cache_misses",
		"test_cache_rejected_sets",
		"test_cache_evicted_count",
		"test_cache_evicted_cost",
	)
	if metrics != 5 {
		t.Errorf("unexpected number of metrics: %d", metrics)
	}
}
