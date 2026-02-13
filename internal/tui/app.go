package tui

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JohnDeved/myrient-cli/internal/client"
	"github.com/JohnDeved/myrient-cli/internal/config"
	"github.com/JohnDeved/myrient-cli/internal/downloader"
	"github.com/JohnDeved/myrient-cli/internal/index"
)

// Tab identifies the active view.
type Tab int

const (
	TabBrowse Tab = iota
	TabSearch
	TabDownloads
)

// Messages
type entriesMsg struct {
	entries []client.Entry
	path    []string
	dirPath string
}

type errMsg struct{ err error }
type searchErrMsg struct{ err error }

type statusClearMsg struct{ id int }

type searchResultsMsg struct {
	results []index.SearchResult
	query   string
	autoIndexed bool
	localCount  int
	refreshWarn string
}

type searchProgressTickMsg struct{}

type browseIndexErrMsg struct{ err error }

type indexRefreshDoneMsg struct {
	dirs   int64
	files  int64
	errors int64
}

type indexRefreshErrMsg struct{ err error }
type indexRefreshTickMsg struct{}

type searchPreviewMsg struct {
	query   string
	results []index.SearchResult
	err     error
}

type searchJob struct {
	mu      sync.Mutex
	results []index.SearchResult
}

func (j *searchJob) setResults(results []index.SearchResult) {
	j.mu.Lock()
	j.results = results
	j.mu.Unlock()
}

func (j *searchJob) getResults() []index.SearchResult {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]index.SearchResult, len(j.results))
	copy(out, j.results)
	return out
}

type downloadUpdateMsg struct{}

// Model is the main Bubble Tea model.
type Model struct {
	client       *client.Client
	db           *index.DB
	dlManager    *downloader.Manager
	cfg          *config.Config
	activeTab    Tab
	browser      browserModel
	search       searchModel
	downloads    downloadsModel
	spinner      spinner.Model
	width        int
	height       int
	showHelp     bool
	helpOffset   int
	statusMsg    string
	statusID     int
	quitConfirm  bool
	startPath    string
	searchCrawler *index.Crawler
	searchJob    *searchJob
	searchLastRefresh time.Time
	indexRefreshRunning bool
	indexRefreshCrawler *index.Crawler
}

type RunOptions struct {
	AltScreen   bool
	MouseMotion bool
}

