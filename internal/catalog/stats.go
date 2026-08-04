package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

//go:generate go tool mockgen -source=stats.go -destination=mocks_test.go -package=catalog

// Stats reports how often each repository has been pulled.
//
// One method, context-taking. That is the property which makes a fourth source
// an addition rather than a rewrite.
//
// Which repositories a source reports on is fixed when the source is built, not
// passed to the method: the catalog asks about the repositories in its index,
// and an unscoped source would answer for every repository the store has ever
// seen — including ones this catalog does not list.
type Stats interface {
	Pulls(ctx context.Context) (Counts, error)
}

// Counts is per-repository pulls as of a stated moment.
//
// One type serves as the on-disk JSON shape, the query-result shape and the
// in-memory shape, deliberately: there is no converter and no second schema to
// drift.
type Counts struct {
	CapturedAt time.Time `json:"captured_at"`
	// Note says where these numbers came from, and it is rendered on every page
	// that shows them.
	//
	// A count with no provenance is a claim the reader cannot check. That
	// matters most where it is least visible: a demo whose figures came from a
	// checked-in file looks exactly like one whose figures came from real
	// traffic, and only the page can tell them apart. A source that measured
	// something can leave this empty — the capture time already says when.
	Note string           `json:"note,omitempty"`
	Rows map[string]Pulls `json:"rows"`
}

// Pulls is one repository's two numbers.
//
// Only Verified may be ranked on. Unverified is known-inflated: a cosign
// signature is a referrer in the skill's own repository, so every `epos verify`
// counts as a download of the skill it verifies — cmd/epos-registry/handler.go
// says so at length. Verified is true only when the request carried
// Epos-Download, which only `epos pull` sends.
type Pulls struct {
	Verified   int64 `json:"verified"`
	Unverified int64 `json:"unverified"`
}

// Sources selectable by --catalog.stats-source.
//
// clickhouse is the production one: the store the download span lands in, read
// through the query deploy/clickhouse/01-schema.sql states. file is an input an
// operator holds; none is the absence of a source and a supported configuration
// rather than a degraded mode.
const (
	SourceNone       = "none"
	SourceFile       = "file"
	SourceClickHouse = "clickhouse"
)

// StatsFor builds the statistics source a configuration names.
//
// `none` is a nil Stats, handled at one call site in the renderer, rather than
// a type with empty methods. Stated because 4.2 asks for the choice to be made
// once: an implementation that has both a NoStats type *and* nil-checks is two
// answers to the same question, and the second one is where the zeroes get in.
//
// repos scopes the source to the catalog's index.
//
// dsn reaches only the clickhouse case. It is a working credential for a
// queryable database, so it is passed rather than stored, never logged, and
// never put into an error — see NewClickHouseStats, which declines to quote it
// back even when it cannot parse it.
func StatsFor(source, file, dsn string, repos []string) (Stats, error) {
	switch source {
	case "", SourceNone:
		return nil, nil
	case SourceFile:
		if file == "" {
			return nil, fmt.Errorf("--catalog.stats-source %s needs --catalog.stats-file", SourceFile)
		}
		return NewFileStats(file, repos), nil
	case SourceClickHouse:
		return NewClickHouseStats(dsn, repos)
	default:
		return nil, fmt.Errorf("unknown statistics source %q: use %s, %s or %s",
			source, SourceNone, SourceFile, SourceClickHouse)
	}
}

// FileStats reads counts from a JSON document with the shape of Counts.
//
// An *input*, never a store the catalog writes. It is what makes an export
// reproducible, it is how the renderer's tests get counts without a container,
// and it is the answer for anyone holding numbers from somewhere epos does not
// know about.
type FileStats struct {
	path  string
	repos map[string]bool
}

// NewFileStats returns a source reading path, scoped to repos.
//
// The scope is applied even though the file is the operator's own: all sources
// have to answer the same question, and a file listing a repository the catalog
// does not show would put a row on a page with nothing to attach it to.
func NewFileStats(path string, repos []string) *FileStats {
	set := make(map[string]bool, len(repos))
	for _, r := range repos {
		set[r] = true
	}
	return &FileStats{path: path, repos: set}
}

func (f *FileStats) Pulls(context.Context) (Counts, error) {
	file, err := os.Open(f.path)
	if err != nil {
		return Counts{}, fmt.Errorf("read the counts file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Rejected whole, with the filename in the error. A malformed document
	// half-read into partial counts is worse than no counts: the page would
	// look right and be wrong. DisallowUnknownFields is part of that — a
	// mistyped key is a file the operator meant to say something with.
	//
	// Bounded, because it is a path an operator names: a counts file is a few
	// kilobytes and anything past maxCountsFile is a mistake, not a catalog.
	var counts Counts
	decoder := json.NewDecoder(io.LimitReader(file, maxCountsFile))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&counts); err != nil {
		return Counts{}, fmt.Errorf("%s: %w", f.path, err)
	}

	scoped := make(map[string]Pulls, len(counts.Rows))
	for repository, pulls := range counts.Rows {
		if len(f.repos) == 0 || f.repos[repository] {
			scoped[repository] = pulls
		}
	}
	counts.Rows = scoped
	return counts, nil
}

// maxCountsFile caps the counts document, for the same reason the rendered
// document is capped: it is a file an operator points at, and a 2 GiB one
// should fail rather than be read into memory.
const maxCountsFile = 8 << 20

// cachedStats is a lazy refresh on the request path under one mutex.
//
// Not a background goroutine. A refresher needs an owner, a shutdown path
// threaded through srv.Shutdown and a -race story on three platforms, and it
// buys nothing: the work is one query and the first request after the TTL
// expires is the natural place to do it. Holding the mutex across the query
// serialises a stampede for free; if that ever proves too coarse the next step
// is singleflight, which is still not a goroutine anyone owns.
//
// A TTL of zero means "query every request", exactly — which is what the
// end-to-end assertion sets it to rather than sleeping.
type cachedStats struct {
	inner   Stats
	ttl     time.Duration
	timeout time.Duration

	mu     sync.Mutex
	at     time.Time
	counts Counts
	err    error
	loaded bool
}

// WithCache bounds how often a source is asked and how long it may take.
func WithCache(inner Stats, ttl, timeout time.Duration) Stats {
	if inner == nil {
		return nil
	}
	return &cachedStats{inner: inner, ttl: ttl, timeout: timeout}
}

func (c *cachedStats) Pulls(ctx context.Context) (Counts, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.loaded && c.ttl > 0 && time.Since(c.at) < c.ttl {
		return c.counts, c.err
	}

	// A slow store must not pin a handler in the process that is also answering
	// /v2/. The timeout is the mechanism; the mutex above is what stops a burst
	// of page loads becoming a burst of queries.
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	c.counts, c.err = c.inner.Pulls(ctx)
	c.at = time.Now()
	c.loaded = true
	return c.counts, c.err
}

// Close releases whatever the wrapped source holds, and nothing when it holds
// nothing.
//
// The cache is what the rest of the program is handed, so without this the
// connection pool underneath a clickhouse source would be unreachable — and the
// alternative, putting Close on Stats, would make every future source carry a
// method two of the three have no use for.
func (c *cachedStats) Close() error {
	if closer, ok := c.inner.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
