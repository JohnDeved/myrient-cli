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
	if s.cursor < len(s.results)-1 {
		s.cursor++
		if s.cursor >= s.offset+s.height {
			s.offset = s.cursor - s.height + 1
		}
	}
}

func (s *searchModel) pageUp() {
	if len(s.results) == 0 {
		s.cursor = 0
		s.offset = 0
		return
	}
	if s.height <= 0 {
		return
	}
	rel := s.cursor - s.offset
	s.offset -= s.height
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
	if s.height <= 0 {
		return
	}
	rel := s.cursor - s.offset
	s.offset += s.height
	maxOffset := len(s.results) - s.height
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

	// Search input
	sb.WriteString(s.input.View())
	sb.WriteString("\n\n")

	if s.searching {
		elapsed := ""
		if !s.startedAt.IsZero() {
			elapsed = fmt.Sprintf(" (%.0fs)", time.Since(s.startedAt).Seconds())
		}
		msg := s.loadingMsg
		if msg == "" {
			msg = "Searching local index (auto-indexing if needed)..."
		}
		sb.WriteString(fmt.Sprintf("  %s %s%s\n", spin, msg, elapsed))
		if s.input.Value() != "" {
			sb.WriteString(helpStyle.Render("  Query: " + s.input.Value()))
			sb.WriteString("\n")
		}
		if s.loadingPath != "" {
			sb.WriteString(helpStyle.Render("  Current Path:"))
			sb.WriteString("\n")
			sb.WriteString(helpStyle.Render("    " + util.TruncatePath(s.loadingPath, max(20, width-6))))
			sb.WriteString("\n")
		}
		sb.WriteString(helpStyle.Render(fmt.Sprintf("  Indexed Dirs:  %d", s.loadingDirs)))
		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render(fmt.Sprintf("  Indexed Files: %d", s.loadingFiles)))
		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render(fmt.Sprintf("  Errors:        %d", s.loadingErrors)))
		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render("  First-time/global searches can take a while while new directories are indexed."))
		sb.WriteString("\n")
		sb.WriteString("\n")
		if len(s.results) == 0 {
			return sb.String()
		}
	}

	if s.err != nil {
		sb.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", s.err)))
		sb.WriteString("\n")
		return sb.String()
	}

	if s.lastQuery != "" && len(s.results) == 0 {
		sb.WriteString(helpStyle.Render("  No results found.\n"))
		return sb.String()
	}

	if len(s.results) == 0 {
		sb.WriteString(helpStyle.Render("  Type to search the local index. Run 'myrient index' to build it.\n"))
		return sb.String()
	}

	sb.WriteString(helpStyle.Render(fmt.Sprintf("  Found %d results:\n\n", s.totalFound)))

	// Render results
	end := s.offset + s.height
	if end > len(s.results) {
		end = len(s.results)
	}

	for i := s.offset; i < end; i++ {
		r := s.results[i]
		isSelected := i == s.cursor

		name := fileStyle.Render(r.Name)
		col := collectionBadge.Render(r.CollectionName)
		size := sizeStyle.Render(r.Size)

		line := fmt.Sprintf("  %s  %s  %s", name, col, size)

		if isSelected {
			line = selectedStyle.Render(line)
		}

		sb.WriteString(line)
		sb.WriteString("\n")
		pathWidth := width - 6
		if pathWidth < 8 {
			pathWidth = 8
		}
		sb.WriteString(helpStyle.Render("    " + util.TruncatePath(r.Path, pathWidth)))
		sb.WriteString("\n")
	}

	if len(s.results) > s.height {
		pct := float64(s.offset) / float64(len(s.results)-s.height) * 100
		sb.WriteString(helpStyle.Render(
			fmt.Sprintf("  %d/%d results (%.0f%%)", s.cursor+1, len(s.results), pct),
		))
		sb.WriteString("\n")
	}

	return sb.String()
}