// NewModel creates the TUI model.
func NewModel(c *client.Client, db *index.DB, cfg *config.Config, startPath string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot

	dlm := downloader.NewManager(c, cfg.DownloadDir, cfg.MaxConcurrentDownloads)

	m := Model{
		client:    c,
		db:        db,
		dlManager: dlm,
		cfg:       cfg,
		activeTab: TabBrowse,
		browser:   newBrowserModel(),
		search:    newSearchModel(),
		downloads: newDownloadsModel(),
		spinner:   s,
		startPath: startPath,
		width:     100,
		height:    30,
	}

	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.loadDirectory(m.startPath),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		viewHeight := m.height - 8 // Account for header, tabs, status bar
		m.browser.height = viewHeight
		m.search.height = viewHeight - 3
		m.downloads.height = viewHeight - 2
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case entriesMsg:
		m.browser.setPathAndEntries(msg.path, msg.entries)
		return m, m.indexFromBrowseSnapshot(msg)

	case browseIndexErrMsg:
		return m, m.setStatus(fmt.Sprintf("Browse index update failed: %v", msg.err))

	case errMsg:
		m.browser.setError(msg.err)
		return m, nil

	case searchErrMsg:
		m.search.setError(msg.err)
		return m, nil

	case searchResultsMsg:
		m.search.lastQuery = msg.query
		m.search.setResults(msg.results)
		m.searchCrawler = nil
		m.searchJob = nil
		if msg.autoIndexed {
			m.searchLastRefresh = time.Now()
		}
		if msg.refreshWarn != "" {
			return m, m.setStatus(msg.refreshWarn)
		}
		if msg.autoIndexed {
			if len(msg.results) > msg.localCount {
				return m, m.setStatus(fmt.Sprintf("Refreshed index and found %d additional result(s)", len(msg.results)-msg.localCount))
			}
			if len(msg.results) > 0 {
				return m, m.setStatus(fmt.Sprintf("Refreshed index, %d result(s) found", len(msg.results)))
			}
			return m, m.setStatus("Refreshed index, but no matches found")
		}
		return m, nil

	case searchPreviewMsg:
		// Ignore stale preview results if input changed since dispatch.
		if strings.TrimSpace(msg.query) != strings.TrimSpace(m.search.input.Value()) {
			return m, nil
		}
		if msg.err != nil {
			m.search.setError(msg.err)
			return m, nil
		}
		m.search.lastQuery = msg.query
		m.search.results = msg.results
		m.search.totalFound = len(msg.results)
		m.search.normalizeViewport()
		if len(msg.results) == 0 {
			m.search.cursor = 0
			m.search.offset = 0
		}
		m.search.err = nil
		return m, nil

	case indexRefreshDoneMsg:
		m.indexRefreshRunning = false
		m.indexRefreshCrawler = nil
		m.search.bgRefreshing = false
		m.search.bgMsg = ""
		m.search.bgPath = ""
		m.searchLastRefresh = time.Now()
		return m, m.setStatus(fmt.Sprintf("Background index refresh complete (%d dirs, %d files, %d errors)", msg.dirs, msg.files, msg.errors))

	case indexRefreshErrMsg:
		m.indexRefreshRunning = false
		m.indexRefreshCrawler = nil
		m.search.bgRefreshing = false
		m.search.bgMsg = ""
		m.search.bgPath = ""
		return m, m.setStatus(fmt.Sprintf("Background index refresh failed: %v", msg.err))

	case indexRefreshTickMsg:
		if !m.indexRefreshRunning || m.indexRefreshCrawler == nil {
			return m, nil
		}
		p := m.indexRefreshCrawler.Progress()
		if p.CurrentPath != "" {
			m.search.bgMsg = "Refreshing stale/unindexed paths..."
			m.search.bgPath = p.CurrentPath
		} else {
			m.search.bgMsg = "Preparing background refresh..."
			m.search.bgPath = ""
		}
		m.search.bgDirs = p.DirsProcessed
		m.search.bgFiles = p.FilesFound
		m.search.bgErrors = p.Errors
		return m, m.indexRefreshTick()

	case downloadUpdateMsg:
		m.downloads.setItems(m.dlManager.Items())
		return m, nil

	case statusClearMsg:
		if msg.id == m.statusID {
			m.statusMsg = ""
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case searchProgressTickMsg:
		if !m.search.searching {
			return m, nil
		}
		if m.searchJob != nil && m.db != nil && strings.TrimSpace(m.search.lastQuery) != "" {
			live, err := m.db.Search(m.search.lastQuery, 100)
			if err == nil {
				m.searchJob.setResults(live)
			}
			live = m.searchJob.getResults()
			m.search.results = live
			m.search.totalFound = len(live)
			m.search.normalizeViewport()
		}
		if m.searchCrawler != nil {
			p := m.searchCrawler.Progress()
			if p.CurrentPath != "" {
				m.search.loadingMsg = "Refreshing stale/unindexed paths..."
				m.search.loadingPath = p.CurrentPath
				m.search.loadingDirs = p.DirsProcessed
				m.search.loadingFiles = p.FilesFound
				m.search.loadingErrors = p.Errors
			} else {
				m.search.loadingMsg = "Preparing index refresh..."
			}
		}
		return m, m.searchProgressTick()
	}

	// Pass through to search input if search tab is active.
	if m.activeTab == TabSearch {
		var cmd tea.Cmd
		m.search.input, cmd = m.search.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	searchFocused := m.activeTab == TabSearch && m.search.input.Focused()

	if m.showHelp {
		switch key {
		case "?", "esc":
			m.showHelp = false
			m.helpOffset = 0
			return m, nil
		case "up", "k":
			if m.helpOffset > 0 {
				m.helpOffset--
			}
			return m, nil
		case "down", "j":
			m.helpOffset++
			return m, nil
		case "pgup", "ctrl+u":
			m.helpOffset -= 8
			if m.helpOffset < 0 {
				m.helpOffset = 0
			}
			return m, nil
		case "pgdown", "ctrl+d":
			m.helpOffset += 8
			return m, nil
		case "home", "g":
			m.helpOffset = 0
			return m, nil
		}
	}

	// In browse view, plain character keys are reserved for filtering.
	if m.activeTab == TabBrowse && isTypeAheadKey(key) {
		return m.handleBrowseKey(key)
	}

	// Global keys.
	switch key {
	case "ctrl+c", "q":
		if searchFocused {
			m.search.input.Blur()
			return m, nil
		}
		if m.quitConfirm {
			m.dlManager.CancelAll()
			return m, tea.Quit
		}
		if m.dlManager.HasActive() {
			m.quitConfirm = true
			return m, m.setStatus("Active downloads running. Press q again to cancel and quit, or Esc to stay")
		}
		return m, tea.Quit

	case "esc":
		if m.quitConfirm {
			m.quitConfirm = false
			return m, m.setStatus("Quit canceled")
		}

	case "?":
		m.showHelp = !m.showHelp
		if !m.showHelp {
			m.helpOffset = 0
		}
		return m, nil

	case "tab":
		m.search.input.Blur()
		switch m.activeTab {
		case TabBrowse:
			m.activeTab = TabSearch
			m.search.input.Focus()
			return m.maybeRefreshIndexInSearchTab()
		case TabSearch:
			m.activeTab = TabDownloads
			m.downloads.setItems(m.dlManager.Items())
		case TabDownloads:
			m.activeTab = TabBrowse
		}
		return m, nil

	case "shift+tab":
		m.search.input.Blur()
		switch m.activeTab {
		case TabBrowse:
			m.activeTab = TabDownloads
			m.downloads.setItems(m.dlManager.Items())
		case TabSearch:
			m.activeTab = TabBrowse
		case TabDownloads:
			m.activeTab = TabSearch
			m.search.input.Focus()
			return m.maybeRefreshIndexInSearchTab()
		}
		return m, nil
	}

	// Tab-specific keys.
	switch m.activeTab {
	case TabBrowse:
		return m.handleBrowseKey(key)
	case TabSearch:
		return m.handleSearchKey(key, msg)
	case TabDownloads:
		return m.handleDownloadsKey(key)
	}

	return m, nil
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button == tea.MouseButtonLeft {
		if msg.Y == 1 {
			if msg.X >= 0 && msg.X <= 10 {
				m.activeTab = TabBrowse
				m.search.input.Blur()
				return m, nil
			}
			if msg.X >= 11 && msg.X <= 21 {
				m.activeTab = TabSearch
				m.search.input.Focus()
				return m.maybeRefreshIndexInSearchTab()
			}
			if msg.X >= 22 && msg.X <= 36 {
				m.activeTab = TabDownloads
				m.search.input.Blur()
				m.downloads.setItems(m.dlManager.Items())
				return m, nil
			}
		}
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.showHelp {
			if m.helpOffset > 0 {
				m.helpOffset--
			}
			return m, nil
		}
		switch m.activeTab {
		case TabBrowse:
			m.browser.moveUp()
		case TabSearch:
			m.search.moveUp()
		case TabDownloads:
			m.downloads.moveUp()
		}
	case tea.MouseButtonWheelDown:
		if m.showHelp {
			m.helpOffset++
			return m, nil
		}
		switch m.activeTab {
		case TabBrowse:
			m.browser.moveDown()
		case TabSearch:
			m.search.moveDown()
		case TabDownloads:
			m.downloads.moveDown()
		}
	}
	return m, nil
}

func (m Model) handleBrowseKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up":
		m.browser.moveUp()
	case "down":
		m.browser.moveDown()
	case "pgup", "ctrl+u":
		m.browser.pageUp()
	case "pgdown", "ctrl+d":
		m.browser.pageDown()
	case "home":
		m.browser.goHome()
	case "end":
		m.browser.goEnd()

	case "enter", "right":
		if sel := m.browser.selected(); sel != nil && sel.IsDir {
			newPath := append([]string{}, m.browser.path...)
			newPath = append(newPath, sel.Name)
			m.browser.loading = true
			return m, m.loadDirectory(strings.Join(newPath, "/") + "/")
		} else if sel != nil {
			subdir := strings.Join(m.browser.path, "/")
			return m, m.enqueueDownload(sel.Name, sel.URL, subdir)
		}

	case "backspace", "left":
		if key == "backspace" && m.browser.filter != "" {
			m.browser.backspaceFilter()
			return m, nil
		}
		if len(m.browser.path) > 0 {
			parentPath := ""
			if len(m.browser.path) > 1 {
				parentPath = strings.Join(m.browser.path[:len(m.browser.path)-1], "/") + "/"
			}
			m.browser.loading = true
			return m, m.loadDirectory(parentPath)
		}

	case "esc":
		if m.browser.filter != "" {
			m.browser.clearFilter()
			return m, m.setStatus("Filter cleared")
		}

	default:
		if isTypeAheadKey(key) {
			m.browser.appendFilter(key)
			return m, nil
		}
		if key == "backspace" {
			if m.browser.filter != "" {
				m.browser.backspaceFilter()
				return m, nil
			}
		}
	}

	return m, nil
}

