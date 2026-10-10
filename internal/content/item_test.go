package content

import (
	"testing"
	"time"
)

func TestContentIdentityAndValidation(t *testing.T) {
	if ID("one", "two") != ID("one", "two") || ID("one", "two") == ID("other", "two") {
		t.Fatal("unstable or unscoped identity")
	}
	a := AudioItem{SourceID: "s", SourceItemID: "x", Title: "Jingle", Kind: Jingle, Origin: Original}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	d := -time.Second
	a.Duration = &d
	if a.Validate() == nil {
		t.Fatal("negative duration accepted")
	}
	a.Duration = nil
	a.Kind = "unknown"
	if a.Validate() == nil {
		t.Fatal("unknown kind accepted")
	}
	a.Kind = Track
	a.Origin = "unknown"
	if a.Validate() == nil {
		t.Fatal("unknown origin accepted")
	}
	a.Origin = Original
	a.SourceItemID = "x\x00y"
	if a.Validate() == nil {
		t.Fatal("NUL identity accepted")
	}
}
