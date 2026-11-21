package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
)

// Trigger branches of the overview pipeline. Several run triggers share one
// branch: the operator cares whether a push, the clock, this UI or a retry
// started work, not which UI button it was.
const (
	branchPush     = "push"
	branchSchedule = "schedule"
	branchManual   = "manual"
	branchRetry    = "retry"
)

// Target states in the pipeline, in order of precedence.
const (
	targetHeld    = "held"
	targetBusy    = "busy"
	targetDone    = "done"
	targetFailing = "failed"
	targetIdle    = "idle"
)

// pipelineView is the overview: triggers fan in to ForgeMirror, which fans
// out to the configured targets.
type pipelineView struct {
	Triggers  []triggerView
	Engine    engineView
	Targets   []pipelineTarget
	Available []string // labels of accepted target types nobody configured
	Live      bool
}

type triggerView struct {
	Key, Label, Source string
	Enabled, Failed    bool
	// Running counts the running repositories this trigger started.
	Running int
}

type engineView struct {
	Busy bool
	// Line is the visible status ("idle", "syncing acme/app", "4 running").
	Line string
	// By names the triggers of the running work ("by push", "1 by push, 3 by
	// schedule"); empty when idle.
	By     string
	Queued int
	// FirstSweep is set until the scheduler finished its first sweep, which
	// starts with the service; NextSweep is known from then on.
	FirstSweep bool
	NextSweep  time.Time
	Error      string
}

type pipelineTarget struct {
	Name, State, Note string
}

func triggerBranch(trigger string) string {
	switch trigger {
	case "webhook", "source deletion":
		return branchPush
	case "schedule":
		return branchSchedule
	case "retry":
		return branchRetry
	default:
		return branchManual
	}
}

func (h *handler) pipeline(d *pageData) pipelineView {
	sched := d.Snapshot.Schedule
	running := map[string]int{}
	for _, r := range d.Snapshot.Repos {
		if r.Running && r.Trigger != "" {
			running[triggerBranch(r.Trigger)]++
		}
	}

	scheduleSource := "no interval"
	if sched.Interval > 0 {
		scheduleSource = "every " + sched.Interval.String()
	}
	retrySource := "after failures"
	if n := len(d.Retries); n > 0 {
		retrySource = fmt.Sprintf("%d pending", n)
	}
	p := pipelineView{
		Triggers: []triggerView{
			{Key: branchPush, Label: "push", Source: labelForgejo, Enabled: d.OnPush},
			{
				Key:     branchSchedule,
				Label:   "schedule",
				Source:  scheduleSource,
				Enabled: sched.Interval > 0,
				Failed:  sched.Error != "",
			},
			{Key: branchManual, Label: "manual", Source: "this UI", Enabled: d.Actions},
			{Key: branchRetry, Label: "retry", Source: retrySource, Enabled: true},
		},
		Engine: engineView{Busy: d.Running > 0, Queued: d.Queued, Error: sched.Error},
		Live:   d.Running > 0,
	}
	for i := range p.Triggers {
		p.Triggers[i].Running = running[p.Triggers[i].Key]
	}
	p.Engine.Line = engineLine(d)
	p.Engine.By = runningBy(d.Running, p.Triggers)
	if sched.Interval > 0 {
		p.Engine.FirstSweep = sched.LastRun.IsZero()
		if !p.Engine.FirstSweep && !p.Engine.Busy {
			p.Engine.NextSweep = sched.LastRun.Add(sched.Interval)
		}
	}

	for _, name := range d.TargetNames {
		p.Targets = append(p.Targets, targetState(name, d, h.opts.Now()))
	}
	for _, t := range d.Types {
		if len(t.Names) == 0 {
			p.Available = append(p.Available, t.Label)
		}
	}

	return p
}

func engineLine(d *pageData) string {
	switch d.Running {
	case 0:
		return "idle"
	case 1:
		for _, r := range d.Repos {
			if r.Activity != nil {
				return strings.ToLower(r.Activity.Label) + " " + r.Name
			}
		}
		return "1 running"
	default:
		return fmt.Sprintf("%d running", d.Running)
	}
}

