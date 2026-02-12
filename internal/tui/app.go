package tui

import (
	"context"
	"fmt"
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
		return m, nil

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
		if m.searchJob != nil {
			live := m.searchJob.getResults()
			if len(live) > 0 {
				m.search.results = live
				m.search.totalFound = len(live)
				if m.search.cursor >= len(m.search.results) {
					m.search.cursor = len(m.search.results) - 1
					if m.search.cursor < 0 {
						m.search.cursor = 0
					}
				}
			}
		}
		if m.searchCrawler != nil {
			p := m.searchCrawler.Progress()
			if p.CurrentPath != "" {
				m.search.loadingMsg = "Refreshing index..."
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
				return m, nil
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
			if !m.search.input.Focused() {
				m.search.moveUp()
			}
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
			if !m.search.input.Focused() {
				m.search.moveDown()
			}
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
				m.search.searching = true
				m.search.startedAt = time.Now()
				m.search.loadingMsg = "Searching local index..."
				m.search.lastQuery = query
				crawler := index.NewCrawler(m.client, m.db, m.cfg.IndexStaleDays)
				crawler.SetForce(true)
				m.searchCrawler = crawler
				job := &searchJob{}
				m.searchJob = job
				return m, tea.Batch(m.performSearch(query, crawler, job), m.searchProgressTick())
			}
		case "esc":
			m.search.input.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.search.input, cmd = m.search.input.Update(msg)
			return m, cmd
		}
	} else {
		switch key {
		case "up", "k":
			m.search.moveUp()
		case "down", "j":
			m.search.moveDown()
		case "pgup", "ctrl+u":
			m.search.pageUp()
		case "pgdown", "ctrl+d":
			m.search.pageDown()
		case "enter":
			// Download selected result.
			if sel := m.search.selected(); sel != nil {
				return m, m.enqueueDownload(sel.Name, sel.URL, sel.CollectionName)
			}
		case "i", "/":
			m.search.input.Focus()
		case "d":
			if sel := m.search.selected(); sel != nil {
				return m, m.enqueueDownload(sel.Name, sel.URL, sel.CollectionName)
			}
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

		return entriesMsg{entries: entries, path: segments}
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

		if err := crawler.CrawlAll(context.Background()); err != nil {
			return searchResultsMsg{
				results:     localResults,
				query:       query,
				localCount:  len(localResults),
				refreshWarn: fmt.Sprintf("Index refresh failed, showing local results: %v", err),
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

func (m Model) searchProgressTick() tea.Cmd {
	return tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg {
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
		sb.WriteString(m.helpView(m.height - 8))
	} else {
		switch m.activeTab {
		case TabBrowse:
			sb.WriteString(m.browser.view(m.width, m.spinner.View()))
		case TabSearch:
			sb.WriteString(m.search.view(m.width, m.spinner.View()))
		case TabDownloads:
			sb.WriteString(m.downloads.view(m.width))
		}
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

func (m Model) defaultStatus() string {
	switch m.activeTab {
	case TabBrowse:
		return "Arrows:navigate  Enter:open/download  type:filter  Backspace/Esc:clear filter  ?:help"
	case TabSearch:
		return "/:focus search  j/k:navigate results  Enter/d:download  b:open in browser  ?:help"
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
		"    j/k           Navigate results",
		"    d / Enter     Download selected",
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
