package dispatch

import "go.bitfoo.de/forgemirror/internal/webhook"

func eventTrigger(e webhook.Event) string {
	if e.Retry {
		return "retry"
	}
	switch e.Kind {
	case webhook.EventPush:
		return "webhook"
	case webhook.EventScheduled:
		return "schedule"
	case webhook.EventDelete:
		return "source deletion"
	case webhook.EventSync:
		return "manual sync"
	case webhook.EventRepairLFS:
		return "LFS repair"
	case webhook.EventRefresh:
		return "refresh"
	default:
		return "unknown"
	}
}