func (m Model) handleSearchKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.search.input.Focused() {
		switch key {
		case "enter":
			if m.search.searching {
				return m, nil
			}
			query := m.search.input.Value()
			if query != "" {
				if m.indexRefreshRunning {
					m.search.input.Blur()
					return m, tea.Batch(m.setStatus("Index refresh already running; results update live"), m.previewSearch(strings.TrimSpace(query)))
				}
				m.search.searching = true
				m.search.startedAt = time.Now()
				m.search.loadingMsg = "Searching local index..."
				m.search.lastQuery = query
				m.search.cursor = 0
				m.search.offset = 0
				m.search.results = nil
				m.search.totalFound = 0
				m.search.loadingPath = ""
				m.search.loadingDirs = 0
				m.search.loadingFiles = 0
				m.search.loadingErrors = 0
				m.search.input.Blur()
				crawler := index.NewCrawler(m.client, m.db, m.cfg.IndexStaleDays)
				crawler.SetForce(false)
				crawler.SetWorkers(8)
				m.searchCrawler = crawler
				job := &searchJob{}
				m.searchJob = job
				started := m.setStatus("Search started: local results first, then full indexing to ensure complete coverage")
				return m, tea.Batch(started, m.performSearch(query, crawler, job), m.searchProgressTick())
			}
		case "up", "down", "pgup", "pgdown":
			m.search.input.Blur()
			return m.handleSearchKey(key, msg)
		case "esc":
			m.search.input.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			before := m.search.input.Value()
			m.search.input, cmd = m.search.input.Update(msg)
			after := strings.TrimSpace(m.search.input.Value())
			if m.search.searching {
				return m, cmd
			}
			if before == m.search.input.Value() {
				return m, cmd
			}
			if after == "" {
				m.search.lastQuery = ""
				m.search.results = nil
				m.search.totalFound = 0
				m.search.cursor = 0
				m.search.offset = 0
				m.search.err = nil
				return m, cmd
			}
			return m, tea.Batch(cmd, m.previewSearch(after))
		}
	} else {
		switch key {
		case "up":
			m.search.moveUp()
		case "down":
			m.search.moveDown()
		case "pgup", "ctrl+u":
			m.search.pageUp()
		case "pgdown", "ctrl+d":
			m.search.pageDown()
		case "home":
			m.search.cursor = 0
			m.search.offset = 0
		case "end":
			m.search.cursor = len(m.search.results) - 1
			if m.search.cursor < 0 {
				m.search.cursor = 0
			}
			m.search.normalizeViewport()
		case "enter":
			// Download selected result.
			if sel := m.search.selected(); sel != nil {
				return m, m.enqueueDownload(sel.Name, sel.URL, sel.CollectionName)
			}
		case "i", "/":
			m.search.input.Focus()
		case "o", "b":
			if sel := m.search.selected(); sel != nil {
				m.activeTab = TabBrowse
				m.search.input.Blur()
				m.browser.loading = true
				path := browsePathForSearchResult(sel.Path)
				status := m.setStatus("Opened result location in browser")
				return m, tea.Batch(status, m.loadDirectory(path))
			}
		case "esc":
			m.search.input.Focus()
		}
	}

	return m, nil
}

