package web

import "testing"

func TestDurationTextKeepsSubMinuteAndRemainder(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		want    string
	}{
		{0, "—"},
		{45, "45s"},
		{60, "1m"},
		{90, "1m 30s"},
		{300, "5m"},
		{900, "15m"},
	} {
		if got := durationText(tc.seconds); got != tc.want {
			t.Errorf("durationText(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestSafeTagsDropsEmptyAndPromptLikeValues(t *testing.T) {
	got := safeTags([]string{"debugging", "", "  ", "multi-file", "a whole sentence that is far too long to be a tag", "line\nbreak"})
	want := []string{"debugging", "multi-file"}
	if len(got) != len(want) {
		t.Fatalf("safeTags = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("safeTags[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTokensTextKeepsSmallCountsExact(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "—"},
		{500, "500"},
		{12_300, "12k"},
		{1_200_000, "1.2M"},
	} {
		if got := tokensText(tc.n); got != tc.want {
			t.Errorf("tokensText(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
