package history_test

import (
	"testing"

	"mbl/ocbench/internal/history"
)

func TestCacheHitRate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		metrics map[string]float64
		want    float64
		ok      bool
	}{
		{
			name:    "three quarters of the prompt came from cache",
			metrics: map[string]float64{"tokens_cache_read": 750, "tokens_input": 250},
			want:    0.75, ok: true,
		},
		{
			name:    "nothing cached",
			metrics: map[string]float64{"tokens_cache_read": 0, "tokens_input": 1000},
			want:    0, ok: true,
		},
		{
			name:    "everything cached",
			metrics: map[string]float64{"tokens_cache_read": 1000, "tokens_input": 0},
			want:    1, ok: true,
		},
		{
			name:    "output and reasoning do not enter the denominator",
			metrics: map[string]float64{"tokens_cache_read": 500, "tokens_input": 500, "tokens_output": 900, "tokens_reasoning": 400},
			want:    0.5, ok: true,
		},
		{
			// A run that used no prompt tokens has no hit rate. Reporting 0
			// would read as a total cache miss.
			name:    "no prompt tokens at all",
			metrics: map[string]float64{"tokens_output": 100},
			ok:      false,
		},
		{name: "no metrics", metrics: nil, ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := history.CacheHitRate(tc.metrics)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if got < tc.want-1e-9 || got > tc.want+1e-9 {
				t.Errorf("rate = %v, want %v", got, tc.want)
			}
		})
	}
}
