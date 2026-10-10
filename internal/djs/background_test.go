package djs

import (
	"strings"
	"testing"
)

func TestProfilePictureValidation(t *testing.T) {
	p := Profile{Name: "DJ", Model: "model", Voice: DefaultVoice, ConversationSpeed: DefaultConversationSpeed}
	for _, picture := range []string{"", "/static/djs/alex-bright.jpg", "https://example.com/portrait.jpg"} {
		p.PortraitURL = picture
		if err := p.Validate(); err != nil {
			t.Fatal(picture, err)
		}
	}
	for _, picture := range []string{"javascript:alert(1)", "data:image/svg+xml,test", "//example.com/photo", "http://example.com/photo", "/static/other.jpg", "https://user:secret@example.com/photo"} {
		p.PortraitURL = picture
		if err := p.Validate(); err == nil {
			t.Fatal("unsafe picture accepted", picture)
		}
	}
	p.PortraitURL = ""
	p.Background = strings.Repeat("a", 16001)
	if err := p.Validate(); err == nil {
		t.Fatal("unbounded background accepted")
	}
}
