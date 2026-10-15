package producer

import (
	"fmt"
	"sort"
	"strings"
)

// Recovery changes the available actions, not just the wording of the prompt.
// This limits checks between writes without inventing an editorial decision.
func focusedTools(tools []Tool, focus string, reads map[string]int) []Tool {
	if focus == "" {
		return tools
	}
	allowed := map[string]bool{"save_task_progress": true}
	switch focus {
	case "playlists":
		for _, name := range []string{"summarize_music", "explore_music", "search_music", "get_playlist", "save_playlist", "add_playlist_rule", "replace_playlist_rules", "remove_playlist_rule", "select_tracks", "assign_default_playlist"} {
			allowed[name] = true
		}
	case "schedule":
		for _, name := range []string{"list_djs", "get_dj", "save_dj", "get_playlist", "save_show", "create_shows", "delete_show", "assign_default_playlist"} {
			allowed[name] = true
		}
	case "station":
		for _, name := range []string{"get_station", "save_station", "get_status", "get_mixer", "save_mixer", "assign_default_playlist"} {
			allowed[name] = true
		}
	case "presenters":
		for _, name := range []string{"list_djs", "get_dj", "save_dj", "list_voices", "generate_dj_photo"} {
			allowed[name] = true
		}
	case "sources":
		for _, name := range []string{"list_sources", "save_source", "refresh_source", "summarize_music"} {
			allowed[name] = true
		}
	case "audience":
		for _, tool := range tools {
			if strings.Contains(tool.Name, "audience") {
				allowed[tool.Name] = true
			}
		}
	case "review":
		for _, tool := range tools {
			if !tool.Mutates {
				allowed[tool.Name] = true
			}
		}
		// Unknown scope must select a work step, rather than exposing everything.
	}

	out := []Tool{}
	for _, tool := range tools {
		if allowed[tool.Name] && (tool.Mutates || reads[tool.Name] < 2) {
			out = append(out, tool)
		}
	}
	return append(out, workStepTool())
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
	message := "The assistant could not turn its checks into saved changes, including after focused recovery."
	if len(parts) > 0 {
		message += " Most frequent checks: " + strings.Join(parts, ", ") + "."
	}
	if lastError != "" {
		message += " Last action error: " + lastError
	}
	return message + " Completed actions, the original brief and remaining work are saved."
}

func resumingStalled(c Conversation) bool {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == "assistant" {
			text := c.Messages[i].Text
			return strings.HasPrefix(text, "The assistant stopped making progress.") || strings.HasPrefix(text, "The assistant could not turn its checks into saved changes")
		}
	}
	return false
}
