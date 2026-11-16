package dispatch

import (
	"context"
	"log/slog"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/plan"
)

// refLogLimit caps the refs logged one by one at info level per target. A
// first push of a repository with hundreds of tags would otherwise flood the
// log; the rest is logged at debug level and counted in the summary.
const refLogLimit = 20

// logRefUpdates records which refs a push changed, as git reported them.
func logRefUpdates(ctx context.Context, logger *slog.Logger, updates []mirror.RefUpdate) {
	counts := make(map[string]int)
	for _, u := range updates {
		counts[u.Kind]++
	}
	logger.InfoContext(ctx, "refs pushed",
		slog.Int("created", counts[mirror.RefCreated]),
		slog.Int("updated", counts[mirror.RefUpdated]),
		slog.Int("forced", counts[mirror.RefForced]),
		slog.Int("deleted", counts[mirror.RefDeleted]),
	)

	for i, u := range updates {
		level := slog.LevelInfo
		if i >= refLogLimit {
			level = slog.LevelDebug
		}
		logger.Log(ctx, level, "ref pushed",
			slog.String("ref", u.Ref), slog.String("change", u.Kind), slog.String("summary", u.Summary))
	}
}

// logSettings records each changed repository setting with its old value.
func logSettings(ctx context.Context, logger *slog.Logger, target string, changes []plan.FieldChange) {
	for _, c := range changes {
		logger.InfoContext(ctx, "setting changed",
			slog.String("target", target), slog.String("field", c.Field),
			slog.String("old", c.Old), slog.String("new", c.New))
	}
}

// logLabels records each label that was created, updated or deleted.
func logLabels(ctx context.Context, logger *slog.Logger, target string, changes []plan.LabelChange) {
	for _, c := range changes {
		attrs := []any{slog.String("target", target), slog.String("label", c.Name)}
		if c.Old != nil && c.New != nil {
			attrs = append(attrs, labelAttrs("old", *c.Old), labelAttrs("new", *c.New))
		} else if c.New != nil {
			attrs = append(attrs, labelAttrs("new", *c.New))
		}
		logger.InfoContext(ctx, "label "+labelPast(c.Kind()), attrs...)
	}
}

func labelAttrs(group string, l forge.Label) slog.Attr {
	return slog.Group(group, slog.String("color", l.Color), slog.String("description", l.Description))
}

func labelVerb(kind string) string {
	switch kind {
	case plan.KindCreate:
		return "creating"
	case plan.KindUpdate:
		return "updating"
	default:
		return "deleting"
	}
}

func labelPast(kind string) string {
	switch kind {
	case plan.KindCreate:
		return "created"
	case plan.KindUpdate:
		return "updated"
	default:
		return "deleted"
	}
}

// logPlan summarises a plan at info level and lists every change at debug
// level; the apply steps log what they actually changed at info level.
func logPlan(ctx context.Context, logger *slog.Logger, p plan.Repo) {
	changes := p.Changes()
	targets := 0
	for _, t := range p.Targets {
		if t.Changes() > 0 {
			targets++
		}
	}

	msg := "plan: in sync"
	if changes > 0 {
		msg = "plan: out of sync"
	}
	logger.InfoContext(ctx, msg,
		slog.Bool("apply", p.Apply),
		slog.Bool("source_deleted", p.SourceDeleted),
		slog.Int("changes", changes),
		slog.Int("targets", targets),
	)

	for _, t := range p.Targets {
		tl := logger.With(slog.String("target", t.Name))
		if t.Error != "" {
			tl.WarnContext(ctx, "plan: target cannot be synced", slog.String("error", t.Error))
		}
		logPlanTarget(ctx, tl, t)
	}
}

func logPlanTarget(ctx context.Context, logger *slog.Logger, t plan.Target) {
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	for _, flag := range []struct {
		set bool
		msg string
	}{
		{t.Create, "plan: create repository"},
		{t.Privatise, "plan: make repository private"},
		{t.Delete, "plan: delete repository"},
		{t.RefsUnknown, "plan: refs unknown; full push"},
	} {
		if flag.set {
			logger.DebugContext(ctx, flag.msg)
		}
	}
	for _, r := range t.Refs {
		logger.DebugContext(ctx, "plan: ref "+r.Kind(),
			slog.String("ref", r.Ref), slog.String("old", r.Old), slog.String("new", r.New))
	}
	for _, s := range t.Settings {
		logger.DebugContext(ctx, "plan: setting", slog.String("field", s.Field),
			slog.String("old", s.Old), slog.String("new", s.New))
	}
	for _, l := range t.Labels {
		logger.DebugContext(ctx, "plan: label "+l.Kind(), slog.String("label", l.Name))
	}
}
