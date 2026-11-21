package ui

import "time"

// CompactDuration exposes the header uptime format to the external tests.
func CompactDuration(d time.Duration) string { return compactDuration(d) }