// runningBy names the triggers of the running work: "by push" for a single
// run, counts per trigger otherwise.
func runningBy(running int, triggers []triggerView) string {
	var parts []string
	for _, t := range triggers {
		if t.Running > 0 {
			parts = append(parts, fmt.Sprintf("%d by %s", t.Running, t.Label))
		}
	}
	if len(parts) == 1 && running == 1 {
		return strings.TrimPrefix(parts[0], "1 ")
	}
	return strings.Join(parts, ", ")
}

// targetState summarises one target across all repositories, with a note
// that says what is happening there.
func targetState(name string, d *pageData, now time.Time) pipelineTarget {
	if note, held := heldNote(name, d.Pauses); held {
		return pipelineTarget{Name: name, State: targetHeld, Note: note}
	}

	u := targetUsage(name, d.Snapshot.Repos)
	t := pipelineTarget{Name: name, State: targetIdle}
	switch {
	case len(u.pending) > 0:
		t.State, t.Note = targetBusy, "pushing "+names(u.pending)
	case len(u.pushed) > 0:
		t.State, t.Note = targetDone, "pushed "+names(u.pushed)
	case len(u.failed) > 0:
		t.State, t.Note = targetFailing, "failed: "+names(u.failed)
	case u.mirrored == 0:
		t.Note = "no repositories yet"
	default:
		t.Note = repositories(u.mirrored) + " · never pushed"
		if !u.lastPush.IsZero() {
			t.Note = repositories(u.mirrored) + " · last push " + compactDuration(now.Sub(u.lastPush)) + " ago"
		}
	}

	return t
}

// heldNote reports a hold that covers every repository on the target: a
// per-repository hold is shown on that repository, not on the whole branch.
func heldNote(name string, pauses []status.Pause) (string, bool) {
	for _, p := range pauses {
		if !p.Scope.AllRepositories || (!p.Scope.AllTargets && !slices.Contains(p.Scope.Targets, name)) {
			continue
		}
		note := "held"
		if p.Reason != "" {
			note += ": " + p.Reason
		}
		if !p.Until.IsZero() {
			note += " · until " + p.Until.UTC().Format("Jan 2 15:04 UTC")
		}
		return note, true
	}
	return "", false
}

type usage struct {
	pending, pushed, failed []string
	mirrored                int
	lastPush                time.Time
}

func targetUsage(name string, repos []status.Repo) usage {
	var u usage
	for _, r := range repos {
		target, known := repoTarget(r, name)
		// A compared but never pushed repository still belongs to the target.
		if known || planned(r, name) {
			u.mirrored++
		}
		if known && target.Error == "" && target.LastRun.After(u.lastPush) {
			u.lastPush = target.LastRun
		}
		switch {
		// TargetFinished stamps LastRun; until then this run still owes the
		// target its push.
		case r.Running && r.Applying && (!known || target.LastRun.Before(r.LastStarted)):
			u.pending = append(u.pending, r.Name)
		case r.Running && r.Applying:
			u.pushed = append(u.pushed, r.Name)
		case known && target.Error != "":
			u.failed = append(u.failed, r.Name)
		}
	}
	return u
}

// names lists repositories for a note, shortened so one long list cannot
// stretch the pipeline: "acme/app, acme/lib +3 more".
func names(list []string) string {
	const shown = 2
	if len(list) <= shown {
		return strings.Join(list, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(list[:shown], ", "), len(list)-shown)
}

func planned(r status.Repo, name string) bool {
	for _, p := range []*plan.Repo{r.Current, r.Plan} {
		if p != nil && slices.ContainsFunc(p.Targets, func(t plan.Target) bool { return t.Name == name }) {
			return true
		}
	}
	return false
}

func repoTarget(r status.Repo, name string) (status.Target, bool) {
	i := slices.IndexFunc(r.Targets, func(t status.Target) bool { return t.Name == name })
	if i < 0 {
		return status.Target{}, false
	}
	return r.Targets[i], true
}
