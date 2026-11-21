package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type toastNotice struct {
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

// activityEvent makes the overview tiles re-fetch at once: an action response
// only re-renders its own fragment, and a short run can start and finish
// between two polls without the Running tile ever showing it.
const activityEvent = "forgemirror:activity"

// Header events acknowledge requests independently of deferred/polled fragments.
func triggerNotice(w http.ResponseWriter, message string, failed, activity bool) error {
	kind := "success"
	if failed {
		kind = "error"
	}
	events := map[string]any{"forgemirror:notice": toastNotice{Message: message, Kind: kind}}
	if activity {
		events[activityEvent] = true
	}
	payload, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("encoding action notification: %w", err)
	}
	w.Header().Set("Hx-Trigger", string(payload))
	return nil
}
