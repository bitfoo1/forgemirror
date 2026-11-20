package ui

import "fmt"

const activityRefreshing = "refreshing"

type activity struct{ Kind, Label string }

// Dispatcher names cover accepted/queued work before observer callbacks run;
// status records cover fixtures/observers and distinguish the active phase.
func populateActivity(data *pageData, queued map[string]bool) {
	active := make(map[string]activity, len(queued))
	for name, followUp := range queued {
		active[name] = activity{Kind: "starting", Label: "Starting"}
		if followUp {
			data.Queued++
		}
	}
	for _, repository := range data.Snapshot.Repos {
		if !repository.Running {
			continue
		}
		phase := activity{Kind: activityRefreshing, Label: "Refreshing"}
		if repository.Applying {
			phase = activity{Kind: "syncing", Label: "Syncing"}
		}
		active[repository.Name] = phase
	}
	phases := map[string]int{}
	for _, phase := range active {
		phases[phase.Kind]++
	}
	data.Running = len(active)
	data.Activity = activitySummary(data.Running, phases)
	for i := range data.Repos {
		if phase, ok := active[data.Repos[i].Name]; ok {
			data.Repos[i].Activity = &phase
		}
	}
}

func activitySummary(total int, phases map[string]int) string {
	if total == 0 {
		return ""
	}
	for _, phase := range []activity{{Kind: activityRefreshing, Label: "Refreshing"}, {Kind: "syncing", Label: "Syncing"}, {Kind: "starting", Label: "Starting"}} {
		if phases[phase.Kind] == total {
			return phase.Label + " " + repositories(total)
		}
	}
	return fmt.Sprintf("%d repositories active", total)
}
