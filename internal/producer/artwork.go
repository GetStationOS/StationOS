package producer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"stationos.local/stationos/internal/artwork"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/station"
)

type ImageRequest struct {
	Kind, Name, Description, Background, Vibe, Brief string
}
type Image struct {
	Data  []byte
	Usage Usage
}
type ImageGenerator interface {
	Generate(context.Context, ImageRequest) (Image, error)
}
type imageArgs struct {
	ID      string `json:"id"`
	Brief   string `json:"brief"`
	Replace bool   `json:"replace"`
}

func registerArtwork(r *Registry, d Dependencies) {
	slots := make(chan struct{}, 1)
	generate := func(ctx context.Context, kind string, a imageArgs) (Result, error) {
		if d.Images == nil {
			return Result{}, errors.New("Artwork generation is unavailable")
		}
		if a.ID == "" || len(a.Brief) > 2000 {
			return Result{}, errors.New("Provide an existing ID and a visual brief of up to 2000 characters")
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		request := ImageRequest{Kind: kind, Brief: strings.TrimSpace(a.Brief)}
		switch kind {
		case "station":
			st, err := d.Stations.Get(ctx, a.ID)
			if err != nil {
				return Result{}, err
			}
			if st.LogoVersion != "" && !a.Replace {
				return saved(st, "Station already has a logo; kept existing artwork", "/producer/stations/"+st.ID)
			}
			request.Name = st.Name
			request.Description = st.Description
		case "dj":
			dj, err := d.DJs.Get(ctx, a.ID)
			if err != nil {
				return Result{}, err
			}
			if dj.PortraitURL != "" && !a.Replace {
				return saved(dj, "DJ already has a photo; kept existing artwork", "/producer/djs/"+dj.ID)
			}
			request.Name = dj.Name
			request.Background = djSummary(dj.Background, 4000)
			request.Vibe = djSummary(dj.Vibe, 2000)
		}
		image, err := d.Images.Generate(ctx, request)
		if err != nil {
			return Result{Usage: image.Usage, Error: err.Error()}, nil
		}
		data, err := artwork.Normalize(image.Data)
		if err != nil {
			return Result{Usage: image.Usage, Error: "Generated artwork could not be saved: " + err.Error()}, nil
		}
		var result Result
		if kind == "station" {
			// Re-read after generation to preserve edits made while the API was working.
			st, e := d.Stations.Get(ctx, a.ID)
			if e != nil {
				return Result{Usage: image.Usage, Error: e.Error()}, nil
			}
			if st.LogoVersion != "" && !a.Replace {
				return Result{Data: st, Summary: "Kept the logo added while generation was running", URL: "/producer/stations/" + st.ID, Usage: image.Usage}, nil
			}
			st.Logo = &station.LogoChange{Data: data}
			st, e = (station.Service{Repo: d.Stations}).Save(ctx, st)
			if e != nil {
				return Result{Usage: image.Usage, Error: e.Error()}, nil
			}
			result, _ = saved(st, "Saved station logo: "+st.Name, "/producer/stations/"+st.ID)
		} else {
			dj, e := d.DJs.Get(ctx, a.ID)
			if e != nil {
				return Result{Usage: image.Usage, Error: e.Error()}, nil
			}
			if dj.PortraitURL != "" && !a.Replace {
				return Result{Data: dj, Summary: "Kept the photo added while generation was running", URL: "/producer/djs/" + dj.ID, Usage: image.Usage}, nil
			}
			dj.Photo = &djs.PhotoChange{Data: data}
			dj, e = (djs.Service{Repo: d.DJs}).Save(ctx, dj, djs.KeyChange{})
			if e != nil {
				return Result{Usage: image.Usage, Error: e.Error()}, nil
			}
			result, _ = saved(dj, "Saved DJ portrait: "+dj.Name, "/producer/djs/"+dj.ID)
		}
		result.Usage = image.Usage
		return result, nil
	}
	register(r, "generate_station_logo", "Generate and save a logo for an existing station using its identity and an optional visual brief. Existing logos are preserved unless replace=true was explicitly requested. Never recreate a station to retry failed artwork.", true, func(ctx context.Context, a imageArgs) (Result, error) { return generate(ctx, "station", a) })
	register(r, "generate_dj_photo", "Generate and save a fictional portrait for an existing DJ using its background and voice direction, with an optional visual brief. Existing photos are preserved unless replace=true was explicitly requested. Never recreate a DJ to retry failed artwork.", true, func(ctx context.Context, a imageArgs) (Result, error) { return generate(ctx, "dj", a) })
	if d.Images == nil {
		return
	}
	r.artwork = func(ctx context.Context, name string, args json.RawMessage, result Result) Result {
		if d.Images == nil || (name != "save_station" && name != "save_dj") {
			return result
		}
		var supplied struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(args, &supplied)
		if supplied.ID != "" {
			return result
		}
		kind, id := "", ""
		switch v := result.Data.(type) {
		case station.Station:
			kind = "station"
			id = v.ID
		case djs.Profile:
			kind = "dj"
			id = v.ID
		}
		if id == "" {
			return result
		}
		generated, err := generate(ctx, kind, imageArgs{ID: id})
		result.Usage = generated.Usage
		if err != nil {
			result.ArtworkError = err.Error()
		} else {
			result.ArtworkError = generated.Error
		}
		if result.ArtworkError != "" {
			result.Summary += ". Artwork not saved: " + result.ArtworkError + ". The entity is saved; retry only its artwork using ID " + id + "."
		} else {
			result.Data = generated.Data
			result.Summary += ". " + generated.Summary
		}
		return result
	}
}
