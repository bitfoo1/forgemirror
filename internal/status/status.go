// Package status keeps the recent state of every repository and target in
// memory for the web UI. It receives the same events as the metrics, but
// per repository, which metrics cannot afford (unbounded label values).
//
// Optional persistence keeps the last comparison and sync attempt across restarts.
package status

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
)

// operationDelete is the forge operation that removes a target repository;
// see dispatch.Observer.
const operationDelete = "delete"

// maxPlanRefs bounds the ref changes kept per target and plan. The first plan
// of a repository with thousands of tags would otherwise keep all of them in
// memory until the next run; the UI cannot show that many usefully anyway.
const maxPlanRefs = 100

// Options configures a [Store].
type Options struct {
	Persistence        Persistence
	ControlPersistence Persistence
	OnError            func()
	// Redact removes configured secrets from serialized state before it reaches disk.
	Redact func([]byte) ([]byte, error)
	// ScheduleInterval is sync.schedule; 0 means no schedule.
	ScheduleInterval time.Duration
	// Now returns the current time; nil means [time.Now].
	Now func() time.Time
}

// Store records the state of repositories, targets and the scheduler. It
// implements dispatch.Observer and schedule.Observer and is safe for
// concurrent use.
type Store struct {
	controls    Persistence
	controlMu   sync.RWMutex
	pauses      []Pause
	persistence Persistence
	redact      func([]byte) ([]byte, error)
	persistErr  string
	onError     func()
	now         func() time.Time
	started     time.Time
	mu          sync.Mutex
	repos       map[string]*repoState
	schedule    Schedule
}

type repoState struct {
	running int
	// applying is set while the running run applies its plan.
	applying     bool
	attempt      *Attempt
	stale        bool
	plan         *plan.Repo
	current      *plan.Repo
	lastStarted  time.Time
	lastFinished time.Time
	targets      map[string]*Target
	// deleting is set when a run removed (or tried to remove) target
	// repositories, i.e. the source repository was deleted.
	deleting bool
	// deleteFailed keeps the repository visible after a failed deletion.
	deleteFailed bool
}

// Snapshot is a consistent copy of the store's state.
type Snapshot struct {
	// Started is when the store, and so the service, started.
	Started  time.Time
	Error    string
	Repos    []Repo
	Schedule Schedule
}

// Repo is the state of one source repository.
type Repo struct {
	// Name is the full name, "owner/name".
	Name    string `json:"name"`
	Running bool   `json:"-"`
	// Applying is set while the running run applies its plan; a running run
	// without it only plans.
	Applying     bool      `json:"-"`
	LastStarted  time.Time `json:"last_started"`
	LastFinished time.Time `json:"last_finished"`
	// Targets is sorted by target name.
	Targets []Target `json:"targets"`
	// Plan is the newest plan; nil before the first. It is shared, not
	// copied: callers must not modify it.
	Plan        *plan.Repo `json:"plan"`
	LastAttempt *Attempt   `json:"last_attempt"`
	Current     *plan.Repo `json:"current"`
	Stale       bool       `json:"-"`
}

// Target is the state of one repository on one target.
type Target struct {
	Name string `json:"name"`
	// LastRun is when the last push (or failed attempt) finished; zero if
	// only forge operations ran so far.
	LastRun  time.Time     `json:"last_run"`
	Duration time.Duration `json:"duration"`
	// Error is the redacted error text of the last push; empty on success.
	Error string `json:"error"`
	// Operations holds the last result of each forge operation, sorted by
	// name.
	Operations []Operation `json:"operations"`
}

// Operation is the last result of one forge API operation.
type Operation struct {
	Name  string    `json:"name"`
	At    time.Time `json:"at"`
	Error string    `json:"error"`
}

// Schedule is the state of scheduled syncs.
type Schedule struct {
	Interval time.Duration
	// LastRun is when the last sweep finished; zero before the first.
	LastRun time.Time
	Repos   int
	Error   string
	Runs    int
}

// New returns an empty [Store].
func New(opts Options) *Store {
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &Store{
		now:         now,
		persistence: opts.Persistence,
		controls:    opts.ControlPersistence,
		onError:     opts.OnError,
		redact:      opts.Redact,
		started:     now(),
		repos:       make(map[string]*repoState),
		schedule:    Schedule{Interval: opts.ScheduleInterval},
	}
}

func (s *Store) repo(name string) *repoState {
	r, ok := s.repos[name]
	if !ok {
		r = &repoState{targets: make(map[string]*Target)}
		s.repos[name] = r
	}

	return r
}

func (r *repoState) target(name string) *Target {
	t, ok := r.targets[name]
	if !ok {
		t = &Target{Name: name}
		r.targets[name] = t
	}

	return t
}