func (m Model) handleDownloadsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.downloads.moveUp()
	case "down", "j":
		m.downloads.moveDown()
	case "pgup", "ctrl+u":
		m.downloads.pageUp()
	case "pgdown", "ctrl+d":
		m.downloads.pageDown()
	case "c":
		// Cancel selected download.
		if sel := m.downloads.selected(); sel != nil {
			m.dlManager.Cancel(sel.ID)
			return m, m.setStatus(fmt.Sprintf("Cancelled: %s", sel.Name))
		}
	case "p":
		if sel := m.downloads.selected(); sel != nil {
			sel.Mu.Lock()
			status := sel.Status
			sel.Mu.Unlock()
			switch status {
			case downloader.StatusPaused:
				if m.dlManager.Resume(sel.ID) {
					return m, m.setStatus(fmt.Sprintf("Resumed: %s", sel.Name))
				}
			case downloader.StatusActive, downloader.StatusQueued:
				if m.dlManager.Pause(sel.ID) {
					return m, m.setStatus(fmt.Sprintf("Paused: %s", sel.Name))
				}
			}
			return m, m.setStatus("Selected download cannot be paused/resumed")
		}
	case "r":
		// Refresh download list.
		m.downloads.setItems(m.dlManager.Items())
	case "R":
		if sel := m.downloads.selected(); sel != nil {
			if m.dlManager.Retry(sel.ID) {
				return m, m.setStatus(fmt.Sprintf("Retrying: %s", sel.Name))
			}
			return m, m.setStatus("Selected download is not retryable")
		}
	case "x":
		removed := m.dlManager.ClearFinished()
		if removed > 0 {
			m.downloads.setItems(m.dlManager.Items())
			return m, m.setStatus(fmt.Sprintf("Cleared %d finished downloads", removed))
		}
		return m, m.setStatus("No finished downloads to clear")
	case "esc":
		m.downloads.cursor = 0
		m.downloads.offset = 0
	}

	return m, nil
}

