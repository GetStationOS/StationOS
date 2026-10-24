package producer

import (
	"fmt"
	"sort"
	"strings"
)

// Tool availability is stable across recovery and read counts. Work areas are
// optional context helpers, never permissions or prerequisites for an action.
func agentTools(tools []Tool) []Tool {
	out := make([]Tool, 0, len(tools)+1)
	for _, tool := range tools {
		// Older transcripts may still contain this call, but new programmes have a
		// single flat creation interface with clock time and duration.
		if tool.Name != "create_shows" {
			out = append(out, tool)
		}
	}
	return append(out, inspectionTool())
}

func stalledMessage(checks map[string]int, lastError string) string {
	type check struct {
		name  string
		count int
	}
	ordered := []check{}
	for name, count := range checks {
		ordered = append(ordered, check{name, count})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].count == ordered[j].count {
			return ordered[i].name < ordered[j].name
		}
		return ordered[i].count > ordered[j].count
	})
	parts := []string{}
	for _, entry := range ordered[:min(3, len(ordered))] {
		parts = append(parts, fmt.Sprintf("%s (%d)", entry.name, entry.count))
	}
	message := "The assistant could not turn its checks into saved changes, including after recovery with saved configuration."
	if len(parts) > 0 {
		message += " Most frequent checks: " + strings.Join(parts, ", ") + "."
	}
	if lastError != "" {
		message += " Last action error: " + lastError
	}
	return message + " Completed actions, the original brief and remaining work are saved."
}