// RunStarted implements dispatch.Observer.
func (s *Store) RunStarted(ctx context.Context, repo string, apply bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.repo(repo)
	r.running++
	r.applying = apply
	r.lastStarted = s.now()
	r.deleting, r.deleteFailed = false, false
	if apply {
		r.stale = true
	}
	_ = s.persist(ctx, repo)
}

// PlanFinished implements dispatch.Observer.
func (s *Store) PlanFinished(ctx context.Context, p plan.Repo) {
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := p.Truncate(maxPlanRefs)
	r := s.repo(p.Name)
	r.plan = &kept
	if p.Apply {
		r.attempt = &Attempt{Plan: kept, Started: r.lastStarted, Status: "running"}
		r.stale = true
	} else {
		r.current = &kept
		r.stale = p.Failed()
	}
	_ = s.persist(ctx, p.Name)
}

// RunFinished implements dispatch.Observer. A repository is forgotten once
// it is gone from the source and nothing is left to delete on the targets.
func (s *Store) RunFinished(ctx context.Context, repo string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.repo(repo)
	r.running = max(r.running-1, 0)
	r.lastFinished = s.now()
	if r.running > 0 {
		return
	}
	if r.applying && r.attempt != nil {
		r.attempt.Finished = r.lastFinished
		r.attempt.Status = attemptStatus(r.attempt)
		if ctx.Err() != nil {
			r.attempt.Status = attemptInterrupted
			r.stale = true
		}
	}
	if err := s.persist(ctx, repo); err != nil && r.applying && r.attempt != nil {
		r.attempt.Status = attemptInterrupted
		r.attempt.Error = "Sync completion could not be saved; verification is required."
		r.stale = true
	}
	r.applying = false

	gone := r.plan != nil && r.plan.SourceDeleted && (r.plan.Changes() == 0 || r.plan.Apply)
	if (r.deleting || gone) && !r.deleteFailed && r.attempt == nil {
		delete(s.repos, repo)
	}
}

// TargetFinished implements dispatch.Observer.
func (s *Store) TargetFinished(ctx context.Context, repo, target string, duration time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t := s.repo(repo).target(target)
	t.LastRun = s.now()
	t.Duration = duration
	t.Error = errorText(err)
	r := s.repo(repo)
	if r.applying && r.attempt != nil {
		r.attempt.recordTarget(*t)
	}
	_ = s.persist(ctx, repo)
}

// SyncFinished implements dispatch.Observer.
func (s *Store) SyncFinished(ctx context.Context, repo, target, operation string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.repo(repo)
	if operation == operationDelete {
		r.deleting = true
		r.deleteFailed = r.deleteFailed || err != nil
	}

	t := r.target(target)
	op := Operation{Name: operation, At: s.now(), Error: errorText(err)}
	if i := slices.IndexFunc(t.Operations, func(o Operation) bool { return o.Name == operation }); i >= 0 {
		t.Operations[i] = op
		if r.applying && r.attempt != nil {
			r.attempt.recordOperation(target, op)
		}
		_ = s.persist(ctx, repo)

		return
	}
	t.Operations = append(t.Operations, op)
	slices.SortFunc(t.Operations, func(a, b Operation) int { return cmp.Compare(a.Name, b.Name) })
	if r.applying && r.attempt != nil {
		r.attempt.recordOperation(target, op)
	}
	_ = s.persist(ctx, repo)
}

// ScheduledRunFinished implements schedule.Observer.
func (s *Store) ScheduledRunFinished(_ context.Context, repos int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.schedule.LastRun = s.now()
	s.schedule.Repos = repos
	s.schedule.Error = errorText(err)
	s.schedule.Runs++
}

// Snapshot returns a copy of the current state; repositories are sorted by
// name.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := Snapshot{
		Started:  s.started,
		Error:    s.persistErr,
		Schedule: s.schedule,
		Repos:    make([]Repo, 0, len(s.repos)),
	}
	for _, name := range slices.Sorted(maps.Keys(s.repos)) {
		r := s.repos[name]
		repo := Repo{
			Name:         name,
			Running:      r.running > 0,
			Applying:     r.applying,
			Plan:         r.plan,
			LastAttempt:  cloneAttempt(r.attempt),
			Current:      r.current,
			Stale:        r.stale,
			LastStarted:  r.lastStarted,
			LastFinished: r.lastFinished,
			Targets:      make([]Target, 0, len(r.targets)),
		}
		for _, tn := range slices.Sorted(maps.Keys(r.targets)) {
			t := *r.targets[tn]
			t.Operations = slices.Clone(t.Operations)
			repo.Targets = append(repo.Targets, t)
		}
		snap.Repos = append(snap.Repos, repo)
	}

	return snap
}

// errorText returns err's message. It is the same text that is logged,
// which is redacted where it is created (git output, URLs).
func errorText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
