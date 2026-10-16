package persistence

import (
	"context"
	"path/filepath"
	"stationos.local/stationos/internal/prompts"
	"strings"
	"testing"
)

func TestPromptsDefaultsPersistenceResetAndValidation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "prompts.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	r := Prompts{DB: db}
	for _, d := range prompts.Definitions() {
		want, e := prompts.Default(d.ID)
		if e != nil {
			t.Fatal(e)
		}
		got, e := r.Prompt(ctx, d.ID)
		if e != nil || got != want || prompts.Validate(d.ID, got) != nil {
			t.Fatal(d.ID, e)
		}
	}
	const custom = "Write concise links.\nKeep accents: café. <script>data</script>"
	if err = r.SavePrompt(ctx, "show", custom); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err = db.QueryRow(`SELECT ai_revision FROM producer_settings`).Scan(&revision); err != nil || revision < 1 {
		t.Fatal(revision, err)
	}
	for _, bad := range []string{"", " \n ", strings.Repeat("x", prompts.MaxLength+1), string([]byte{0xff})} {
		if r.SavePrompt(ctx, "show", bad) == nil {
			t.Fatal("invalid prompt saved")
		}
	}
	if r.SavePrompt(ctx, "missing", "test") == nil || r.ResetPrompt(ctx, "missing") == nil {
		t.Fatal("unknown ID accepted")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r.DB = db
	if got, e := r.Prompt(ctx, "show"); e != nil || got != custom {
		t.Fatal(got, e)
	}
	if err = r.ResetPrompt(ctx, "show"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Prompt(ctx, "show")
	want, _ := prompts.Default("show")
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Prompt(ctx, "show"); err == nil {
		t.Fatal("storage failure silently returned a default")
	}
}
