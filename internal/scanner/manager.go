package scanner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// Scan states reported by Manager.Status.
const (
	StateIdle      = "idle"
	StateRunning   = "running"
	StateDone      = "done"
	StateError     = "error"
	StateCancelled = "cancelled"
)

var (
	// ErrScanInProgress is returned by Report while a scan is running.
	ErrScanInProgress = errors.New("scan in progress")
	// ErrNoReport is returned by Report when no scan has completed yet.
	ErrNoReport = errors.New("no scan report available")
)

// Status is the pollable snapshot of the scan manager.
type Status struct {
	State      string     `json:"state"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Total      int        `json:"total"`
	Probed     int        `json:"probed"`
	Alive      int        `json:"alive"`
	Dead       int        `json:"dead"`
	Error      string     `json:"error,omitempty"`
	HasReport  bool       `json:"has_report"`
}

// Manager runs at most one scan at a time and keeps the latest report in
// memory. It holds no database handle: targets come from the source's
// StreamProbeLister capability and nothing it produces is ever written
// anywhere.
type Manager struct {
	lister source.StreamProbeLister

	mu         sync.Mutex
	state      string
	startedAt  time.Time
	finishedAt time.Time
	total      int
	probed     int
	alive      int
	errMsg     string
	report     *Report
	cancel     context.CancelFunc
}

// NewManager returns an idle scan manager that enumerates targets through the
// given source capability.
func NewManager(lister source.StreamProbeLister) *Manager {
	return &Manager{lister: lister, state: StateIdle}
}

// Start begins a scan in the background and returns immediately. If a scan is
// already running, no second scan is started and the in-progress status is
// returned with started=false.
func (m *Manager) Start() (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == StateRunning {
		return m.statusLocked(), false
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.state = StateRunning
	m.startedAt = time.Now().UTC()
	m.finishedAt = time.Time{}
	m.total = 0
	m.probed = 0
	m.alive = 0
	m.errMsg = ""
	m.report = nil
	m.cancel = cancel

	go m.run(ctx)
	return m.statusLocked(), true
}

// Cancel aborts a running scan (context cancellation is honored by RunScan).
// Intended for shutdown wiring. A cancelled scan ends in StateCancelled and a
// truncated run is deliberately NOT published as a report: Report() returns
// ErrNoReport so a partial scan can never be mistaken for a complete one.
// Calling it when no scan is running — including on a manager that never
// started one — is a harmless no-op.
func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

// Status returns a snapshot of the current scan state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

// Report returns the latest completed report, or an error explaining why one
// is not available (running or never scanned).
func (m *Manager) Report() (*Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.state {
	case StateRunning:
		return nil, ErrScanInProgress
	case StateIdle:
		return nil, ErrNoReport
	}
	if m.report == nil {
		return nil, ErrNoReport
	}
	return m.report, nil
}

// run executes the scan and records its outcome. It never panics out of the
// goroutine: a panic is converted into an error state.
func (m *Manager) run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.state = StateError
			m.errMsg = fmt.Sprintf("scan panicked: %v", r)
			m.finishedAt = time.Now().UTC()
			m.cancel = nil
		}
	}()

	targets, err := m.lister.ListStreamsForProbe()
	if err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.state = StateError
		m.errMsg = fmt.Sprintf("list streams for probe: %v", err)
		m.finishedAt = time.Now().UTC()
		m.cancel = nil
		return
	}

	report, err := RunScan(ctx, targets, m.onProgress)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishedAt = time.Now().UTC()
	m.cancel = nil
	switch {
	case err == nil:
		m.state = StateDone
		if report != nil {
			m.publishLocked(report)
		}
	case errors.Is(err, context.Canceled):
		// A cancelled scan is a truncated run. It is reported as StateCancelled
		// with its partial result deliberately left unpublished, so Report()
		// never hands back an incomplete scan as if it were the finished one.
		m.state = StateCancelled
		m.errMsg = err.Error()
	default:
		m.state = StateError
		m.errMsg = err.Error()
		if report != nil {
			m.publishLocked(report)
		}
	}
}

// publishLocked records a completed report and its aggregate counts. Callers
// must hold m.mu.
func (m *Manager) publishLocked(report *Report) {
	m.report = report
	m.total = report.TotalStreams
	m.probed = len(report.Streams)
	m.alive = report.AliveStreams
}

// onProgress records live probe counts for the status endpoint.
func (m *Manager) onProgress(probed, total, alive int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.probed = probed
	m.total = total
	m.alive = alive
}

func (m *Manager) statusLocked() Status {
	s := Status{
		State:     m.state,
		Total:     m.total,
		Probed:    m.probed,
		Alive:     m.alive,
		Dead:      m.probed - m.alive,
		Error:     m.errMsg,
		HasReport: m.report != nil,
	}
	if !m.startedAt.IsZero() {
		t := m.startedAt
		s.StartedAt = &t
	}
	if !m.finishedAt.IsZero() {
		t := m.finishedAt
		s.FinishedAt = &t
	}
	return s
}