// Commands

func (m Model) loadDirectory(path string) tea.Cmd {
	return func() tea.Msg {
		entries, err := m.client.ListDirectory(context.Background(), path)
		if err != nil {
			return errMsg{err: err}
		}

		// Parse path into segments.
		path = strings.Trim(path, "/")
		var segments []string
		if path != "" {
			segments = strings.Split(path, "/")
		}

		return entriesMsg{entries: entries, path: segments, dirPath: path}
	}
}

func (m Model) indexFromBrowseSnapshot(msg entriesMsg) tea.Cmd {
	return func() tea.Msg {
		if m.db == nil {
			return nil
		}

		// Root listing: treat directories as collections and keep collection metadata fresh.
		if len(msg.path) == 0 {
			for _, e := range msg.entries {
				if !e.IsDir {
					continue
				}
				if _, err := m.db.UpsertCollection(e.Name, e.Name+"/", index.GetCollectionDescription(e.Name)); err != nil {
					return browseIndexErrMsg{err: err}
				}
			}
			return nil
		}

		collectionName := msg.path[0]
		colID, err := m.db.UpsertCollection(collectionName, collectionName+"/", index.GetCollectionDescription(collectionName))
		if err != nil {
			return browseIndexErrMsg{err: err}
		}

		dirPath := msg.dirPath
		if dirPath == "" {
			dirPath = strings.Join(msg.path, "/") + "/"
		}
		dirID, err := m.db.UpsertDirectory(dirPath, colID)
		if err != nil {
			return browseIndexErrMsg{err: err}
		}

		if err := m.db.ClearDirectoryFiles(dirID); err != nil {
			return browseIndexErrMsg{err: err}
		}

		files := make([]index.FileRecord, 0, len(msg.entries))
		for _, e := range msg.entries {
			if e.IsDir {
				continue
			}
			files = append(files, index.FileRecord{
				Name:         e.Name,
				Path:         dirPath + e.Name,
				URL:          e.URL,
				Size:         e.Size,
				Date:         e.Date,
				DirectoryID:  dirID,
				CollectionID: colID,
			})
		}
		if len(files) > 0 {
			if err := m.db.InsertFileBatch(files); err != nil {
				return browseIndexErrMsg{err: err}
			}
		}
		if err := m.db.MarkDirectoryCrawled(dirID); err != nil {
			return browseIndexErrMsg{err: err}
		}
		return nil
	}
}

