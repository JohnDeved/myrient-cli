package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/JohnDeved/myrient-cli/internal/client"
	"github.com/JohnDeved/myrient-cli/internal/util"
)

// browserEntry is a client.Entry plus local state.
type browserEntry struct {
	client.Entry
	Marked bool
}

// browserModel manages the directory browser view.
type browserModel struct {
	entries   []browserEntry
	cursor    int
	path      []string // breadcrumb path segments
	markCache map[string]map[string]bool
	filter    string
	typeAhead string
	typedAt   time.Time
	loading   bool
	err       error
	offset    int // viewport scroll offset
	height    int // visible area height
}

func (b *browserModel) visibleIndices() []int {
	indices := make([]int, 0, len(b.entries))
	q := strings.ToLower(strings.TrimSpace(b.filter))
	for i, e := range b.entries {
		if q == "" || strings.Contains(strings.ToLower(e.Name), q) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (b *browserModel) normalizeViewport(total int) {
	if total <= 0 {
		b.cursor = 0
		b.offset = 0
		return
	}
	if b.cursor < 0 {
		b.cursor = 0
	}
	if b.cursor >= total {
		b.cursor = total - 1
	}
	if b.offset < 0 {
		b.offset = 0
	}
	if b.cursor < b.offset {
		b.offset = b.cursor
	}
	if b.height > 0 && b.cursor >= b.offset+b.height {
		b.offset = b.cursor - b.height + 1
	}
	maxOffset := total - b.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if b.offset > maxOffset {
		b.offset = maxOffset
	}
}

func (b *browserModel) appendFilter(ch string) {
	b.filter += strings.ToLower(ch)
	b.cursor = 0
	b.offset = 0
}

func (b *browserModel) backspaceFilter() {
	if b.filter == "" {
		return
	}
	r := []rune(b.filter)
	b.filter = string(r[:len(r)-1])
	b.cursor = 0
	b.offset = 0
}

func (b *browserModel) clearFilter() {
	b.filter = ""
	b.cursor = 0
	b.offset = 0
}

func (b *browserModel) typeAheadFind(key string) {
	if len(b.entries) == 0 {
		return
	}
	now := time.Now()
	if now.Sub(b.typedAt) > time.Second {
		b.typeAhead = ""
	}
	b.typedAt = now
	b.typeAhead += strings.ToLower(key)

	start := b.cursor + 1
	for i := 0; i < len(b.entries); i++ {
		idx := (start + i) % len(b.entries)
		name := strings.ToLower(b.entries[idx].Name)
		if strings.HasPrefix(name, b.typeAhead) {
			b.cursor = idx
			if b.cursor < b.offset {
				b.offset = b.cursor
			}
			if b.cursor >= b.offset+b.height {
				b.offset = b.cursor - b.height + 1
			}
			return
		}
	}
}

func newBrowserModel() browserModel {
	return browserModel{
		height:    20,
		markCache: make(map[string]map[string]bool),
	}
}

func (b *browserModel) setPathAndEntries(path []string, entries []client.Entry) {
	b.persistMarks()
	b.filter = ""
	b.typeAhead = ""
	b.path = path
	b.setEntries(entries)
}

func (b *browserModel) setEntries(entries []client.Entry) {
	b.entries = make([]browserEntry, len(entries))
	for i, e := range entries {
		b.entries[i] = browserEntry{Entry: e}
	}
	b.restoreMarks()
	b.cursor = 0
	b.offset = 0
	b.loading = false
	b.err = nil
}

func (b *browserModel) persistMarks() {
	if len(b.entries) == 0 {
		return
	}
	path := b.currentPath()
	marked := make(map[string]bool)
	for _, e := range b.entries {
		if !e.IsDir && e.Marked {
			marked[e.Name] = true
		}
	}
	if len(marked) == 0 {
		delete(b.markCache, path)
		return
	}
	b.markCache[path] = marked
}

func (b *browserModel) restoreMarks() {
	path := b.currentPath()
	marked, ok := b.markCache[path]
	if !ok {
		return
	}
	for i := range b.entries {
		if b.entries[i].IsDir {
			continue
		}
		b.entries[i].Marked = marked[b.entries[i].Name]
	}
}

func (b *browserModel) setError(err error) {
	b.err = err
	b.loading = false
}

func (b *browserModel) currentPath() string {
	if len(b.path) == 0 {
		return ""
	}
	return strings.Join(b.path, "/") + "/"
}

func (b *browserModel) breadcrumb() string {
	parts := append([]string{"/"}, b.path...)
	return strings.Join(parts, " > ")
}

func (b *browserModel) selected() *browserEntry {
	visible := b.visibleIndices()
	b.normalizeViewport(len(visible))
	if b.cursor >= 0 && b.cursor < len(visible) {
		return &b.entries[visible[b.cursor]]
	}
	return nil
}

func (b *browserModel) moveUp() {
	visible := b.visibleIndices()
	b.normalizeViewport(len(visible))
	if b.cursor > 0 {
		b.cursor--
		if b.cursor < b.offset {
			b.offset = b.cursor
		}
	}
}

func (b *browserModel) moveDown() {
	visible := b.visibleIndices()
	b.normalizeViewport(len(visible))
	if b.cursor < len(visible)-1 {
		b.cursor++
		if b.cursor >= b.offset+b.height {
			b.offset = b.cursor - b.height + 1
		}
	}
}

func (b *browserModel) pageUp() {
	visible := b.visibleIndices()
	if len(visible) == 0 {
		b.cursor = 0
		b.offset = 0
		return
	}
	if b.height <= 0 {
		return
	}
	rel := b.cursor - b.offset
	b.offset -= b.height
	if b.offset < 0 {
		b.offset = 0
	}
	b.cursor = b.offset + rel
	if b.cursor < 0 {
		b.cursor = 0
	}
	if b.cursor >= len(visible) {
		b.cursor = len(visible) - 1
	}
	if b.cursor < 0 {
		b.cursor = 0
	}
}

func (b *browserModel) pageDown() {
	visible := b.visibleIndices()
	if len(visible) == 0 {
		b.cursor = 0
		b.offset = 0
		return
	}
	if b.height <= 0 {
		return
	}
	rel := b.cursor - b.offset
	b.offset += b.height
	maxOffset := len(visible) - b.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if b.offset > maxOffset {
		b.offset = maxOffset
	}
	b.cursor = b.offset + rel
	if b.cursor >= len(visible) {
		b.cursor = len(visible) - 1
	}
	if b.cursor < 0 {
		b.cursor = 0
	}
}

func (b *browserModel) goHome() {
	b.cursor = 0
	b.offset = 0
}

func (b *browserModel) goEnd() {
	visible := b.visibleIndices()
	b.cursor = len(visible) - 1
	if b.cursor < 0 {
		b.cursor = 0
	}
	b.offset = b.cursor - b.height + 1
	if b.offset < 0 {
		b.offset = 0
	}
}

func (b *browserModel) toggleMark() {
	if sel := b.selected(); sel != nil && !sel.IsDir {
		sel.Marked = !sel.Marked
	}
}

func (b *browserModel) markedEntries() []browserEntry {
	var marked []browserEntry
	for _, e := range b.entries {
		if e.Marked {
			marked = append(marked, e)
		}
	}
	return marked
}

func (b *browserModel) markedCount() int {
	count := 0
	for _, e := range b.entries {
		if e.Marked {
			count++
		}
	}
	return count
}

func (b *browserModel) view(width int, spin string) string {
	var sb strings.Builder
	breadWidth := width - 2
	if breadWidth < 8 {
		breadWidth = 8
	}

	// Breadcrumb
	sb.WriteString(breadcrumbStyle.Render(util.TruncatePath(b.breadcrumb(), breadWidth)))
	sb.WriteString("\n")

	if b.loading {
		sb.WriteString(fmt.Sprintf("\n  %s Loading...\n", spin))
		return sb.String()
	}

	if b.err != nil {
		sb.WriteString("\n")
		sb.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", b.err)))
		sb.WriteString("\n")
		return sb.String()
	}

	if len(b.entries) == 0 {
		sb.WriteString("\n  (empty directory)\n")
		return sb.String()
	}

	if b.filter != "" {
		sb.WriteString(helpStyle.Render(fmt.Sprintf("  Filter: %q", b.filter)))
		sb.WriteString("\n")
	}

	visible := b.visibleIndices()
	b.normalizeViewport(len(visible))
	if len(visible) == 0 {
		sb.WriteString(helpStyle.Render("  No matches for current filter."))
		sb.WriteString("\n")
		return sb.String()
	}

	// Calculate visible range.
	end := b.offset + b.height
	if end > len(visible) {
		end = len(visible)
	}

	// Render entries.
	rowWidth := width - selectedStyle.GetHorizontalFrameSize()
	if rowWidth < 12 {
		rowWidth = 12
	}
	for i := b.offset; i < end; i++ {
		e := b.entries[visible[i]]
		isSelected := i == b.cursor
		line := renderBrowseLikeRow(e.Name, e.Size, e.Date, e.IsDir, rowWidth, isSelected)
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	// Scroll indicator.
	if len(visible) > b.height {
		pct := float64(b.offset) / float64(len(visible)-b.height) * 100
		sb.WriteString(helpStyle.Render(
			fmt.Sprintf("  %d/%d items (%.0f%%)", b.cursor+1, len(visible), pct),
		))
		sb.WriteString("\n")
	}

	return sb.String()
}

func renderBrowseLikeRow(name, size, date string, isDir bool, rowWidth int, isSelected bool) string {
	var icon string
	var displayName string
	if isDir {
		icon = " "
		displayName = dirStyle.Render(truncateText(name+"/", max(12, rowWidth-35)))
	} else {
		icon = " "
		displayName = fileStyle.Render(truncateText(name, max(12, rowWidth-35)))
	}

	line := fmt.Sprintf("  %s%s  %s  %s",
		icon,
		displayName,
		sizeStyle.Render(size),
		dateStyle.Render(date),
	)

	if isSelected {
		return selectedStyle.Render(padToWidth(line, rowWidth))
	}
	return normalStyle.Render(padToWidth(line, rowWidth))
}

func truncateText(s string, maxWidth int) string {
	if maxWidth < 4 {
		return s
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	r := []rune(s)
	if len(r) <= maxWidth {
		return s
	}
	return string(r[:maxWidth-3]) + "..."
}

func padToWidth(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
