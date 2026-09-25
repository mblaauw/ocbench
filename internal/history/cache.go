package history

// CacheHitRate is the share of a run's prompt tokens that were served from the
// provider's cache: cache reads over cache reads plus fresh input.
//
// It is the closest thing to a single number for prompt stability and context
// discipline, which are the config choices a user actually tunes. Output and
// reasoning tokens are excluded: they are not part of the prompt.
//
// ok is false when the run used no prompt tokens at all. Reporting a rate of
// zero there would read as a total cache miss rather than as no evidence.
func CacheHitRate(metrics map[string]float64) (float64, bool) {
	cached := metrics["tokens_cache_read"]
	fresh := metrics["tokens_input"]
	total := cached + fresh
	if total <= 0 {
		return 0, false
	}
	return cached / total, true
}
