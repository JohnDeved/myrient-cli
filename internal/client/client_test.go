package client

import (
	"strings"
	"testing"
)

func TestParseDirectoryListing_TableAndAnchorDedup(t *testing.T) {
	html := `
<html><body>
<table>
  <tr><td><a href="game.zip">game.zip</a></td><td>1.2M</td><td>2026-01-01</td></tr>
</table>
<a href="https://example.com/help">help</a>
</body></html>`

	entries, err := parseDirectoryListing(strings.NewReader(html), "https://example.com/No-Intro/")
	if err != nil {
		t.Fatalf("parseDirectoryListing returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "game.zip" || entries[0].URL != "https://example.com/No-Intro/game.zip" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
}

func TestParseDirectoryListing_PreListingFallback(t *testing.T) {
	html := `
<html><body><pre>
<a href="?C=N;O=D">Name</a>
<a href="../">Parent Directory</a>
<a href="/donate/">Donate</a>
<a href="https://evil.example/file.zip">evil</a>
<a href="data:text/html;base64,SGVsbG8=">bad</a>
<a href="Folder/">Folder/</a>
<a href="file%20name.zip">file name.zip</a>
</pre></body></html>`

	entries, err := parseDirectoryListing(strings.NewReader(html), "https://example.com/No-Intro/")
	if err != nil {
		t.Fatalf("parseDirectoryListing returned error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "Folder" || !entries[0].IsDir {
		t.Fatalf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].Name != "file name.zip" || entries[1].IsDir {
		t.Fatalf("unexpected second entry: %+v", entries[1])
	}
}

func TestParseDirectoryListing_AcceptsAbsoluteSameHostChild(t *testing.T) {
	html := `<html><body><pre><a href="/files/No-Intro/">No-Intro/</a></pre></body></html>`

	entries, err := parseDirectoryListing(strings.NewReader(html), "https://example.com/files/")
	if err != nil {
		t.Fatalf("parseDirectoryListing returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].URL != "https://example.com/files/No-Intro/" || !entries[0].IsDir {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
}

func TestIsLikelyListingEntryURL_PathBoundary(t *testing.T) {
	if isLikelyListingEntryURL("https://example.com/files/", "https://example.com/filesomething/dir/") {
		t.Fatal("expected sibling path prefix lookalike to be rejected")
	}
}