func (m Model) performSearch(query string, crawler *index.Crawler, job *searchJob) tea.Cmd {
	return func() tea.Msg {
		if m.db == nil {
			return searchErrMsg{err: fmt.Errorf("index unavailable")}
		}

		localResults, err := m.db.Search(query, 100)
		if err != nil {
			return searchErrMsg{err: err}
		}
		job.setResults(localResults)

		collections := chooseSearchRefreshCollections(m.db, query, localResults)
		if err := crawlSelectedCollections(context.Background(), crawler, collections); err != nil {
			return searchResultsMsg{
				results:     localResults,
				query:       query,
				localCount:  len(localResults),
				refreshWarn: fmt.Sprintf("Targeted refresh failed, showing local results: %v", err),
			}
		}

		midResults, err := m.db.Search(query, 100)
		if err == nil {
			job.setResults(midResults)
		}

		if err := crawler.CrawlAll(context.Background()); err != nil {
			results, serr := m.db.Search(query, 100)
			if serr != nil {
				results = job.getResults()
			}
			return searchResultsMsg{
				results:     results,
				query:       query,
				localCount:  len(localResults),
				refreshWarn: fmt.Sprintf("Full refresh interrupted: %v", err),
			}
		}

		results, err := m.db.Search(query, 100)
		if err != nil {
			return searchResultsMsg{
				results:     localResults,
				query:       query,
				localCount:  len(localResults),
				refreshWarn: fmt.Sprintf("Refreshed index, but search failed: %v", err),
			}
		}
		job.setResults(results)

		return searchResultsMsg{
			results:     results,
			query:       query,
			autoIndexed: true,
			localCount:  len(localResults),
		}
	}
}

func (m Model) previewSearch(query string) tea.Cmd {
	return func() tea.Msg {
		if m.db == nil {
			return searchPreviewMsg{query: query}
		}
		results, err := m.db.Search(query, 100)
		return searchPreviewMsg{query: query, results: results, err: err}
	}
}

func (m Model) maybeRefreshIndexInSearchTab() (tea.Model, tea.Cmd) {
	if m.db == nil || m.indexRefreshRunning || m.search.searching {
		return m, nil
	}
	if !m.searchLastRefresh.IsZero() && time.Since(m.searchLastRefresh) < 2*time.Minute {
		return m, nil
	}
	crawler := index.NewCrawler(m.client, m.db, m.cfg.IndexStaleDays)
	crawler.SetForce(false)
	crawler.SetWorkers(8)
	m.indexRefreshRunning = true
	m.indexRefreshCrawler = crawler
	m.search.bgRefreshing = true
	m.search.bgMsg = "Preparing background refresh..."
	m.search.bgPath = ""
	m.search.bgDirs = 0
	m.search.bgFiles = 0
	m.search.bgErrors = 0
	started := m.setStatus("Refreshing indexes in background for search...")
	return m, tea.Batch(started, m.refreshIndexInBackground(crawler), m.indexRefreshTick())
}

func (m Model) refreshIndexInBackground(crawler *index.Crawler) tea.Cmd {
	return func() tea.Msg {
		if err := crawler.CrawlAll(context.Background()); err != nil {
			return indexRefreshErrMsg{err: err}
		}
		p := crawler.Progress()
		return indexRefreshDoneMsg{dirs: p.DirsProcessed, files: p.FilesFound, errors: p.Errors}
	}
}

func (m Model) indexRefreshTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return indexRefreshTickMsg{}
	})
}

func chooseSearchRefreshCollections(db *index.DB, query string, local []index.SearchResult) []string {
	cols, err := db.GetCollections()
	if err != nil || len(cols) == 0 {
		return []string{"No-Intro"}
	}

	tokens := strings.Fields(strings.ToLower(query))
	localHits := map[string]int{}
	for _, r := range local {
		localHits[r.CollectionName]++
	}

	type scored struct {
		name  string
		score int
	}
	scoredCols := make([]scored, 0, len(cols))
	for _, c := range cols {
		s := scoreCollectionForQuery(strings.ToLower(c.Name), tokens)
		s += localHits[c.Name] * 120
		scoredCols = append(scoredCols, scored{name: c.Name, score: s})
	}

	sort.SliceStable(scoredCols, func(i, j int) bool {
		if scoredCols[i].score == scoredCols[j].score {
			return scoredCols[i].name < scoredCols[j].name
		}
		return scoredCols[i].score > scoredCols[j].score
	})

	maxCollections := 6
	if len(local) == 0 {
		maxCollections = 8
	}
	if maxCollections > len(scoredCols) {
		maxCollections = len(scoredCols)
	}

	ordered := make([]string, 0, maxCollections)
	for i := 0; i < maxCollections; i++ {
		if scoredCols[i].score <= 0 && len(ordered) > 0 {
			break
		}
		ordered = append(ordered, scoredCols[i].name)
	}
	if len(ordered) == 0 {
		ordered = append(ordered, "No-Intro")
	}
	return ordered
}

