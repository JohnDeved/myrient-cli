package tui

import (
	"context"
	"fmt"
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
	batchConfirm bool
	startPath    string
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

	case "1":
		if searchFocused {
			return m.handleSearchKey(key, msg)
		}
		m.batchConfirm = false
		m.activeTab = TabBrowse
		m.search.input.Blur()
		return m, nil

	case "2":
		if searchFocused {
			return m.handleSearchKey(key, msg)
		}
		m.batchConfirm = false
		m.activeTab = TabSearch
		m.search.input.Focus()
		return m, nil

	case "3":
		if searchFocused {
			return m.handleSearchKey(key, msg)
		}
		m.batchConfirm = false
		m.activeTab = TabDownloads
		m.search.input.Blur()
		m.downloads.setItems(m.dlManager.Items())
		return m, nil

	case "tab":
		m.batchConfirm = false
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
		m.batchConfirm = false
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
	if m.batchConfirm {
		switch key {
		case "y", "Y", "enter":
			m.batchConfirm = false
			return m.startMarkedDownloads(true)
		case "n", "N", "esc":
			m.batchConfirm = false
			return m, m.setStatus("Batch download canceled")
		default:
			return m, nil
		}
	}

	switch key {
	case "up", "k":
		m.browser.moveUp()
	case "down", "j":
		m.browser.moveDown()
	case "pgup", "ctrl+u":
		m.browser.pageUp()
	case "pgdown", "ctrl+d":
		m.browser.pageDown()
	case "home", "g":
		m.browser.goHome()
	case "end", "G":
		m.browser.goEnd()

	case "enter", "l", "right":
		if sel := m.browser.selected(); sel != nil && sel.IsDir {
			newPath := append([]string{}, m.browser.path...)
			newPath = append(newPath, sel.Name)
			m.browser.loading = true
			return m, m.loadDirectory(strings.Join(newPath, "/") + "/")
		} else if sel != nil {
			subdir := strings.Join(m.browser.path, "/")
			return m, m.enqueueDownload(sel.Name, sel.URL, subdir)
		}

	case "backspace", "h", "left":
		if len(m.browser.path) > 0 {
			parentPath := ""
			if len(m.browser.path) > 1 {
				parentPath = strings.Join(m.browser.path[:len(m.browser.path)-1], "/") + "/"
			}
			m.browser.loading = true
			return m, m.loadDirectory(parentPath)
		}

	case " ":
		m.browser.toggleMark()
		m.browser.moveDown()

	case "d":
		return m.startMarkedDownloads(false)

	case "esc":
		cleared := 0
		for i := range m.browser.entries {
			if m.browser.entries[i].Marked {
				m.browser.entries[i].Marked = false
				cleared++
			}
		}
		if cleared > 0 {
			return m, m.setStatus(fmt.Sprintf("Cleared %d marks", cleared))
		}

	case "a":
		// Select all files.
		for i := range m.browser.entries {
			if !m.browser.entries[i].IsDir {
				m.browser.entries[i].Marked = true
			}
		}

	case "A":
		// Clear all marks.
		for i := range m.browser.entries {
			if !m.browser.entries[i].IsDir {
				m.browser.entries[i].Marked = false
			}
		}

	default:
		if isTypeAheadKey(key) {
			m.browser.typeAheadFind(key)
		}
	}

	return m, nil
}

func (m Model) handleSearchKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.search.input.Focused() {
		switch key {
		case "enter":
			query := m.search.input.Value()
			if query != "" {
				m.search.searching = true
				return m, m.performSearch(query)
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

func (m Model) startMarkedDownloads(confirmed bool) (Model, tea.Cmd) {
	marked := m.browser.markedEntries()
	if len(marked) == 0 {
		// Download current selection if it's a file.
		if sel := m.browser.selected(); sel != nil && !sel.IsDir {
			subdir := strings.Join(m.browser.path, "/")
			return m, m.enqueueDownload(sel.Name, sel.URL, subdir)
		}
	} else {
		if len(marked) > 1 && !confirmed {
			m.batchConfirm = true
			return m, m.setStatus(fmt.Sprintf("Download %d marked files? Press y to confirm, n to cancel", len(marked)))
		}
		subdir := strings.Join(m.browser.path, "/")
		queued := 0
		duplicates := 0
		for _, e := range marked {
			_, created := m.dlManager.Enqueue(e.Name, e.URL, subdir)
			if created {
				queued++
			} else {
				duplicates++
			}
		}
		status := fmt.Sprintf("Queued %d files", queued)
		if duplicates > 0 {
			status = fmt.Sprintf("Queued %d files (%d already queued)", queued, duplicates)
		}
		cmd := m.setStatus(status)
		// Clear marks.
		for i := range m.browser.entries {
			m.browser.entries[i].Marked = false
		}
		return m, cmd
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

func (m Model) performSearch(query string) tea.Cmd {
	return func() tea.Msg {
		if m.db == nil {
			return searchResultsMsg{query: query}
		}

		results, err := m.db.Search(query, 100)
		if err != nil {
			return searchErrMsg{err: err}
		}

		return searchResultsMsg{results: results, query: query}
	}
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
		key  string
	}{
		{"Browse", TabBrowse, "1"},
		{"Search", TabSearch, "2"},
		{"Downloads", TabDownloads, "3"},
	}

	var tabLine strings.Builder
	for _, t := range tabs {
		label := fmt.Sprintf(" %s %s ", t.key, t.name)
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

	markedCount := m.browser.markedCount()
	if markedCount > 0 && m.activeTab == TabBrowse {
		badge := markedStyle.Render(fmt.Sprintf(" [%d marked]", markedCount))
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
	if m.activeTab == TabBrowse && m.batchConfirm {
		return "Confirm batch download: y/Enter confirm, n/Esc cancel"
	}
	switch m.activeTab {
	case TabBrowse:
		return "j/k:navigate  Enter:open/download  type:jump  Space:mark  d:download  ?:help"
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
		"    Tab / 1-3     Switch views",
		"    Shift+Tab     Reverse view cycle",
		"    ?             Toggle help",
		"    q / Ctrl+C    Quit (double-press if downloads active)",
		"",
		"  Browser:",
		"    j/k / Up/Down Navigate",
		"    Enter / l     Open directory / queue file",
		"    Backspace / h Go up",
		"    Space         Mark/unmark file",
		"    a / A         Mark all / clear marks",
		"    d             Download marked/selected",
		"    g / G         Go to top/bottom",
		"    PgUp / PgDn   Page up/down",
		"    type letters  Jump to name",
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
	if ch < 32 || ch == ' ' {
		return false
	}
	return true
}

// Run starts the TUI.
func Run(c *client.Client, db *index.DB, cfg *config.Config, startPath string) error {
	m := NewModel(c, db, cfg, startPath)

	// Wire up download change notifications.
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	m.dlManager.SetOnChange(func() {
		p.Send(downloadUpdateMsg{})
	})

	_, err := p.Run()
	return err
}

// Ensure lipgloss is used (it is used in styles.go, but keep the import valid).
var _ = lipgloss.NewStyle
