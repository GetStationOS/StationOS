// Package catalogue coordinates bounded, reproducible imports without owning configuration.
package catalogue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
)

type Query struct {
	ItemID           string
	StationID        string
	Artist, Composer string
	YearFrom, YearTo int
	Genre            string
	Album            string
	SourceID         string
	Sort             string
	Descending       bool
	Text             string
	Offset           int
	Limit            int
}

func (q Query) Validate() error {
	if len(q.ItemID) > 128 {
		return errors.New("invalid catalogue item")
	}
	if len(q.Artist) > 4096 || len(q.Composer) > 4096 || q.YearFrom < 0 || q.YearFrom > 9999 || q.YearTo < 0 || q.YearTo > 9999 || (q.YearFrom > 0 && q.YearTo > 0 && q.YearFrom > q.YearTo) {
		return errors.New("invalid catalogue credit or year filter")
	}
	if q.Offset < 0 || q.Offset > 1000000 || q.Limit < 1 || q.Limit > 100 || len(q.Text) > 200 || len(q.Genre) > 4096 || len(q.Album) > 4096 || len(q.SourceID) > 128 || len(q.StationID) > 128 {
		return errors.New("invalid catalogue query")
	}
	switch q.Sort {
	case "", "title", "artist", "album", "duration":
	default:
		return errors.New("invalid catalogue sort")
	}
	return nil
}

type Result struct {
	Items []content.AudioItem `json:"items"`
	Total int                 `json:"total"`
}
type Album struct {
	SourceID   string
	SourceName string
	Name       string
	Tracks     int
}
type Facets struct {
	Genres             []string
	Artists, Composers []string
	Years, Decades     []string
	Albums             []Album
}

type Repository interface {
	Replace(context.Context, sources.Config, []content.AudioItem) error
	Search(context.Context, Query) (Result, error)
	Clear(context.Context, string) error
	Facets(context.Context, ...string) (Facets, error)
}
type Sync struct {
	Sources   sources.Repository
	Catalogue Repository
	Factory   sources.Factory
	Logger    *slog.Logger
	// MaxSnapshotBytes bounds a single import; zero uses the 64 MiB default.
	MaxSnapshotBytes int
}

func (s Sync) Run(ctx context.Context, id string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	c, err := s.Sources.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	started := time.Now()
	items, err := s.fetch(ctx, c)
	if err == nil {
		err = s.Catalogue.Replace(ctx, c, items)
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if err != nil {
		logger.Error("catalogue import failed", "source_id", id, "elapsed", time.Since(started), "error", err)
		return 0, err
	}
	logger.Info("catalogue import complete", "source_id", id, "items", len(items), "elapsed", time.Since(started))
	return len(items), nil
}
func (s Sync) fetch(ctx context.Context, c sources.Config) ([]content.AudioItem, error) {
	p, err := s.Factory(ctx, c)
	if err != nil {
		return nil, err
	}
	if !p.Profile().Capabilities.Browse {
		return nil, sources.ErrUnsupported
	}
	root := "0"
	if provider, ok := p.(sources.CatalogueRootProvider); ok {
		root, err = provider.CatalogueRoot(ctx)
		if err != nil {
			return nil, err
		}
		if root == "" {
			return nil, errors.New("source returned an empty catalogue root")
		}
	}
	queue := []string{root}
	seen := map[string]bool{root: true}
	items := map[string]content.AudioItem{}
	out := []content.AudioItem{}
	pages := 0
	bytes := 0
	limit := s.MaxSnapshotBytes
	if limit <= 0 {
		limit = 64 << 20
	}
	for len(queue) > 0 {
		object := queue[0]
		queue = queue[1:]
		offset := 0
		total := -1
		update := ""
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			pages++
			if pages > 10000 {
				return nil, errors.New("source exceeds page limit")
			}
			page, err := p.Browse(ctx, object, offset, 100)
			if err != nil {
				return nil, err
			}
			if page.Total < 0 || page.Total > 100000 || len(page.Entries) > 100 || offset+len(page.Entries) > page.Total {
				return nil, errors.New("inconsistent source pagination")
			}
			if total == -1 {
				total = page.Total
				update = page.UpdateID
			} else if total != page.Total || update != page.UpdateID {
				return nil, errors.New("source changed during pagination; retry")
			}
			for _, entry := range page.Entries {
				if entry.ContainerID != "" && !seen[entry.ContainerID] {
					seen[entry.ContainerID] = true
					queue = append(queue, entry.ContainerID)
					if len(seen) > 10000 {
						return nil, errors.New("source exceeds container limit")
					}
				}
				if entry.Item == nil {
					continue
				}
				a := *entry.Item
				a.SourceID = c.ID
				a.ID = content.ID(c.ID, a.SourceItemID)
				if err := a.Validate(); err != nil {
					return nil, fmt.Errorf("invalid imported item: %w", err)
				}
				if _, ok := items[a.ID]; !ok {
					encoded, err := json.Marshal(a)
					if err != nil {
						return nil, err
					}
					bytes += len(encoded)
					if bytes > limit {
						return nil, errors.New("source snapshot exceeds metadata byte limit")
					}
					items[a.ID] = a
					out = append(out, a)
				}
				if len(out) > 100000 {
					return nil, errors.New("source exceeds item limit")
				}
			}
			offset += len(page.Entries)
			if offset == page.Total {
				break
			}
			if len(page.Entries) == 0 {
				return nil, errors.New("source pagination made no progress")
			}
		}
	}
	return out, nil
}