func scoreCollectionForQuery(collectionLower string, tokens []string) int {
	score := 0
	for _, t := range tokens {
		if len(t) < 2 {
			continue
		}
		if strings.Contains(collectionLower, t) {
			score += 80
		}
	}

	has := func(v string) bool { return strings.Contains(collectionLower, v) }

	for _, t := range tokens {
		switch t {
		case "nds", "nintendo", "ds", "3ds", "switch", "pokemon", "zelda", "mario", "kirby", "metroid", "fire", "emblem":
			if has("no-intro") {
				score += 220
			}
		case "ps1", "ps2", "ps3", "psp", "psx", "dreamcast", "gamecube", "wii", "xbox", "dvd", "cd", "blu", "ray":
			if has("redump") {
				score += 220
			}
		case "arcade", "mame", "neo", "cps", "naomi":
			if has("mame") || has("finalburn") {
				score += 220
			}
		case "dos", "pc", "windows", "msdos":
			if has("dos") || has("total dos") {
				score += 220
			}
		}
	}

	if len(tokens) == 0 {
		if has("no-intro") {
			score += 40
		}
	}

	return score
}

func crawlSelectedCollections(ctx context.Context, crawler *index.Crawler, collections []string) error {
	if len(collections) == 0 {
		return crawler.CrawlAll(ctx)
	}

	var firstErr error
	for _, col := range collections {
		if err := crawler.CrawlCollection(ctx, col); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (m Model) searchProgressTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return searchProgressTickMsg{}
	})
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var sb strings.Builder

	// Header
	header := titleStyle.Render("  Myrient TUI  ")
	sb.WriteString(header)
	sb.WriteString("\n")

	// Tabs
	tabs := []struct {
		name string
		tab  Tab
	}{
		{"Browse", TabBrowse},
		{"Search", TabSearch},
		{"Downloads", TabDownloads},
	}

	var tabLine strings.Builder
	for _, t := range tabs {
		label := fmt.Sprintf(" %s ", t.name)
		if m.activeTab == t.tab {
			tabLine.WriteString(tabActiveStyle.Render(label))
		} else {
			tabLine.WriteString(tabInactiveStyle.Render(label))
		}
		tabLine.WriteString(" ")
	}

	// Add download count badge.
	dlCount := m.dlManager.ActiveCount()
	if dlCount > 0 {
		badge := successStyle.Render(fmt.Sprintf(" [%d active]", dlCount))
		tabLine.WriteString(badge)
	}

	sb.WriteString(tabLine.String())
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", m.width))
	sb.WriteString("\n")

	// Content area.
	if m.showHelp {
		sb.WriteString(fitToHeight(m.helpView(m.height-8), m.height-8))
	} else {
		contentHeight := m.height - 8
		if contentHeight < 1 {
			contentHeight = 1
		}
		content := ""
		switch m.activeTab {
		case TabBrowse:
			content = m.browser.view(m.width, m.spinner.View())
		case TabSearch:
			content = m.search.view(m.width, m.spinner.View())
		case TabDownloads:
			content = m.downloads.view(m.width)
		}
		sb.WriteString(fitToHeight(content, contentHeight))
	}

	// Status bar.
	statusLine := m.statusMsg
	if statusLine == "" {
		statusLine = m.defaultStatus()
	}
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", m.width))
	sb.WriteString("\n")
	sb.WriteString(statusBarStyle.Width(m.width).Render(statusLine))

	return sb.String()
}

