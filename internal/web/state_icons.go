package web

import (
	"stationos.local/stationos/internal/audience"
	"strings"
)

type stateIconView struct{ Label, Detail, Icon, Tone, Description string }

func stateIcon(label, detail, icon, tone string) stateIconView {
	switch tone {
	case "completed", "recovered":
		tone = "success"
	case "transmitting", "approved":
		tone = "accent"
	case "failed", "interrupted":
		tone = "error"
	case "unknown":
		tone = "warning"
	case "error", "warning", "success", "accent", "info":
	default:
		tone = "neutral"
	}
	if detail == "" {
		detail = label
	}
	return stateIconView{Label: label, Detail: detail, Icon: icon, Tone: tone}
}
func audienceStateIcon(m audience.Message) stateIconView {
	var label, icon, tone string
	switch m.Status {
	case audience.Pending:
		label, icon, tone = "Awaiting AI review", "ai", "warning"
	case audience.ReviewNeeded:
		label, icon, tone = "Needs producer review", "review", "warning"
	case audience.Approved:
		label, icon, tone = "Approved", "completed", "accent"
	case audience.Rejected:
		label, icon, tone = "Dismissed", "failed", "neutral"
	case audience.Used:
		label, icon, tone = "Broadcast", "transmitting", "info"
	case audience.Expired:
		label, icon, tone = "Expired", "expired", "neutral"
	default:
		label, icon, tone = "Unknown status", "unknown", "neutral"
	}
	out := stateIcon(label, label, icon, tone)
	out.Description = strings.TrimSpace(m.Reason)
	return out
}
func historyStateIcon(v operationalEvent) stateIconView {
	return stateIcon(v.OutcomeLabel, v.OutcomeLabel, v.OutcomeIcon, v.OutcomeStyle)
}
func incidentStateIcon(v operationalIncident) stateIconView {
	detail := v.StatusLabel + " · " + v.Severity
	if !v.Recovered.IsZero() {
		detail = "Recovered " + v.RecoveredLabel
	}
	out := stateIcon(v.StatusLabel, v.StatusLabel, v.Icon, v.Tone)
	out.Description = detail
	return out
}
