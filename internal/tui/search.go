package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/JohnDeved/myrient-cli/internal/index"
	"github.com/JohnDeved/myrient-cli/internal/util"
)

// searchModel manages the search view.
type searchModel struct {
	input      textinput.Model
	results    []index.SearchResult
	cursor     int
	offset     int
	height     int
	viewportRows int
	searching  bool
	startedAt  time.Time
	loadingMsg string
	loadingPath string
	loadingDirs int64
	loadingFiles int64
	loadingErrors int64
	err        error
	lastQuery  string
	totalFound int
}

func (s *searchModel) pageSize() int {
	if s.viewportRows > 0 {
		return s.viewportRows
	}
	if s.height > 0 {
		return s.height
	}
	return 1
}

func newSearchModel() searchModel {
	ti := textinput.New()
	ti.Placeholder = "Search for games, ROMs, collections..."
	ti.CharLimit = 256
	ti.Width = 60
	ti.Prompt = "Search: "
	ti.PromptStyle = searchPromptStyle
	return searchModel{
		input:  ti,
		height: 20,
	}
}

func (s *searchModel) normalizeViewport() {
	rows := s.pageSize()
	if len(s.results) == 0 {
		s.cursor = 0
		s.offset = 0
		return
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= len(s.results) {
		s.cursor = len(s.results) - 1
	}
	if s.offset < 0 {
		s.offset = 0
	}
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if rows > 0 && s.cursor >= s.offset+rows {
		s.offset = s.cursor - rows + 1
	}
	maxOffset := len(s.results) - rows
	if maxOffset < 0 {
		maxOffset = 0
	}
	if s.offset > maxOffset {
		s.offset = maxOffset
	}
}

func (s *searchModel) setResults(results []index.SearchResult) {
	s.results = results
	s.totalFound = len(results)
	s.cursor = 0
	s.offset = 0
	s.searching = false
	s.startedAt = time.Time{}
	s.loadingMsg = ""
	s.loadingPath = ""
	s.loadingDirs = 0
	s.loadingFiles = 0
	s.loadingErrors = 0
	s.err = nil
}

func (s *searchModel) setError(err error) {
	s.err = err
	s.searching = false
	s.startedAt = time.Time{}
	s.loadingMsg = ""
	s.loadingPath = ""
	s.loadingDirs = 0
	s.loadingFiles = 0
	s.loadingErrors = 0
}

func (s *searchModel) selected() *index.SearchResult {
	s.normalizeViewport()
	if s.cursor >= 0 && s.cursor < len(s.results) {
		return &s.results[s.cursor]
	}
	return nil
}

func (s *searchModel) moveUp() {
	if s.cursor > 0 {
		s.cursor--
		if s.cursor < s.offset {
			s.offset = s.cursor
		}
	}
}

func (s *searchModel) moveDown() {
	rows := s.pageSize()
	if s.cursor < len(s.results)-1 {
		s.cursor++
		if s.cursor >= s.offset+rows {
			s.offset = s.cursor - rows + 1
		}
	}
}

func (s *searchModel) pageUp() {
	if len(s.results) == 0 {
		s.cursor = 0
		s.offset = 0
		return
	}
	rows := s.pageSize()
	if rows <= 0 {
		return
	}
	rel := s.cursor - s.offset
	s.offset -= rows
	if s.offset < 0 {
		s.offset = 0
	}
	s.cursor = s.offset + rel
	if s.cursor >= len(s.results) {
		s.cursor = len(s.results) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *searchModel) pageDown() {
	rows := s.pageSize()
	if rows <= 0 {
		return
	}
	rel := s.cursor - s.offset
	s.offset += rows
	maxOffset := len(s.results) - rows
	if maxOffset < 0 {
		maxOffset = 0
	}
	if s.offset > maxOffset {
		s.offset = maxOffset
	}
	s.cursor = s.offset + rel
	if s.cursor >= len(s.results) {
		s.cursor = len(s.results) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *searchModel) view(width int, spin string) string {
	var sb strings.Builder
	s.normalizeViewport()
	usedLines := 0

	// Search input
	sb.WriteString(padToWidth(s.input.View(), width))
	sb.WriteString("\n\n")
	usedLines += 2

	if s.searching {
		elapsed := ""
		if !s.startedAt.IsZero() {
			elapsed = fmt.Sprintf(" (%.0fs)", time.Since(s.startedAt).Seconds())
		}
		msg := s.loadingMsg
		if msg == "" {
			msg = "Searching local index (auto-indexing if needed)..."
		}
		sb.WriteString(padToWidth(helpStyle.Render("  Progress"), width))
		sb.WriteString("\n")
		usedLines++
		sb.WriteString(padToWidth(fmt.Sprintf("  %s %s%s", spin, msg, elapsed), width))
		sb.WriteString("\n")
		usedLines++
		if s.input.Value() != "" {
			sb.WriteString(padToWidth(helpStyle.Render("  Query: "+s.input.Value()), width))
			sb.WriteString("\n")
			usedLines++
		}
		if s.loadingPath != "" {
			sb.WriteString(padToWidth(helpStyle.Render("  Current Path:"), width))
			sb.WriteString("\n")
			usedLines++
			sb.WriteString(padToWidth(helpStyle.Render("    "+util.TruncatePath(s.loadingPath, max(20, width-6))), width))
			sb.WriteString("\n")
			usedLines++
		}
		sb.WriteString(padToWidth(helpStyle.Render(fmt.Sprintf("  Indexed Dirs:  %d", s.loadingDirs)), width))
		sb.WriteString("\n")
		usedLines++
		sb.WriteString(padToWidth(helpStyle.Render(fmt.Sprintf("  Indexed Files: %d", s.loadingFiles)), width))
		sb.WriteString("\n")
		usedLines++
		sb.WriteString(padToWidth(helpStyle.Render(fmt.Sprintf("  Errors:        %d", s.loadingErrors)), width))
		sb.WriteString("\n")
		usedLines++
		sb.WriteString(padToWidth(helpStyle.Render("  First-time/global searches can take a while while new directories are indexed."), width))
		sb.WriteString("\n")
		usedLines++
		if len(s.results) > 0 {
			sb.WriteString(padToWidth(helpStyle.Render(fmt.Sprintf("  Live Results:  %d (navigable while indexing)", len(s.results))), width))
			sb.WriteString("\n")
			usedLines++
		}
		sb.WriteString("\n")
		usedLines++
		if len(s.results) == 0 {
			return sb.String()
		}
	}

	if s.err != nil {
		sb.WriteString(padToWidth(errorStyle.Render(fmt.Sprintf("  Error: %v", s.err)), width))
		sb.WriteString("\n")
		return sb.String()
	}

	if s.lastQuery != "" && len(s.results) == 0 {
		sb.WriteString(padToWidth(helpStyle.Render("  No results found."), width))
		sb.WriteString("\n")
		usedLines++
		return sb.String()
	}

	if len(s.results) == 0 {
		sb.WriteString(padToWidth(helpStyle.Render("  Type to search the local index. Run 'myrient index' to build it."), width))
		sb.WriteString("\n")
		usedLines++
		return sb.String()
	}

	sb.WriteString(padToWidth(helpStyle.Render(fmt.Sprintf("  Found %d results:", s.totalFound)), width))
	sb.WriteString("\n\n")
	usedLines += 2

	resultDetailsLines := 0
	scrollInfoLines := 0
	if len(s.results) > s.pageSize() {
		scrollInfoLines = 1
	}
	availableRows := s.height - usedLines - resultDetailsLines - scrollInfoLines
	if availableRows < 1 {
		availableRows = 1
	}
	s.viewportRows = availableRows
	s.normalizeViewport()

	// Render results
	end := s.offset + availableRows
	if end > len(s.results) {
		end = len(s.results)
	}
	rowWidth := width - selectedStyle.GetHorizontalFrameSize()
	if rowWidth < 12 {
		rowWidth = 12
	}

	for i := s.offset; i < end; i++ {
		r := s.results[i]
		isSelected := i == s.cursor
		line := renderBrowseLikeRow(r.Name, r.Size, r.Date, false, rowWidth, isSelected)
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	if len(s.results) > availableRows {
		pct := 0.0
		if len(s.results)-availableRows > 0 {
			pct = float64(s.offset) / float64(len(s.results)-availableRows) * 100
		}
		sb.WriteString(padToWidth(helpStyle.Render(
			fmt.Sprintf("  %d/%d results (%.0f%%)", s.cursor+1, len(s.results), pct),
		), width))
		sb.WriteString("\n")
	}

	return sb.String()
}
