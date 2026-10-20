package web

import (
	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/inference"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
	"time"
)

// pageView contains only the stable shell and station navigation context.
// Feature models embed it; they do not carry fields from unrelated screens.
type pageView struct {
	ActiveIncidents                               int
	Page, Title, CSRF, Notice, Section, StationID string
	PlaylistID, AssistantSection                  string
	Station                                       station.Station
	BroadcastEnabled                              bool
}

func (v pageView) common() pageView   { return v }
func (v pageView) player() playerView { return playerView{Station: v.Station} }

type playerView struct {
	Station                   station.Station
	Broadcast                 broadcast.State
	Public                    bool
	PublicCurrent, PublicNext cardTrack
}

type playlistChoices struct {
	Stations             []station.Station
	PlaylistOptions      []playlistOption
	StationPlaylistCount int
}
type catalogueChoices struct {
	Facets catalogue.Facets
	Albums []albumOption
}
type stationDirectoryView struct {
	pageView
	playlistChoices
	StationCards []stationCard
}
type stationSourceOption struct {
	ID, Name, Host string
	Selected       bool
}
type stationPageView struct {
	SourceOptions []stationSourceOption
	Operations    operationSummary
	pageView
	Broadcast                        broadcast.State
	StreamURL                        string
	StationRepeatSupplied            bool
	StationRepeatInput, StationError string
	StationLogoDraft                 string
	StationLogoGeneration            bool
	StationPlaylistCount             int
}

func (v stationPageView) player() playerView {
	return playerView{Station: v.Station, Broadcast: v.Broadcast}
}

type sourcePageView struct {
	pageView
	Source                            sources.Config
	SourceTab, SourceHost             string
	Sources                           []sources.Config
	SourceCards                       []sourceCard
	Catalogue                         catalogue.Result
	SourceAlbums                      int
	AlbumCards                        []sourceAlbumCard
	AlbumName, AlbumURL, AlbumBack    string
	AlbumItem                         content.AudioItem
	AlbumRows                         []trackRow
	Pagination                        pagination
	SourceDiscoveries                 []discoveredSource
	SourceDiscoveryError, SourceError string
	SourceDiscoveryAttempted          bool
}
type cataloguePageView struct {
	pageView
	playlistChoices
	catalogueChoices
	Sources                                                                []sources.Config
	Catalogue                                                              catalogue.Result
	TargetPlaylist                                                         playlists.Playlist
	Query, Genre, Sort, Direction, AlbumChoice, SelectedPlaylist, ReturnTo string
	Rows                                                                   []trackRow
	Pagination                                                             pagination
}
type playlistPageView struct {
	pageView
	playlistChoices
	catalogueChoices
	Playlist                                                   playlists.Playlist
	PlaylistTab, PlaylistDuration, ReturnTo, BroadcastPlaylist string
	PlaylistResult                                             playlists.Result
	ExcludedTracks                                             int
	PlaylistRefreshNotice                                      string
	TrackSearch                                                string
	Sources                                                    []sources.Config
	SavedRestrictions                                          []ruleRow
	RuleGroups                                                 []ruleChoiceGroup
	Filter                                                     playlists.Filter
	RuleID, RuleError, YearFromInput, YearToInput              string
	Genres                                                     []genreOption
	Rows                                                       []trackRow
	Pagination                                                 pagination
}
type schedulePageView struct {
	Episode                                               episodes.Episode
	EpisodePlanChanged                                    bool
	EpisodeCanCancel                                      bool
	EpisodeEditing                                        bool
	EpisodeQueue                                          []episodes.RunningTrack
	EpisodeQueueLive                                      bool
	EpisodePlayed                                         []episodes.Track
	EpisodePagination                                     pagination
	EpisodeStart, Week, WeekLabel, PreviousWeek, NextWeek string
	EpisodeStarts                                         map[string]string
	EpisodeExclusions                                     map[string]bool
	SelectedEpisodeID                                     string
	EpisodeError                                          string
	pageView
	playlistChoices
	DJs                                         []djs.Profile
	Show                                        schedule.Show
	Shows                                       []schedule.Show
	ShowError, ScheduleMode, ShowStart, ShowEnd string
	ScheduleDays                                []scheduleDay
	ShowDays                                    map[int]bool
	ScheduleHours                               []scheduleHour
	ScheduleCards                               []scheduleCard
}
type djPageView struct {
	pageView
	DJ                            djs.Profile
	DJCards                       []djCard
	DJs                           []djs.Profile
	DJVoices                      []djs.Voice
	SpeechCatalog                 djs.VoiceCatalog
	SpeechVoice, SpeechVoiceError string
	ChatterboxVoice               bool
	SpeechFields                  bool
	SpeechEndpoints               []djs.SpeechEndpoint
	SpeechPreference              djs.SpeechPreference
	SpeechDraftJSON               string
	DJError, DJMode, ExampleVibe  string
	DJPhotoDraft                  string
	DJPhotoGeneration             bool
	AssistantSettings             producer.Settings
}
type mixerPageView struct {
	pageView
	NormaliseMusic                    bool
	MixerChannels                     []mixerChannel
	MusicVolume, DJVolume, MixerError string
}
type assistantPageView struct {
	pageView
	AssistantMessages                                                 []assistantMessage
	AssistantState, AssistantActivity, AssistantTiming                string
	AssistantMode, AssistantError, AssistantInput, AssistantRequestID string
	AssistantSettings                                                 producer.Settings
	AIConnections                                                     []ai.Connection
	AIConnection                                                      ai.Connection
	AICheck                                                           inference.Check
	AIRoles                                                           []aiRoleView
	AIConnectionMode, AIConnectionSaved                               bool
	Conversation                                                      producer.Conversation
	Conversations                                                     []producer.Conversation
}
type audienceQueueView struct {
	pageView
	Broadcast                          broadcast.State
	AudienceMessages                   []audience.Message
	AudienceTotal                      int
	AudienceStatuses                   []audienceStatusChoice
	AudienceFilter, AudienceRefreshURL string
	Pagination                         pagination
}

