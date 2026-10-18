package web

import (
	"context"
	"strings"
	"testing"

	"stationos.local/stationos/internal/producer"
)

func TestAssistantMarkdownFormatsAndEscapesContent(t *testing.T) {
	input := "# Station plan\n\n**Jazz** and *soul*, with ~~repeats~~ removed.\n\n1. Create station\n2. Add shows\n   - Evening music\n\n> A relaxed programme\n\nUse `station_id`.\n\n```go\nfmt.Println(\"<example>\")\n```\n\n| Show | Day |\n| --- | --- |\n| Jazz | Friday |\n\n[Open station](/producer/stations/example)\n"
	output := string(assistantMarkdown(input))
	for _, expected := range []string{"<h2>Station plan</h2>", "<strong>Jazz</strong>", "<em>soul</em>", "<del>repeats</del>", "<ol>", "<ul>", "<blockquote>", "<code>station_id</code>", "<pre><code class=\"language-go\">", "&lt;example&gt;", "<table>", `href="/producer/stations/example"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing %s in %s", expected, output)
		}
	}
	if strings.Contains(output, "<h1>") {
		t.Fatal("response added another page h1")
	}
}
func TestAssistantMarkdownBlocksActiveContent(t *testing.T) {
	for _, input := range []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<a hx-post="/producer/stations/example/delete">Delete</a>`,
		`[click](javascript:alert%281%29)`,
		`[click](javascript&colon;alert%281%29)`,
		`[click](java&#x73;cript:alert%281%29)`,
		`[click](data:text/html;base64,PHNjcmlwdD4=)`,
		`![pixel](https://tracking.invalid/pixel?secret=example)`,
	} {
		output := string(assistantMarkdown(input))
		for _, unsafe := range []string{"<script", "<img", "onerror=", "hx-post=", "javascript:", "data:text/html", "tracking.invalid"} {
			if strings.Contains(output, unsafe) {
				t.Fatalf("active content rendered: %s", output)
			}
		}
	}
}
func TestAssistantMarkdownFullPageAndPollingPreservePlainUserMessages(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	ctx := context.Background()
	id := producer.ID()
	if e := store.Create(ctx, producer.Conversation{ID: id, Title: "Markdown"}); e != nil {
		t.Fatal(e)
	}
	for _, m := range []producer.Message{
		{Role: "user", Text: "**literal user text** <script>"},
		{Role: "action", Text: "Saved **literal action**"},
		{Role: "assistant", Text: "# Plan\n\n**Created** your station.\n\n1. Jazz\n2. Soul"},
	} {
		if e := store.Append(ctx, id, m); e != nil {
			t.Fatal(e)
		}
	}
	for _, path := range []string{"/producer/assistant?conversation=" + id, "/producer/assistant/" + id + "/messages"} {
		w := request(h, "GET", path, nil, nil)
		output := w.Body.String()
		if w.Code != 200 || !strings.Contains(output, "<strong>Created</strong>") || !strings.Contains(output, "<h2>Plan</h2>") {
			t.Fatal("Markdown missing", output)
		}
		if !strings.Contains(output, "**literal user text** &lt;script&gt;") || !strings.Contains(output, "Saved **literal action**") {
			t.Fatal("plain messages changed", output)
		}
		if !strings.Contains(output, `<ol aria-label="Conversation history" hx-target="#main" hx-swap="innerHTML show:window:top" hx-push-url="true">`) {
			t.Fatal("conversation links inherit polling navigation", output)
		}
		if strings.Contains(output, "<script>") {
			t.Fatal("user HTML rendered")
		}
	}
}

func TestAssistantTablesUseSharedFrameWithoutAllowingRawHTML(t *testing.T) {
	body := string(assistantMarkdown("| Title | Artist |\n| --- | --- |\n| Song | Performer |\n\n<script>alert(1)</script>"))
	if !strings.Contains(body, `<div class="table-scroll table-outline" tabindex="0" role="region" aria-label="Assistant response table"><table>`) || !strings.Contains(body, "</table></div>") {
		t.Fatal("assistant table missing shared accessible frame", body)
	}
	if strings.Contains(body, "<script>") {
		t.Fatal("raw HTML must remain omitted", body)
	}
}
