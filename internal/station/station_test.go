package station

import (
	"context"
	"errors"
	"testing"
)

type memory struct {
	v   map[string]Station
	err error
}

func (m *memory) Save(_ context.Context, s Station) error {
	if m.err != nil {
		return m.err
	}
	m.v[s.ID] = s
	return nil
}
func (m *memory) Get(_ context.Context, id string) (Station, error) {
	s, ok := m.v[id]
	if !ok {
		return Station{}, ErrNotFound
	}
	return s, nil
}
func (m *memory) List(context.Context) ([]Station, error) { return nil, m.err }
func (m *memory) Delete(_ context.Context, id string) error {
	if _, ok := m.v[id]; !ok {
		return ErrNotFound
	}
	delete(m.v, id)
	return nil
}
func TestStationLifecycle(t *testing.T) {
	m := &memory{v: map[string]Station{}}
	svc := Service{Repo: m}
	ctx := context.Background()
	s, err := svc.Save(ctx, Station{Name: " Test ", Timezone: "Europe/London", Locale: "en-GB"})
	if err != nil || len(s.ID) != 32 || s.Name != "Test" {
		t.Fatalf("%+v %v", s, err)
	}
	s.Name = "Updated"
	got, err := svc.Save(ctx, s)
	if err != nil || got.ID != s.ID || m.v[s.ID].Name != "Updated" {
		t.Fatal(got, err)
	}
	s.ID = "unknown"
	if _, err = svc.Save(ctx, s); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	m.err = errors.New("disk full")
	s.ID = ""
	if _, err = svc.Save(ctx, s); !errors.Is(err, m.err) {
		t.Fatal(err)
	}
}
func TestValidation(t *testing.T) {
	for _, s := range []Station{{Timezone: "UTC"}, {Name: "A"}, {Name: "A", Timezone: "Unknown/Place"}, {Name: "   ", Timezone: "UTC"}} {
		if s.Validate() == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
	if (Station{Name: "A", Timezone: "UTC"}).Validate() != nil {
		t.Fatal("UTC rejected")
	}
}
