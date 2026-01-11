package health

import (
	"context"
	"fmt"
	"sync"

	"github.com/iodesystems/ubuntu-router/internal/system"
)

// State represents the result state of a health check
type State string

const (
	StateOK      State = "ok"
	StateWarning State = "warning"
	StateError   State = "error"
	StateUnknown State = "unknown"
	StateSkipped State = "skipped" // Skipped due to failed dependency
)

// Severity indicates how serious an issue is
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Fixer is a function that can attempt to fix an issue
type Fixer struct {
	Description string                             // Human-readable description of what the fixer does
	Fix         func(ctx context.Context) error    // The fix function
	RequiresRoot bool                              // Whether this fixer requires root privileges
}

// Issue represents a problem found by a check
type Issue struct {
	Severity    Severity `json:"severity"`
	Message     string   `json:"message"`
	Details     string   `json:"details,omitempty"`
	Fixer       *Fixer   `json:"-"` // Not serialized to JSON
	FixerDesc   string   `json:"fixer,omitempty"` // Description for JSON
	FixerID     string   `json:"fixer_id,omitempty"` // ID to reference the fixer
}

// Status is the result of running a check
type Status struct {
	CheckID     string    `json:"check_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	State       State     `json:"state"`
	Message     string    `json:"message"`
	Issues      []Issue   `json:"issues,omitempty"`
	SubChecks   []*Status `json:"sub_checks,omitempty"` // Checks spawned by this check
}

// Check is the interface that all health checks must implement
type Check interface {
	// ID returns a unique identifier for this check
	ID() string
	// Name returns a human-readable name
	Name() string
	// Description returns what this check verifies
	Description() string
	// DependsOn returns IDs of checks that must pass before this one runs
	DependsOn() []string
	// Run executes the check and returns the status
	Run(ctx context.Context) *Status
}

// BaseCheck provides common functionality for checks
type BaseCheck struct {
	id          string
	name        string
	description string
	dependsOn   []string
	runner      system.CommandRunner
	fs          system.FileSystem
}

func (b *BaseCheck) ID() string          { return b.id }
func (b *BaseCheck) Name() string        { return b.name }
func (b *BaseCheck) Description() string { return b.description }
func (b *BaseCheck) DependsOn() []string { return b.dependsOn }

// NewBaseCheck creates a new base check
func NewBaseCheck(id, name, description string, deps []string, runner system.CommandRunner, fs system.FileSystem) BaseCheck {
	return BaseCheck{
		id:          id,
		name:        name,
		description: description,
		dependsOn:   deps,
		runner:      runner,
		fs:          fs,
	}
}

// Runner manages and executes health checks
type Runner struct {
	mu       sync.RWMutex
	checks   map[string]Check
	order    []string // Topologically sorted check order
	results  map[string]*Status
	runner   system.CommandRunner
	fs       system.FileSystem
}

// NewRunner creates a new health check runner
func NewRunner(cmdRunner system.CommandRunner, fs system.FileSystem) *Runner {
	return &Runner{
		checks:  make(map[string]Check),
		results: make(map[string]*Status),
		runner:  cmdRunner,
		fs:      fs,
	}
}

// Register adds a check to the runner
func (r *Runner) Register(check Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[check.ID()] = check
	r.order = nil // Invalidate order cache
}

// computeOrder performs topological sort of checks based on dependencies
func (r *Runner) computeOrder() ([]string, error) {
	// Build dependency graph
	inDegree := make(map[string]int)
	dependents := make(map[string][]string)

	for id := range r.checks {
		inDegree[id] = 0
	}

	for id, check := range r.checks {
		for _, dep := range check.DependsOn() {
			if _, exists := r.checks[dep]; !exists {
				return nil, fmt.Errorf("check %s depends on unknown check %s", id, dep)
			}
			inDegree[id]++
			dependents[dep] = append(dependents[dep], id)
		}
	}

	// Kahn's algorithm
	var queue []string
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	var order []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)

		for _, dep := range dependents[id] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(order) != len(r.checks) {
		return nil, fmt.Errorf("circular dependency detected in health checks")
	}

	return order, nil
}

// RunAll executes all registered checks in dependency order
func (r *Runner) RunAll(ctx context.Context) ([]*Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Compute order if needed
	if r.order == nil {
		order, err := r.computeOrder()
		if err != nil {
			return nil, err
		}
		r.order = order
	}

	// Clear previous results
	r.results = make(map[string]*Status)

	var results []*Status

	for _, id := range r.order {
		check := r.checks[id]

		// Check if dependencies passed
		skip := false
		for _, dep := range check.DependsOn() {
			if depResult, ok := r.results[dep]; ok {
				if depResult.State == StateError || depResult.State == StateSkipped {
					skip = true
					break
				}
			}
		}

		var status *Status
		if skip {
			status = &Status{
				CheckID:     check.ID(),
				Name:        check.Name(),
				Description: check.Description(),
				State:       StateSkipped,
				Message:     "Skipped due to failed dependency",
			}
		} else {
			status = check.Run(ctx)
		}

		r.results[id] = status
		results = append(results, status)
	}

	return results, nil
}

// RunCheck executes a single check by ID (and its dependencies if needed)
func (r *Runner) RunCheck(ctx context.Context, id string) (*Status, error) {
	r.mu.RLock()
	check, exists := r.checks[id]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("check %s not found", id)
	}

	// Run dependencies first
	for _, dep := range check.DependsOn() {
		if _, err := r.RunCheck(ctx, dep); err != nil {
			return nil, err
		}
	}

	// Check if dependencies passed
	r.mu.RLock()
	for _, dep := range check.DependsOn() {
		if depResult, ok := r.results[dep]; ok {
			if depResult.State == StateError || depResult.State == StateSkipped {
				r.mu.RUnlock()
				return &Status{
					CheckID:     check.ID(),
					Name:        check.Name(),
					Description: check.Description(),
					State:       StateSkipped,
					Message:     "Skipped due to failed dependency",
				}, nil
			}
		}
	}
	r.mu.RUnlock()

	status := check.Run(ctx)

	r.mu.Lock()
	r.results[id] = status
	r.mu.Unlock()

	return status, nil
}

// GetFixers returns all available fixers from the last run results
func (r *Runner) GetFixers() map[string]*Fixer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	fixers := make(map[string]*Fixer)

	var collectFixers func(status *Status)
	collectFixers = func(status *Status) {
		for _, issue := range status.Issues {
			if issue.Fixer != nil && issue.FixerID != "" {
				fixers[issue.FixerID] = issue.Fixer
			}
		}
		for _, sub := range status.SubChecks {
			collectFixers(sub)
		}
	}

	for _, status := range r.results {
		collectFixers(status)
	}

	return fixers
}

// RunFixer executes a fixer by ID
func (r *Runner) RunFixer(ctx context.Context, fixerID string) error {
	fixers := r.GetFixers()
	fixer, ok := fixers[fixerID]
	if !ok {
		return fmt.Errorf("fixer %s not found", fixerID)
	}
	return fixer.Fix(ctx)
}

// Summary returns a summary of all check results
type Summary struct {
	Total    int           `json:"total"`
	OK       int           `json:"ok"`
	Warnings int           `json:"warnings"`
	Errors   int           `json:"errors"`
	Skipped  int           `json:"skipped"`
	Results  []*Status     `json:"results"`
}

// GetSummary returns a summary of the last run
func (r *Runner) GetSummary() *Summary {
	r.mu.RLock()
	defer r.mu.RUnlock()

	summary := &Summary{}

	for _, status := range r.results {
		summary.Total++
		switch status.State {
		case StateOK:
			summary.OK++
		case StateWarning:
			summary.Warnings++
		case StateError:
			summary.Errors++
		case StateSkipped:
			summary.Skipped++
		}
		summary.Results = append(summary.Results, status)
	}

	return summary
}
