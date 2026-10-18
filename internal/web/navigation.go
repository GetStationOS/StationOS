package web

import "fmt"

type headingView struct {
	Title, Metadata string
	Actions         []headingAction
}
type headingAction struct {
	Label, URL, Hint, Icon string
	Method, CSRF           string
	Secondary              bool
	Fields                 []field
}

func heading(title string) headingView { return headingView{Title: title} }
func headingLink(title, metadata, label, destination string) headingView {
	return headingView{Title: title, Metadata: metadata, Actions: []headingAction{{Label: label, URL: destination}}}
}
func scheduleHeading(v schedulePageView) headingView {
	return headingLink(v.Title, "Repeats weekly · "+v.Station.Timezone, "+ Add show", "/producer/stations/"+v.Station.ID+"/schedule/new")
}
func playlistsHeading(v playlistPageView) headingView {
	if len(v.Stations) == 0 {
		return heading(v.Title)
	}
	destination := "/producer/playlists/new"
	if v.Station.ID != "" {
		destination += "?station=" + v.Station.ID
	}
	return headingLink(v.Title, "", "+ Add playlist", destination)
}
func playlistHeading(v playlistPageView) headingView {
	out := heading(v.Title)
	if v.PlaylistTab == "delete" {
		return out
	}
	out.Metadata = fmt.Sprintf("%d tracks", v.PlaylistResult.Total)
	if v.ExcludedTracks > 0 {
		out.Metadata += fmt.Sprintf(" · %d excluded", v.ExcludedTracks)
	}
	out.Metadata += " · " + v.PlaylistDuration
	if v.PlaylistResult.UnknownDurations > 0 {
		out.Metadata += fmt.Sprintf(" · %d durations unknown", v.PlaylistResult.UnknownDurations)
	}
	out.Actions = []headingAction{{Label: "Refresh", URL: "/producer/playlists/" + v.Playlist.ID + "/refresh", Method: "post", CSRF: v.CSRF, Secondary: true, Icon: "refresh", Hint: "Recheck saved playlist rules", Fields: []field{{"return_to", v.ReturnTo}}}}
	return out
}
func assistantHeading(v assistantPageView) headingView {
	out := heading(v.Title)
	if v.AssistantMode == "chat" {
		out.Actions = []headingAction{{Label: "+ New conversation", URL: "/producer/assistant/conversations", Method: "post", CSRF: v.CSRF, Fields: []field{{"station", v.StationID}}}}
	} else if !v.AIConnectionMode {
		out = headingLink(v.Title, "", "+ Add endpoint", "/producer/assistant/endpoints?connection=new")
	}
	return out
}

type breadcrumb struct {
	Label, URL string
	Current    bool
}

func crumb(label, destination string) breadcrumb {
	return breadcrumb{Label: label, URL: destination, Current: destination == ""}
}
func breadcrumbs(items ...breadcrumb) []breadcrumb { return items }
func stationBreadcrumbs(v pageFrame) []breadcrumb {
	out := []breadcrumb{crumb("Stations", "/producer"), crumb(v.Station.Name, "/producer/stations/"+v.Station.ID)}
	if v.PlaylistID != "" {
		out = append(out, crumb("Playlists", "/producer/playlists?station="+v.Station.ID))
	} else if v.Section == "overview" {
		out[1].Current = true
	}
	return out
}
func sourceBreadcrumbs(v sourcePageView) []breadcrumb {
	if v.SourceTab == "settings" {
		return breadcrumbs(crumb("Sources", "/producer/sources"), crumb(v.Source.Name, "/producer/sources/"+v.Source.ID), crumb("Settings", ""))
	}
	return breadcrumbs(crumb("Sources", "/producer/sources"), crumb(v.Source.Name, ""))
}
func publicBreadcrumbs(v publicPageView) []breadcrumb {
	out := breadcrumbs(crumb("Radio stations", "/"))
	if v.PublicShow.Name != "" {
		return append(out, crumb(v.Station.Name, "/radio/"+v.Station.StreamSlug), crumb(v.PublicShow.Name, ""))
	}
	return append(out, crumb(v.Station.Name, ""))
}

type workspaceTab struct {
	Label, URL, Key string
	Current         bool
	IndicatorURL    string
	Incidents       int
}
type workspaceTabsView struct {
	Label, ScrollKey string
	Items            []workspaceTab
}

func stationTabs(v pageFrame) workspaceTabsView {
	base := "/producer/stations/" + v.Station.ID
	out := workspaceTabsView{Label: "Station navigation", ScrollKey: "station:" + v.Station.ID}
	for _, item := range []struct{ label, key, path string }{{"Overview", "overview", ""}, {"Schedule", "schedule", "/schedule"}, {"Audience", "audience", "/audience"}, {"Timeline", "timeline", "/timeline"}, {"Incidents", "incidents", "/incidents"}, {"Mixer", "mixer", "/mixer"}, {"Playlists", "playlists", ""}, {"Settings", "settings", "/settings"}} {
		tab := workspaceTab{Label: item.label, URL: base + item.path, Current: v.Section == item.key}
		if item.key == "playlists" {
			tab.URL = "/producer/playlists?station=" + v.Station.ID
		}
		if item.key == "incidents" {
			tab.IndicatorURL = base + "/operations/indicator"
			tab.Incidents = v.ActiveIncidents
		}
		out.Items = append(out.Items, tab)
	}
	return out
}
func sourceTabs(v sourcePageView) workspaceTabsView {
	base := "/producer/sources/" + v.Source.ID
	return workspaceTabsView{Label: "Source navigation", ScrollKey: "source:" + v.Source.ID, Items: []workspaceTab{{Label: "Overview", URL: base, Current: v.SourceTab == "overview"}, {Label: "Settings", URL: base + "/settings", Current: v.SourceTab == "settings"}}}
}
func playlistTabs(v playlistPageView) workspaceTabsView {
	base := "/producer/playlists/" + v.Playlist.ID
	return workspaceTabsView{Label: "Playlist navigation", ScrollKey: "playlist:" + v.Playlist.ID, Items: []workspaceTab{{Label: "Selections", URL: base, Current: v.PlaylistTab == "selections"}, {Label: "Tracks", URL: base + "/tracks", Current: v.PlaylistTab == "tracks"}, {Label: "Details", URL: base + "/settings", Current: v.PlaylistTab == "details"}}}
}
func assistantTabs(mode string) workspaceTabsView {
	out := workspaceTabsView{Label: "Assistant navigation", ScrollKey: "assistant"}
	for _, item := range []struct{ label, key, path string }{{"Chats", "chat", ""}, {"Prompts", "prompts", "/prompts"}, {"Models", "settings", "/settings"}, {"Endpoints", "endpoints", "/endpoints"}} {
		key := item.key
		if key == "chat" {
			key = "chats"
		} else if key == "settings" {
			key = "models"
		}
		out.Items = append(out.Items, workspaceTab{Label: item.label, URL: "/producer/assistant" + item.path, Key: key, Current: mode == item.key || item.key == "endpoints" && mode == "delete-connection"})
	}
	return out
}
