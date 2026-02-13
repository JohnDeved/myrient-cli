package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/time/rate"
)

// Entry represents a file or directory in a Myrient directory listing.
type Entry struct {
	Name  string
	URL   string // Full URL
	Size  string // Human-readable size (e.g. "1.2M") or "-" for directories
	Date  string // Last modified date string
	IsDir bool
}

// Client handles HTTP requests to Myrient.
type Client struct {
	listHTTP *http.Client // Short timeout for directory listings
	dlHTTP   *http.Client // No timeout for file downloads (managed by context)
	limiter  *rate.Limiter
	baseURL  string
}

// New creates a new Myrient client.
func New(baseURL string, reqPerSec float64) *Client {
	if reqPerSec <= 0 {
		reqPerSec = 5.0
	}

	return &Client{
		listHTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
		dlHTTP: &http.Client{
			// No timeout -- downloads are long-running and controlled by context.
			// The 30s timeout on http.Client includes body read time in Go,
			// which would kill any download larger than ~150MB.
		},
		limiter: rate.NewLimiter(rate.Limit(reqPerSec), 5),
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// BaseURL returns the configured base URL.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// ListDirectory fetches and parses a directory listing from Myrient.
// The path should be relative to the base URL (e.g. "No-Intro/" or "No-Intro/Nintendo - Game Boy/").
func (c *Client) ListDirectory(ctx context.Context, dirPath string) ([]Entry, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	dirURL := c.baseURL + "/" + dirPath
	if !strings.HasSuffix(dirURL, "/") {
		dirURL += "/"
	}

	req, err := http.NewRequestWithContext(ctx, "GET", dirURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "myrient-tui/1.0")
	req.Header.Set("Referer", dirURL)

	resp, err := c.listHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", dirURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, dirURL)
	}

	return parseDirectoryListing(resp.Body, dirURL)
}

// DownloadFile initiates a download of a file, optionally resuming from offset.
// Returns the response body (caller must close), content length, and whether resume was accepted.
func (c *Client) DownloadFile(ctx context.Context, fileURL string, resumeFrom int64) (io.ReadCloser, int64, bool, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, 0, false, err
	}

	// Derive the directory URL for the Referer header.
	parts := strings.Split(fileURL, "/")
	referer := strings.Join(parts[:len(parts)-1], "/") + "/"

	req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
	if err != nil {
		return nil, 0, false, err
	}
	req.Header.Set("User-Agent", "myrient-tui/1.0")
	req.Header.Set("Referer", referer)

	if resumeFrom > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
	}

	resp, err := c.dlHTTP.Do(req)
	if err != nil {
		return nil, 0, false, err
	}

	resumed := resp.StatusCode == http.StatusPartialContent
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, 0, false, fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, fileURL)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	lowerURL := strings.ToLower(fileURL)
	if strings.Contains(contentType, "text/html") &&
		!strings.HasSuffix(lowerURL, ".html") &&
		!strings.HasSuffix(lowerURL, ".htm") {
		resp.Body.Close()
		return nil, 0, false, fmt.Errorf("refusing HTML response for file URL %s", fileURL)
	}

	return resp.Body, resp.ContentLength, resumed, nil
}

// parseDirectoryListing parses an Apache/nginx autoindex HTML page into entries.
func parseDirectoryListing(r io.Reader, dirURL string) ([]Entry, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	var entries []Entry
	seen := map[string]struct{}{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			entry, ok := parseTableRow(n, dirURL)
			if ok {
				if _, exists := seen[entry.URL]; !exists {
					seen[entry.URL] = struct{}{}
					entries = append(entries, entry)
				}
			}
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			entry, ok := parseAnchorLink(n, dirURL)
			if ok {
				if _, exists := seen[entry.URL]; !exists {
					seen[entry.URL] = struct{}{}
					entries = append(entries, entry)
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	return entries, nil
}

// parseTableRow extracts an Entry from a <tr> element in the directory listing.
func parseTableRow(tr *html.Node, dirURL string) (Entry, bool) {
	var cells []*html.Node
	for td := tr.FirstChild; td != nil; td = td.NextSibling {
		if td.Type == html.ElementNode && td.Data == "td" {
			cells = append(cells, td)
		}
	}

	if len(cells) < 1 {
		return Entry{}, false
	}

	// First cell contains an <a> tag with the file/directory link.
	link, name := findLink(cells[0])
	if link == "" || name == "" {
		return Entry{}, false
	}

	// Skip parent directory and self links.
	if name == "." || name == ".." || link == "../" || link == "./" {
		return Entry{}, false
	}
	if name == "Parent directory/" {
		return Entry{}, false
	}

	// Determine if it's a directory.
	isDir := strings.HasSuffix(link, "/")

	// Clean up the name.
	name = strings.TrimSuffix(name, "/")

	// URL-decode the name if it came from the href.
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}

	fullURL, err := resolveURL(dirURL, link)
	if err != nil {
		return Entry{}, false
	}

	sizeText := ""
	dateText := ""
	if len(cells) > 1 {
		sizeText = textContent(cells[1])
	}
	if len(cells) > 2 {
		dateText = textContent(cells[2])
	}

	return Entry{
		Name:  name,
		URL:   fullURL,
		Size:  strings.TrimSpace(sizeText),
		Date:  strings.TrimSpace(dateText),
		IsDir: isDir,
	}, true
}

func parseAnchorLink(a *html.Node, dirURL string) (Entry, bool) {
	link, name := findLink(a)
	if link == "" {
		return Entry{}, false
	}
	if strings.HasPrefix(link, "#") || strings.HasPrefix(link, "?") {
		return Entry{}, false
	}
	if strings.HasPrefix(strings.ToLower(link), "javascript:") {
		return Entry{}, false
	}
	if name == "" {
		name = strings.TrimSuffix(pathBase(link), "/")
	}
	if name == "" || name == "." || name == ".." || link == "../" || link == "./" {
		return Entry{}, false
	}
	if strings.EqualFold(strings.TrimSpace(name), "Parent Directory") ||
		strings.EqualFold(strings.TrimSpace(name), "Parent directory/") {
		return Entry{}, false
	}
	name = strings.TrimSuffix(name, "/")
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	fullURL, err := resolveURL(dirURL, link)
	if err != nil {
		return Entry{}, false
	}
	return Entry{
		Name:  strings.TrimSpace(name),
		URL:   fullURL,
		Size:  "",
		Date:  "",
		IsDir: strings.HasSuffix(link, "/"),
	}, true
}

func resolveURL(base, ref string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	relURL, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(relURL).String(), nil
}

func pathBase(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	path := parsed.Path
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return ""
	}
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// findLink finds the first <a> tag in a node tree and returns (href, text).
func findLink(n *html.Node) (string, string) {
	if n.Type == html.ElementNode && n.Data == "a" {
		href := ""
		for _, attr := range n.Attr {
			if attr.Key == "href" {
				href = attr.Val
				break
			}
		}
		// Get the title attribute if available, otherwise use text content.
		title := ""
		for _, attr := range n.Attr {
			if attr.Key == "title" {
				title = attr.Val
				break
			}
		}
		text := textContent(n)
		if title != "" {
			return href, title
		}
		return href, strings.TrimSpace(text)
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if href, text := findLink(child); href != "" {
			return href, text
		}
	}
	return "", ""
}

// textContent returns all text content within a node.
func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		sb.WriteString(textContent(child))
	}
	return sb.String()
}
