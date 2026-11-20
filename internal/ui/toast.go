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

// Header events acknowledge requests independently of deferred/polled fragments.
func triggerNotice(w http.ResponseWriter, message string, failed bool) error {
	kind := "success"
	if failed {
		kind = "error"
	}
	payload, err := json.Marshal(map[string]toastNotice{"forgemirror:notice": {Message: message, Kind: kind}})
	if err != nil {
		return fmt.Errorf("encoding action notification: %w", err)
	}
	w.Header().Set("Hx-Trigger", string(payload))
	return nil
}