func fitToHeight(content string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	for len(lines) < maxLines {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) defaultStatus() string {
	switch m.activeTab {
	case TabBrowse:
		return "Arrows:navigate  Enter:open/download  type:filter  Backspace/Esc:clear filter  ?:help"
	case TabSearch:
		return "/:focus search  Arrows:results  Home/End/PgUp/PgDn:scroll  Enter:download  b:open in browser  ?:help"
	case TabDownloads:
		return "j/k:navigate  p:pause/resume  c:cancel  R:retry failed  x:clear done  r:refresh  ?:help"
	}
	return ""
}

func (m Model) helpView(maxLines int) string {
	lines := []string{
		"  Keyboard Shortcuts",
		"  ──────────────────",
		"",
		"  Global:",
		"    Tab           Switch views",
		"    Shift+Tab     Reverse view cycle",
		"    ?             Toggle help",
		"    q / Ctrl+C    Quit (double-press if downloads active)",
		"",
		"  Browser:",
		"    Up/Down       Navigate",
		"    Enter         Open directory / queue file",
		"    Backspace     Remove filter char / go up when filter empty",
		"    Home/End      Go to top/bottom",
		"    PgUp / PgDn   Page up/down",
		"    type letters  Filter entries",
		"    Esc           Clear filter",
		"",
		"  Search:",
		"    / or i        Focus search input",
		"    Enter         Search (when input focused)",
		"    Up/Down       Navigate results",
		"    Home/End      Go to top/bottom",
		"    PgUp / PgDn   Page up/down",
		"    Enter         Download selected",
		"    b / o         Open selected path in browser",
		"",
		"  Downloads:",
		"    j/k           Navigate",
		"    p             Pause/resume selected",
		"    c             Cancel selected",
		"    R             Retry failed",
		"    x             Clear completed/failed",
		"    r             Refresh list",
		"",
		"  Help view scroll: mouse wheel, j/k, PgUp/PgDn",
		"  Press ? or Esc to close help.",
	}

	if maxLines < 6 {
		maxLines = 6
	}
	helpOffset := m.helpOffset
	if helpOffset < 0 {
		helpOffset = 0
	}
	maxOffset := len(lines) - maxLines
	if maxOffset < 0 {
		maxOffset = 0
	}
	if helpOffset > maxOffset {
		helpOffset = maxOffset
	}

	end := helpOffset + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[helpOffset:end]
	if maxOffset > 0 {
		visible = append(visible, fmt.Sprintf("  [%d/%d]", helpOffset+1, maxOffset+1))
	}

	return helpStyle.Render(strings.Join(visible, "\n"))
}

func (m *Model) setStatus(msg string) tea.Cmd {
	m.statusMsg = msg
	m.statusID++
	id := m.statusID
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return statusClearMsg{id: id}
	})
}

func (m *Model) enqueueDownload(name, fileURL, subdir string) tea.Cmd {
	_, created := m.dlManager.Enqueue(name, fileURL, subdir)
	if !created {
		return m.setStatus(fmt.Sprintf("Already queued: %s", name))
	}
	return m.setStatus(fmt.Sprintf("Queued: %s", name))
}

func browsePathForSearchResult(filePath string) string {
	filePath = strings.Trim(filePath, "/")
	if filePath == "" {
		return ""
	}
	idx := strings.LastIndex(filePath, "/")
	if idx < 0 {
		return ""
	}
	return filePath[:idx+1]
}

func isTypeAheadKey(key string) bool {
	r := []rune(key)
	if len(r) != 1 {
		return false
	}
	ch := r[0]
	if ch < 32 {
		return false
	}
	return true
}

// Run starts the TUI.
func Run(c *client.Client, db *index.DB, cfg *config.Config, startPath string, opts RunOptions) error {
	m := NewModel(c, db, cfg, startPath)

	// Wire up download change notifications.
	programOpts := []tea.ProgramOption{}
	if opts.AltScreen {
		programOpts = append(programOpts, tea.WithAltScreen())
	}
	if opts.MouseMotion {
		programOpts = append(programOpts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(m, programOpts...)

	m.dlManager.SetOnChange(func() {
		go p.Send(downloadUpdateMsg{})
	})

	_, err := p.Run()
	return err
}

// Ensure lipgloss is used (it is used in styles.go, but keep the import valid).
var _ = lipgloss.NewStyle
