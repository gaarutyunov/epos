package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func writeCounts(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "counts.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// 4.2: `none` is the default and a first-class configuration. A nil Stats,
// handled at one call site — the choice made once, so there is no second answer
// for a zero to get in through.
func TestNoneIsANilStats(t *testing.T) {
	stats, err := StatsFor(SourceNone, "", nil)
	require.NoError(t, err)
	assert.Nil(t, stats)

	empty, err := StatsFor("", "", nil)
	require.NoError(t, err)
	assert.Nil(t, empty)

	assert.Nil(t, WithCache(nil, time.Second, time.Second))
	assert.Nil(t, StatsOrNil(t.Context(), nil, nil))
}

// D4e: the repository set is fixed when the source is built, so every source
// answers the same question. A file naming a repository the catalog does not
// list would put a row on a page with nothing to attach it to.
func TestTheFileSourceIsScopedToTheIndex(t *testing.T) {
	path := writeCounts(t, `{"captured_at":"2026-08-04T12:00:00Z","rows":{
		"demo/agent-skills/pdf":{"verified":10,"unverified":20},
		"somebody/else":{"verified":9999,"unverified":9999}}}`)

	stats := NewFileStats(path, []string{"demo/agent-skills/pdf"})
	counts, err := stats.Pulls(t.Context())
	require.NoError(t, err)

	assert.Equal(t, map[string]Pulls{"demo/agent-skills/pdf": {Verified: 10, Unverified: 20}},
		counts.Rows)
	assert.Equal(t, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC), counts.CapturedAt.UTC())
}

// 4.4: a malformed document is rejected whole, with the filename in the error.
// Half-read into partial counts, the page would look right and be wrong.
func TestAMalformedCountsFileIsRejectedWhole(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"truncated", `{"rows":{"demo/pdf":{"verified":1`},
		{"not JSON at all", "verified: 1\n"},
		{"a mistyped key", `{"rowz":{"demo/pdf":{"verified":1}}}`},
		{"the wrong type", `{"rows":{"demo/pdf":"lots"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeCounts(t, tt.body)
			_, err := NewFileStats(path, nil).Pulls(t.Context())
			require.Error(t, err)
			assert.Contains(t, err.Error(), path, "the error names the file")
		})
	}
}

func TestAMissingCountsFileIsAnError(t *testing.T) {
	_, err := NewFileStats(filepath.Join(t.TempDir(), "absent.json"), nil).Pulls(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read the counts file")
}

// 4.7: a failing source degrades to absent counts. Nothing renders as zero and
// no page fails.
func TestAFailingSourceDegradesToAbsentCounts(t *testing.T) {
	ctrl := gomock.NewController(t)
	stats := NewMockStats(ctrl)
	stats.EXPECT().Pulls(gomock.Any()).Return(Counts{}, errors.New("the store is unreachable"))

	var reported error
	counts := StatsOrNil(t.Context(), stats, func(err error) { reported = err })

	assert.Nil(t, counts, "a failed read is absent counts, never zeroed ones")
	require.Error(t, reported, "and it is logged rather than swallowed")
}

// D4e: a TTL of zero means "query every request", exactly — which is what the
// end-to-end assertion sets it to rather than sleeping.
func TestATTLOfZeroQueriesEveryRequest(t *testing.T) {
	ctrl := gomock.NewController(t)
	stats := NewMockStats(ctrl)
	stats.EXPECT().Pulls(gomock.Any()).Return(Counts{}, nil).Times(3)

	cached := WithCache(stats, 0, time.Second)
	for range 3 {
		_, err := cached.Pulls(t.Context())
		require.NoError(t, err)
	}
}

// And a non-zero TTL reuses the answer, so a burst of page loads is not a burst
// of queries.
func TestANonZeroTTLReusesTheAnswer(t *testing.T) {
	ctrl := gomock.NewController(t)
	stats := NewMockStats(ctrl)
	stats.EXPECT().Pulls(gomock.Any()).Return(Counts{}, nil).Times(1)

	cached := WithCache(stats, time.Minute, time.Second)
	for range 5 {
		_, err := cached.Pulls(t.Context())
		require.NoError(t, err)
	}
}

// The bound that keeps a slow store off the relay: the catalog shares a process
// with /v2/, so a source that hangs must cost the numbers and nothing else.
func TestASlowSourceIsBounded(t *testing.T) {
	ctrl := gomock.NewController(t)
	stats := NewMockStats(ctrl)
	stats.EXPECT().Pulls(gomock.Any()).DoAndReturn(func(ctx context.Context) (Counts, error) {
		<-ctx.Done()
		return Counts{}, ctx.Err()
	})

	start := time.Now()
	_, err := WithCache(stats, 0, 50*time.Millisecond).Pulls(t.Context())
	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestAnUnknownSourceIsNamed(t *testing.T) {
	_, err := StatsFor("clickhouse", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse")

	_, err = StatsFor(SourceFile, "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--catalog.stats-file")
}
