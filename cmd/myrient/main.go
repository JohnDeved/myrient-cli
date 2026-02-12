package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/johannberger/myrient/internal/client"
	"github.com/johannberger/myrient/internal/config"
	"github.com/johannberger/myrient/internal/downloader"
	"github.com/johannberger/myrient/internal/index"
	"github.com/johannberger/myrient/internal/tui"
	"github.com/johannberger/myrient/internal/util"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "myrient",
		Short: "A TUI client for browsing and downloading from Myrient",
		Long: `Myrient TUI - Browse, search, and download video game preservation content
from myrient.erista.me directly in your terminal.`,
		RunE: runTUI,
	}

	// Browse command
	browseCmd := &cobra.Command{
		Use:   "browse [path]",
		Short: "Launch TUI at a specific path",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runTUI,
	}
	browseCmd.Flags().Bool("plain", false, "List entries in plain text instead of launching TUI")
	browseCmd.Flags().Bool("json", false, "List entries as JSON instead of launching TUI")
	browseCmd.Flags().Bool("name-only", false, "Only print names in non-TUI output")
	browseCmd.Flags().Int("limit", 0, "Limit number of entries in non-TUI output (0 = unlimited)")

	// List command (non-interactive directory listing)
	listCmd := &cobra.Command{
		Use:   "ls [path]",
		Short: "List a Myrient directory in plain text",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runList,
	}
	listCmd.Flags().Bool("json", false, "Output JSON")
	listCmd.Flags().Bool("name-only", false, "Only print names")
	listCmd.Flags().Int("limit", 0, "Limit number of entries (0 = unlimited)")

	// Index command
	indexCmd := &cobra.Command{
		Use:   "index",
		Short: "Crawl Myrient and build a local search index",
		RunE:  runIndex,
	}
	indexCmd.Flags().String("collection", "", "Only index a specific collection (e.g. 'No-Intro')")
	indexCmd.Flags().Bool("force", false, "Force re-crawling even when directories are not stale")
	indexCmd.Flags().Int("workers", 4, "Number of collections to crawl in parallel")

	// Search command
	searchCmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search the local index for games/ROMs",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runSearch,
	}
	searchCmd.Flags().String("collection", "", "Filter by collection name")
	searchCmd.Flags().Int("limit", 50, "Maximum number of results")
	searchCmd.Flags().Bool("json", false, "Output JSON")

	// Download command
	downloadCmd := &cobra.Command{
		Use:   "download <url-or-query>",
		Short: "Download a file from Myrient by URL or query",
		Args:  cobra.ExactArgs(1),
		RunE:  runDownload,
	}
	downloadCmd.Flags().StringP("output", "o", "", "Output directory for this download")
	downloadCmd.Flags().String("search-path", "No-Intro/Nintendo - Nintendo DS (Decrypted)/", "Directory path to search when argument is a query")
	downloadCmd.Flags().String("prefer-region", "", "Preferred region when resolving a query (eu, usa, japan)")
	downloadCmd.Flags().String("prefer-language", "", "Preferred languages in order (comma-separated, e.g. de,en)")
	downloadCmd.Flags().Bool("exact", false, "Require exact phrase match when resolving a query")
	downloadCmd.Flags().Bool("include-nonretail", false, "Include demo/beta/kiosk variants in query matches")
	downloadCmd.Flags().Bool("all", false, "When using a query, download all matching files")
	downloadCmd.Flags().Int("match-limit", 0, "Limit matched query results before downloading (0 = unlimited)")
	downloadCmd.Flags().Bool("dry-run", false, "Resolve query and print selected match without downloading")

	findCmd := &cobra.Command{
		Use:   "find <query>",
		Short: "Find matching files in a Myrient directory",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runFind,
	}
	findCmd.Flags().String("search-path", "No-Intro/Nintendo - Nintendo DS (Decrypted)/", "Directory path to search")
	findCmd.Flags().String("prefer-region", "", "Preferred region for ranking (eu, usa, japan)")
	findCmd.Flags().String("prefer-language", "", "Preferred languages in order (comma-separated, e.g. de,en)")
	findCmd.Flags().Bool("exact", false, "Require exact phrase match")
	findCmd.Flags().Int("limit", 20, "Maximum number of matches to print")
	findCmd.Flags().Bool("json", false, "Output JSON")

	// Stats command
	statsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show index statistics",
		RunE:  runStats,
	}
	statsCmd.Flags().Bool("json", false, "Output JSON")

	rootCmd.AddCommand(browseCmd, listCmd, indexCmd, searchCmd, downloadCmd, findCmd, statsCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runTUI(cmd *cobra.Command, args []string) error {
	plainMode, _ := cmd.Flags().GetBool("plain")
	jsonMode, _ := cmd.Flags().GetBool("json")
	if plainMode || jsonMode {
		return runList(cmd, args)
	}

	if !isInteractiveTerminal() {
		return runList(cmd, args)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	c := client.New(cfg.BaseURL, cfg.RequestsPerSecond)

	// Open DB (may not exist yet, that's fine).
	db, err := index.OpenDB(config.DBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not open index DB: %v\n", err)
		db = nil
	}
	if db != nil {
		defer db.Close()
	}

	startPath := ""
	if len(args) > 0 {
		startPath = args[0]
	}

	return tui.Run(c, db, cfg, startPath)
}

func runList(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	path := ""
	if len(args) > 0 {
		path = args[0]
	}
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	if path != "" && !strings.HasSuffix(path, "/") {
		path += "/"
	}

	c := client.New(cfg.BaseURL, cfg.RequestsPerSecond)
	entries, err := c.ListDirectory(context.Background(), path)
	if err != nil {
		return err
	}

	limit, _ := cmd.Flags().GetInt("limit")
	if limit > 0 && limit < len(entries) {
		entries = entries[:limit]
	}

	jsonMode, _ := cmd.Flags().GetBool("json")
	nameOnly, _ := cmd.Flags().GetBool("name-only")
	if jsonMode {
		type entryOut struct {
			Name  string `json:"name"`
			URL   string `json:"url"`
			Size  string `json:"size"`
			Date  string `json:"date"`
			IsDir bool   `json:"is_dir"`
		}
		out := struct {
			Path    string     `json:"path"`
			Entries []entryOut `json:"entries"`
		}{
			Path: path,
		}
		out.Entries = make([]entryOut, 0, len(entries))
		for _, e := range entries {
			out.Entries = append(out.Entries, entryOut{
				Name:  e.Name,
				URL:   e.URL,
				Size:  e.Size,
				Date:  e.Date,
				IsDir: e.IsDir,
			})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if path == "" {
		fmt.Println("/")
	} else {
		fmt.Printf("/%s\n", path)
	}
	for _, e := range entries {
		if nameOnly {
			if e.IsDir {
				fmt.Printf("%s/\n", e.Name)
			} else {
				fmt.Println(e.Name)
			}
			continue
		}
		kind := "F"
		if e.IsDir {
			kind = "D"
		}
		fmt.Printf("%s\t%-12s\t%-20s\t%s\n", kind, e.Size, e.Date, e.Name)
	}
	return nil
}

func runIndex(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	c := client.New(cfg.BaseURL, cfg.RequestsPerSecond)

	db, err := index.OpenDB(config.DBPath())
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	collection, _ := cmd.Flags().GetString("collection")
	force, _ := cmd.Flags().GetBool("force")
	workers, _ := cmd.Flags().GetInt("workers")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	crawler := index.NewCrawler(c, db, cfg.IndexStaleDays)
	crawler.SetForce(force)
	crawler.SetWorkers(workers)
	crawler.SetProgressCallback(func(p index.CrawlProgress) {
		fmt.Fprintf(os.Stderr, "\r  Crawling: %s  [dirs: %d  files: %d  errors: %d]",
			util.TruncatePath(p.CurrentPath, 50), p.DirsProcessed, p.FilesFound, p.Errors)
	})

	if collection != "" {
		fmt.Fprintf(os.Stderr, "Indexing collection: %s\n", collection)
		if err := crawler.CrawlCollection(ctx, collection); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "Indexing all collections...\n")
		if err := crawler.CrawlAll(ctx); err != nil {
			return err
		}
	}

	p := crawler.Progress()
	fmt.Fprintf(os.Stderr, "\n\nDone! Indexed %d directories, %d files (%d errors)\n",
		p.DirsProcessed, p.FilesFound, p.Errors)

	return nil
}

func runSearch(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	db, err := index.OpenDB(config.DBPath())
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	collection, _ := cmd.Flags().GetString("collection")
	limit, _ := cmd.Flags().GetInt("limit")

	var results []index.SearchResult
	if collection != "" {
		results, err = db.SearchInCollection(query, collection, limit)
	} else {
		results, err = db.Search(query, limit)
	}
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		jsonMode, _ := cmd.Flags().GetBool("json")
		if jsonMode {
			out := struct {
				Query      string               `json:"query"`
				Collection string               `json:"collection,omitempty"`
				Count      int                  `json:"count"`
				Results    []index.SearchResult `json:"results"`
			}{
				Query:      query,
				Collection: collection,
				Count:      0,
				Results:    []index.SearchResult{},
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}

		fmt.Println("No results found.")
		fmt.Println("Tip: Run 'myrient index' to build the search index first.")
		return nil
	}

	jsonMode, _ := cmd.Flags().GetBool("json")
	if jsonMode {
		out := struct {
			Query      string               `json:"query"`
			Collection string               `json:"collection,omitempty"`
			Count      int                  `json:"count"`
			Results    []index.SearchResult `json:"results"`
		}{
			Query:      query,
			Collection: collection,
			Count:      len(results),
			Results:    results,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	for _, r := range results {
		fmt.Printf("%-60s  %-25s  %s\n", r.Name, r.CollectionName, r.Size)
	}

	fmt.Fprintf(os.Stderr, "\n%d results found.\n", len(results))
	return nil
}

func runDownload(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	outDir, _ := cmd.Flags().GetString("output")
	if outDir == "" {
		outDir = cfg.DownloadDir
	}

	c := client.New(cfg.BaseURL, cfg.RequestsPerSecond)

	arg := strings.TrimSpace(args[0])
	fileURLs := []string{}

	isURL := false
	if parsed, parseErr := url.ParseRequestURI(arg); parseErr == nil && strings.HasPrefix(strings.ToLower(arg), "http") && parsed.Host != "" {
		isURL = true
	}

	allMatches, _ := cmd.Flags().GetBool("all")
	matchLimit, _ := cmd.Flags().GetInt("match-limit")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	exact, _ := cmd.Flags().GetBool("exact")
	includeNonRetail, _ := cmd.Flags().GetBool("include-nonretail")

	if !isURL {
		searchPath, _ := cmd.Flags().GetString("search-path")
		preferRegion, _ := cmd.Flags().GetString("prefer-region")
		preferLanguageRaw, _ := cmd.Flags().GetString("prefer-language")
		preferLanguages := parsePreferredLanguages(preferLanguageRaw)
		entries, err := c.ListDirectory(context.Background(), normalizeListPath(searchPath))
		if err != nil {
			return fmt.Errorf("listing search path %q: %w", searchPath, err)
		}
		matches := rankMatches(entries, arg, preferRegion, preferLanguages, exact)
		if !includeNonRetail {
			filtered := make([]client.Entry, 0, len(matches))
			for _, m := range matches {
				if isNonRetail(strings.ToLower(m.Name)) {
					continue
				}
				filtered = append(filtered, m)
			}
			matches = filtered
		}
		if matchLimit > 0 && matchLimit < len(matches) {
			matches = matches[:matchLimit]
		}
		if len(matches) == 0 {
			return fmt.Errorf("no matches found for %q in /%s", arg, normalizeListPath(searchPath))
		}
		fmt.Fprintf(os.Stderr, "Resolved query: %s\n", arg)
		fmt.Fprintf(os.Stderr, "From: /%s\n", normalizeListPath(searchPath))
		if allMatches {
			fmt.Fprintf(os.Stderr, "Matched %d file(s)\n", len(matches))
			for i, m := range matches {
				fmt.Fprintf(os.Stderr, "%d. %s\n", i+1, m.Name)
				if dryRun {
					fmt.Fprintf(os.Stderr, "   %s\n", m.URL)
				}
				fileURLs = append(fileURLs, m.URL)
			}
		} else {
			picked := matches[0]
			fmt.Fprintf(os.Stderr, "Picked: %s\n", picked.Name)
			fmt.Fprintf(os.Stderr, "URL: %s\n", picked.URL)
			fileURLs = append(fileURLs, picked.URL)
		}

		if dryRun {
			return nil
		}
	} else {
		fileURLs = append(fileURLs, arg)
	}
	failures := []string{}
	for i, fileURL := range fileURLs {
		if len(fileURLs) > 1 {
			fmt.Fprintf(os.Stderr, "\n[%d/%d]\n", i+1, len(fileURLs))
		}
		if err := downloadOne(c, outDir, fileURL); err != nil {
			failures = append(failures, err.Error())
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("%d download(s) failed:\n- %s", len(failures), strings.Join(failures, "\n- "))
	}
	return nil
}

func downloadOne(c *client.Client, outDir, fileURL string) error {
	u, err := url.Parse(fileURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid URL: %q", fileURL)
	}
	if strings.HasSuffix(u.Path, "/") {
		return fmt.Errorf("refusing to download directory URL: %s (provide a file URL)", fileURL)
	}
	if path.Base(strings.TrimSuffix(u.Path, "/")) == "files" {
		return fmt.Errorf("refusing to download directory URL: %s (provide a file URL)", fileURL)
	}

	preflightCtx, preflightCancel := context.WithTimeout(context.Background(), 20*time.Second)
	body, _, _, err := c.DownloadFile(preflightCtx, fileURL, 0)
	preflightCancel()
	if err != nil {
		return fmt.Errorf("download preflight failed: %w", err)
	}
	body.Close()

	parts := strings.Split(fileURL, "/")
	name := parts[len(parts)-1]
	if name == "" && len(parts) > 1 {
		name = parts[len(parts)-2]
	}
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}

	fmt.Fprintf(os.Stderr, "Downloading: %s\n", name)
	fmt.Fprintf(os.Stderr, "To: %s\n", outDir)

	dlm := downloader.NewManager(c, outDir, 1)
	item, created := dlm.Enqueue(name, fileURL, "")
	if !created {
		fmt.Fprintf(os.Stderr, "Already queued or downloaded: %s\n", name)
		return nil
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		item.Mu.Lock()
		status := item.Status
		errVal := item.Error
		item.Mu.Unlock()

		progress := item.Progress()
		speed := item.Speed()

		switch status {
		case downloader.StatusCompleted:
			fmt.Fprintf(os.Stderr, "\rDownloaded: %s (100%%)                    \n", name)
			return nil
		case downloader.StatusFailed:
			return fmt.Errorf("download failed: %s: %v", name, errVal)
		case downloader.StatusActive:
			fmt.Fprintf(os.Stderr, "\r  %.1f%% (%s/s)    ", progress*100, util.FormatBytes(int64(speed)))
		}
	}
	return nil
}

func runFind(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	searchPath, _ := cmd.Flags().GetString("search-path")
	preferRegion, _ := cmd.Flags().GetString("prefer-region")
	preferLanguageRaw, _ := cmd.Flags().GetString("prefer-language")
	preferLanguages := parsePreferredLanguages(preferLanguageRaw)
	exact, _ := cmd.Flags().GetBool("exact")
	limit, _ := cmd.Flags().GetInt("limit")
	jsonMode, _ := cmd.Flags().GetBool("json")

	c := client.New(cfg.BaseURL, cfg.RequestsPerSecond)
	entries, err := c.ListDirectory(context.Background(), normalizeListPath(searchPath))
	if err != nil {
		return err
	}

	matches := rankMatches(entries, query, preferRegion, preferLanguages, exact)
	if limit > 0 && limit < len(matches) {
		matches = matches[:limit]
	}

	if jsonMode {
		out := struct {
			Query       string         `json:"query"`
			SearchPath  string         `json:"search_path"`
			PreferRegion string        `json:"prefer_region,omitempty"`
			PreferLanguage []string    `json:"prefer_language,omitempty"`
			Exact       bool           `json:"exact"`
			Count       int            `json:"count"`
			Matches     []client.Entry `json:"matches"`
		}{
			Query:       query,
			SearchPath:  normalizeListPath(searchPath),
			PreferRegion: preferRegion,
			PreferLanguage: preferLanguages,
			Exact:       exact,
			Count:       len(matches),
			Matches:     matches,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Printf("query=%q path=/%s\n", query, normalizeListPath(searchPath))
	if len(matches) == 0 {
		fmt.Println("No matches found.")
		return nil
	}
	for i, m := range matches {
		fmt.Printf("%s\t%s\t%s\t%s\n", strconv.Itoa(i+1)+".", m.Size, m.Date, m.Name)
	}
	return nil
}

func runStats(cmd *cobra.Command, args []string) error {
	db, err := index.OpenDB(config.DBPath())
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	stats, err := db.GetStats()
	if err != nil {
		return err
	}

	jsonMode, _ := cmd.Flags().GetBool("json")
	if jsonMode {
		out := struct {
			Collections int    `json:"collections"`
			Directories int    `json:"directories"`
			Files       int    `json:"files"`
			Database    string `json:"database"`
		}{
			Collections: stats.Collections,
			Directories: stats.Directories,
			Files:       stats.Files,
			Database:    config.DBPath(),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Printf("Index Statistics:\n")
	fmt.Printf("  Collections: %d\n", stats.Collections)
	fmt.Printf("  Directories: %d\n", stats.Directories)
	fmt.Printf("  Files:       %d\n", stats.Files)
	fmt.Printf("  Database:    %s\n", config.DBPath())

	return nil
}

func isInteractiveTerminal() bool {
	inInfo, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	outInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (inInfo.Mode()&os.ModeCharDevice) != 0 && (outInfo.Mode()&os.ModeCharDevice) != 0
}

func normalizeListPath(p string) string {
	p = strings.TrimSpace(strings.TrimPrefix(p, "/"))
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	replacer := strings.NewReplacer(
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"-", " ",
		"_", " ",
		",", " ",
		".", " ",
		"/", " ",
	)
	s = replacer.Replace(s)
	raw := strings.Fields(s)
	stop := map[string]bool{
		"a": true, "an": true, "the": true, "of": true, "and": true,
		"film": true, "game": true,
	}
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		if len(tok) < 2 || stop[tok] {
			continue
		}
		out = append(out, tok)
	}
	return out
}

func rankMatches(entries []client.Entry, query, preferRegion string, preferLanguages []string, exact bool) []client.Entry {
	tokens := tokenize(query)
	prefer := strings.ToLower(strings.TrimSpace(preferRegion))
	queryLower := strings.ToLower(strings.TrimSpace(query))

	type scored struct {
		entry client.Entry
		score int
	}
	var scoredEntries []scored

	for _, e := range entries {
		if e.IsDir {
			continue
		}
		name := strings.ToLower(e.Name)
		nameTokens := tokenize(name)
		nameTokenSet := make(map[string]struct{}, len(nameTokens))
		for _, nt := range nameTokens {
			nameTokenSet[nt] = struct{}{}
		}
		score := 0
		tokenHits := 0
		for _, t := range tokens {
			if _, ok := nameTokenSet[t]; ok {
				tokenHits++
				score += 10
			}
		}
		phraseHit := queryLower != "" && strings.Contains(name, queryLower)
		if phraseHit {
			score += 20
		}
		if exact && !phraseHit {
			continue
		}
		if len(tokens) > 0 && tokenHits == len(tokens) {
			score += 12
		}

		requiredHits := 1
		if len(tokens) >= 2 {
			requiredHits = 2
		}
		if !phraseHit && tokenHits < requiredHits {
			continue
		}
		if prefer != "" {
			switch prefer {
			case "eu", "europe":
				if strings.Contains(name, "(europe") {
					score += 30
				}
			case "us", "usa", "na":
				if strings.Contains(name, "(usa") || strings.Contains(name, "(usa,") {
					score += 30
				}
			case "jp", "japan":
				if strings.Contains(name, "(japan") {
					score += 30
				}
			}
		}

		if len(preferLanguages) > 0 {
			matchedPreferredLanguage := false
			for i, lang := range preferLanguages {
				if hasLanguageTag(name, lang) {
					bonus := 18 - (i * 4)
					if bonus < 4 {
						bonus = 4
					}
					score += bonus
					matchedPreferredLanguage = true
					break
				}
			}
			if !matchedPreferredLanguage && hasAnyLanguageTag(name) {
				score -= 4
			}
		}

		if strings.Contains(name, "(demo") || strings.Contains(name, "(kiosk") || strings.Contains(name, "(beta") {
			score -= 50
		}
		if strings.Contains(name, "wii u virtual console") {
			score -= 10
		}

		if score > 0 {
			scoredEntries = append(scoredEntries, scored{entry: e, score: score})
		}
	}

	sort.Slice(scoredEntries, func(i, j int) bool {
		if scoredEntries[i].score == scoredEntries[j].score {
			return scoredEntries[i].entry.Name < scoredEntries[j].entry.Name
		}
		return scoredEntries[i].score > scoredEntries[j].score
	})

	res := make([]client.Entry, 0, len(scoredEntries))
	for _, s := range scoredEntries {
		res = append(res, s.entry)
	}
	return res
}

func parsePreferredLanguages(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	seen := map[string]bool{}
	langs := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		switch p {
		case "de", "deu", "ger", "german":
			p = "de"
		case "en", "eng", "english":
			p = "en"
		case "fr", "fra", "fre", "french":
			p = "fr"
		case "es", "spa", "spanish":
			p = "es"
		case "it", "ita", "italian":
			p = "it"
		case "nl", "dut", "nld", "dutch":
			p = "nl"
		case "ja", "jp", "jpn", "japanese":
			p = "ja"
		}
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		langs = append(langs, p)
	}
	return langs
}

func hasLanguageTag(lowerName, lang string) bool {
	patterns := []string{
		"(" + lang + ")",
		"(" + lang + ",",
		"," + lang + ",",
		"," + lang + ")",
	}
	for _, p := range patterns {
		if strings.Contains(lowerName, p) {
			return true
		}
	}
	return false
}

func hasAnyLanguageTag(lowerName string) bool {
	langs := []string{"en", "de", "fr", "es", "it", "nl", "ja", "ko", "zh", "ru", "pt", "sv", "no", "da", "fi", "pl", "cs", "hu"}
	for _, lang := range langs {
		if hasLanguageTag(lowerName, lang) {
			return true
		}
	}
	return false
}

func isNonRetail(lowerName string) bool {
	return strings.Contains(lowerName, "(demo") ||
		strings.Contains(lowerName, "(kiosk") ||
		strings.Contains(lowerName, "(beta") ||
		strings.Contains(lowerName, "(video")
}
