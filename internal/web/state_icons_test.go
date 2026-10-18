package web

import (
	"stationos.local/stationos/internal/audience"
	"strings"
	"testing"
)

func TestAudienceStateIconsCoverEveryStateAndRetainReasons(t *testing.T) {
	for _, tc := range []struct {
		state             audience.Status
		label, icon, tone string
	}{
		{audience.Pending, "Awaiting AI review", "ai", "warning"},
		{audience.ReviewNeeded, "Needs producer review", "review", "warning"},
		{audience.Approved, "Approved", "completed", "accent"},
		{audience.Rejected, "Dismissed", "failed", "neutral"},
		{audience.Used, "Broadcast", "transmitting", "info"},
		{audience.Expired, "Expired", "expired", "neutral"},
		{audience.Status("future"), "Unknown status", "unknown", "neutral"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			v := audienceStateIcon(audience.Message{Status: tc.state, Reason: "<script>review reason</script>"})
			if v.Label != tc.label || v.Icon != tc.icon || v.Tone != tc.tone || !strings.Contains(v.Description, "review reason") || v.Detail != tc.label {
				t.Fatal(v)
			}
			body := navigationHTML(t, "state-icon", v)
			for _, want := range []string{`type="button"`, `aria-label="` + tc.label + `"`, `data-state-detail=`, `&lt;script&gt;review reason&lt;/script&gt;`} {
				if !strings.Contains(body, want) {
					t.Fatal(want, body)
				}
			}
			if strings.Contains(body, "<script>") {
				t.Fatal("state detail must be escaped", body)
			}
		})
	}
}
func TestTimelineStateIconKeepsItsLabelAndTone(t *testing.T) {
	for _, tc := range []struct{ state, tone string }{{"completed", "success"}, {"recovered", "success"}, {"transmitting", "accent"}, {"failed", "error"}, {"interrupted", "error"}, {"warning", "warning"}, {"unknown", "warning"}} {
		v := historyStateIcon(operationalEvent{OutcomeLabel: "Outcome", OutcomeIcon: tc.state, OutcomeStyle: tc.state})
		if v.Label != "Outcome" || v.Detail != "Outcome" || v.Tone != tc.tone {
			t.Fatal(tc, v)
		}
	}
}
