package sources

import (
	"context"
	"errors"
	"testing"
)

type repo struct {
	v   Config
	err error
}

func (r *repo) Get(_ context.Context, id string) (Config, error) {
	if id != r.v.ID {
		return Config{}, ErrNotFound
	}
	return r.v, nil
}
func (r *repo) List(context.Context) ([]Config, error) { return []Config{r.v}, nil }
func (r *repo) Save(_ context.Context, c Config) error {
	if r.err != nil {
		return r.err
	}
	r.v = c
	return nil
}
func TestConfiguration(t *testing.T) {
	ctx := context.Background()
	r := &repo{}
	s := Service{Repo: r}
	c, err := s.Save(ctx, Config{Name: " Music ", Kind: "upnp", Endpoint: "http://server:9790/desc"})
	if err != nil || len(c.ID) != 32 || c.Revision != 1 || c.Name != "Music" {
		t.Fatal(c, err)
	}
	c.Name = "Updated"
	c, err = s.Save(ctx, c)
	if err != nil || c.Revision != 2 {
		t.Fatal(c, err)
	}
	c.ID = "missing"
	if _, err = s.Save(ctx, c); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	r.err = errors.New("disk error")
	c.ID = ""
	if _, err = s.Save(ctx, c); !errors.Is(err, r.err) {
		t.Fatal(err)
	}
}
func TestUnsafeConfiguration(t *testing.T) {
	for _, endpoint := range []string{"file:///etc/passwd", "http://user:password@server/x", "http://server/x#fragment", "https://", "gopher://server"} {
		c := Config{Name: "Music", Kind: "upnp", Endpoint: endpoint}
		if c.Validate() == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}
