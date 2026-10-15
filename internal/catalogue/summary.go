package catalogue

import (
	"context"
	"errors"
	"time"
)

type SummaryQuery struct {
	SourceID string `json:"source_id"`
	Genre    string `json:"genre"`
	Text     string `json:"text"`
	Limit    int    `json:"limit"`
}
type Coverage struct {
	Name   string `json:"name"`
	Tracks int    `json:"tracks"`
}
type Summary struct {
	CatalogueSummary                                bool         `json:"catalogue_summary"`
	Scope                                           SummaryQuery `json:"scope"`
	Tracks, Albums, Artists, Genres, Sources        int
	UnknownDurations, UnknownArtists, UnknownGenres int
	KnownSeconds                                    int64
	TopGenres, TopArtists                           []Coverage
}
type SummaryRepository interface {
	Summary(context.Context, SummaryQuery) (Summary, error)
}
type SummaryService struct{ Repo SummaryRepository }

func (s SummaryService) Summarize(ctx context.Context, q SummaryQuery) (Summary, error) {
	if q.Limit == 0 {
		q.Limit = 12
	}
	if q.Limit < 1 || q.Limit > 20 || len(q.SourceID) > 128 || len(q.Genre) > 4096 || len(q.Text) > 200 {
		return Summary{}, errors.New("Use a summary limit from 1 to 20 and bounded source, genre and text filters")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.Repo.Summary(ctx, q)
}