func (v audienceQueueView) player() playerView {
	return playerView{Station: v.Station, Broadcast: v.Broadcast}
}

// Listener pages share their public programme and contribution context.
type publicPageView struct {
	LiveDeadline time.Time
	pageView
	Public                                               bool
	StationCards                                         []stationCard
	Broadcast                                            broadcast.State
	StreamURL                                            string
	PublicDays, PublicDayLinks                           []publicDay
	PublicDate, AudiencePanel, AudienceDay, AudienceBase string
	PublicShow                                           publicShow
	PublicCurrent, PublicNext                            cardTrack
	audienceFormData
}
type audienceFormData struct {
	AudienceMode, AudienceKind, AudienceKey                  string
	AudienceQuery, AudienceToken                             string
	AudienceShow                                             schedule.Occurrence
	AudienceName, AudienceText, AudienceTrack, AudienceError string
	AudienceConsent                                          bool
	AudienceShows                                            []schedule.Occurrence
	AudienceTracks                                           []content.AudioItem
	AudienceMoreTracks                                       bool
}

func (v publicPageView) Player() playerView {
	return playerView{Station: v.Station, Broadcast: v.Broadcast, Public: true, PublicCurrent: v.PublicCurrent, PublicNext: v.PublicNext}
}

// formActionsView is the shared routine editing footer, independent of feature data.
type formActionsView struct {
	CancelURL string
	Label     string
}

func formActions(cancelURL, label string) formActionsView {
	return formActionsView{CancelURL: cancelURL, Label: label}
}

type ruleChoiceGroup struct {
	Name, Kind string
	Options    []ruleChoice
}
type ruleChoice struct {
	Name, Value       string
	Selected, Missing bool
}
