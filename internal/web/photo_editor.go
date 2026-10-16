package web

// photoEditorView describes the same upload/generate/remove control for station
// logos and DJ portraits. Each parent form owns its draft and persistence.
type photoEditorView struct {
	ID, Action, DraftName, GenerateName                       string
	UploadLabel, GenerateLabel, RemoveLabel, Alt, DraftStatus string
	SavedURL, DefaultURL, Draft                               string
	CanGenerate, Removable                                    bool
}

func stationPhotoEditor(v stationPageView) photoEditorView {
	return photoEditorView{
		ID:            "station-logo-editor",
		Action:        "/producer/station",
		DraftName:     "generated_logo",
		GenerateName:  "generate_logo",
		UploadLabel:   "Upload logo",
		GenerateLabel: "Generate logo",
		RemoveLabel:   "Remove logo",
		Alt:           "Station logo",
		DraftStatus:   "Generated logo ready to save",
		SavedURL:      producerLogo(v.Station),
		DefaultURL:    "/static/station-logo.jpg",
		Draft:         v.StationLogoDraft,
		CanGenerate:   v.StationLogoGeneration,
		Removable:     v.Station.LogoVersion != "" || v.StationLogoDraft != "",
	}
}
func djPhotoEditor(v djPageView) photoEditorView {
	return photoEditorView{
		ID:            "dj-photo-editor",
		Action:        "/producer/djs",
		DraftName:     "generated_photo",
		GenerateName:  "generate_photo",
		UploadLabel:   "Upload photo",
		GenerateLabel: "Generate photo",
		RemoveLabel:   "Remove photo",
		Alt:           "DJ photo",
		DraftStatus:   "Generated photo ready to save",
		SavedURL:      v.DJ.PortraitURL,
		Draft:         v.DJPhotoDraft,
		CanGenerate:   v.DJPhotoGeneration,
		Removable:     v.DJ.PortraitURL != "" || v.DJPhotoDraft != "",
	}
}
