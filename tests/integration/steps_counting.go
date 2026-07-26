//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// metricsInterval is how often epos-registry's exporter emits during the suite.
// Short enough that a scenario is not waiting on it, long enough not to spin.
const metricsInterval = 200 * time.Millisecond

// countSettleTime is how long a scenario waits before concluding that a count
// did *not* happen. Several export intervals, so an absent data point means the
// download was never recorded rather than not yet flushed.
const countSettleTime = 10 * metricsInterval

// metricsOutput collects epos-registry's stdout, where SPEC.md 5.3's stdout
// exporter writes. It is an io.Writer handed to exec.Cmd, so writes arrive on
// the process-reaping goroutine while steps read: hence the mutex.
type metricsOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (m *metricsOutput) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Mirrored to stderr so a failing scenario is debuggable from the log.
	_, _ = os.Stderr.Write(p)
	return m.buf.Write(p)
}

func (m *metricsOutput) snapshot() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.buf.Bytes()...)
}

// exporterPayload is the subset of stdoutmetric's JSON the suite reads.
type exporterPayload struct {
	ScopeMetrics []struct {
		Metrics []struct {
			Name string `json:"Name"`
			Data struct {
				DataPoints []struct {
					Value      int64 `json:"Value"`
					Attributes []struct {
						Key   string `json:"Key"`
						Value struct {
							Value any `json:"Value"`
						} `json:"Value"`
					} `json:"Attributes"`
				} `json:"DataPoints"`
			} `json:"Data"`
		} `json:"Metrics"`
	} `json:"ScopeMetrics"`
}

// download is one epos.downloads data point as exported.
type download struct {
	repository string
	verified   bool
	count      int64
}

// downloads parses every export the registry has written so far.
//
// The exporter is cumulative, so the last export of a given attribute set
// carries the running total; later exports replace earlier ones rather than
// adding to them.
func (w *world) downloads() ([]download, error) {
	if w.metrics == nil {
		return nil, fmt.Errorf("epos-registry is not running")
	}

	latest := map[string]download{}
	dec := json.NewDecoder(bytes.NewReader(w.metrics.snapshot()))
	for {
		var payload exporterPayload
		if err := dec.Decode(&payload); err != nil {
			if err == io.EOF {
				break
			}
			// A partially written export is normal — the process is still
			// running — so stop at the last whole one rather than failing.
			break
		}

		for _, sm := range payload.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name != "epos.downloads" {
					continue
				}
				for _, dp := range m.Data.DataPoints {
					d := download{count: dp.Value}
					key := strings.Builder{}
					for _, a := range dp.Attributes {
						switch a.Key {
						case "repository":
							d.repository, _ = a.Value.Value.(string)
						case "verified":
							d.verified, _ = a.Value.Value.(bool)
						}
						fmt.Fprintf(&key, "%s=%v;", a.Key, a.Value.Value)
					}
					latest[key.String()] = d
				}
			}
		}
	}

	out := make([]download, 0, len(latest))
	for _, d := range latest {
		out = append(out, d)
	}
	return out, nil
}

// countFor totals the downloads recorded against a repository.
func (w *world) countFor(repository string) (int64, error) {
	all, err := w.downloads()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, d := range all {
		if d.repository == repository {
			total += d.count
		}
	}
	return total, nil
}

// awaitCount polls until the repository's count reaches want, or the deadline
// passes. Polling rather than sleeping keeps the scenario as fast as the
// exporter allows.
func (w *world) awaitCount(repository string, want int64) error {
	deadline := time.Now().Add(30 * time.Second)
	var last int64
	for time.Now().Before(deadline) {
		got, err := w.countFor(repository)
		if err != nil {
			return err
		}
		if got == want {
			return nil
		}
		if got > want {
			return fmt.Errorf("download count for %q = %d, want %d — counted too much",
				repository, got, want)
		}
		last = got
		time.Sleep(metricsInterval / 2)
	}
	return fmt.Errorf("download count for %q reached %d, want %d before the deadline",
		repository, last, want)
}

// --- steps -----------------------------------------------------------------

func (w *world) downloadCountIncreasesBy(repository string, delta int64) error {
	return w.awaitCount(repository, delta)
}

// downloadCountUnchanged proves a resolve did not count. There is nothing to
// wait *for*, so it waits out several export intervals and then asserts the
// data point never appeared.
func (w *world) downloadCountUnchanged(repository string) error {
	time.Sleep(countSettleTime)

	got, err := w.countFor(repository)
	if err != nil {
		return err
	}
	if got != 0 {
		return fmt.Errorf("download count for %q = %d, want 0 — a resolve was counted",
			repository, got)
	}
	return nil
}

func (w *world) recordedDownloadIs(verified bool) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		all, err := w.downloads()
		if err != nil {
			return err
		}
		if len(all) == 1 {
			if all[0].verified != verified {
				return fmt.Errorf("recorded download has verified=%v, want %v",
					all[0].verified, verified)
			}
			return nil
		}
		if len(all) > 1 {
			return fmt.Errorf("expected a single recorded download, got %d: %+v", len(all), all)
		}
		time.Sleep(metricsInterval / 2)
	}
	return fmt.Errorf("no download was recorded before the deadline")
}

// fetchContentBlobSending performs a blob fetch carrying one extra header,
// which is how a scenario sends Epos-Download (SPEC.md 5.2).
func (w *world) fetchContentBlobSending(ref, header string) error {
	name, value, ok := strings.Cut(header, ": ")
	if !ok {
		return fmt.Errorf("header %q is not \"<name>: <value>\"", header)
	}
	return w.blobRequest(ref, map[string]string{name: value}, false)
}
