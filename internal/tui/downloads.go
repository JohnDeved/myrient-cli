package tui

import (
	"fmt"
	"strings"

	"github.com/johannberger/myrient/internal/downloader"
	"github.com/johannberger/myrient/internal/util"
)

// downloadsModel manages the downloads view.
type downloadsModel struct {
	items  []*downloader.Item
	cursor int
	offset int
	height int
}

func newDownloadsModel() downloadsModel {
	return downloadsModel{
		height: 20,
	}
}

func (d *downloadsModel) setItems(items []*downloader.Item) {
	d.items = items
	if d.cursor >= len(d.items) {
		d.cursor = len(d.items) - 1
		if d.cursor < 0 {
			d.cursor = 0
		}
	}
}

func (d *downloadsModel) moveUp() {
	if d.cursor > 0 {
		d.cursor--
		if d.cursor < d.offset {
			d.offset = d.cursor
		}
	}
}

func (d *downloadsModel) moveDown() {
	if d.cursor < len(d.items)-1 {
		d.cursor++
		if d.cursor >= d.offset+d.height {
			d.offset = d.cursor - d.height + 1
		}
	}
}

func (d *downloadsModel) pageUp() {
	if len(d.items) == 0 {
		d.cursor = 0
		d.offset = 0
		return
	}
	if d.height <= 0 {
		return
	}
	rel := d.cursor - d.offset
	d.offset -= d.height
	if d.offset < 0 {
		d.offset = 0
	}
	d.cursor = d.offset + rel
	if d.cursor >= len(d.items) {
		d.cursor = len(d.items) - 1
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
}

func (d *downloadsModel) pageDown() {
	if d.height <= 0 {
		return
	}
	rel := d.cursor - d.offset
	d.offset += d.height
	maxOffset := len(d.items) - d.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if d.offset > maxOffset {
		d.offset = maxOffset
	}
	d.cursor = d.offset + rel
	if d.cursor >= len(d.items) {
		d.cursor = len(d.items) - 1
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
}

func (d *downloadsModel) selected() *downloader.Item {
	if d.cursor >= 0 && d.cursor < len(d.items) {
		return d.items[d.cursor]
	}
	return nil
}

func (d *downloadsModel) view(width int) string {
	var sb strings.Builder

	if len(d.items) == 0 {
		sb.WriteString(helpStyle.Render("\n  No downloads. Mark files with Space, then press d to download.\n"))
		return sb.String()
	}

	// Stats line.
	active, queued, completed, failed := 0, 0, 0, 0
	for _, it := range d.items {
		it.Mu.Lock()
		switch it.Status {
		case downloader.StatusActive:
			active++
		case downloader.StatusQueued:
			queued++
		case downloader.StatusCompleted:
			completed++
		case downloader.StatusFailed:
			failed++
		}
		it.Mu.Unlock()
	}

	stats := fmt.Sprintf("  Active: %d  Queued: %d  Completed: %d  Failed: %d",
		active, queued, completed, failed)
	sb.WriteString(helpStyle.Render(stats))
	sb.WriteString("\n\n")

	end := d.offset + d.height
	if end > len(d.items) {
		end = len(d.items)
	}

	barWidth := 30
	if width > 100 {
		barWidth = 40
	}

	for i := d.offset; i < end; i++ {
		it := d.items[i]
		isSelected := i == d.cursor

		it.Mu.Lock()
		status := it.Status
		name := it.Name
		errVal := it.Error
		it.Mu.Unlock()

		progress := it.Progress()
		speed := it.Speed()
		done := it.DoneBytes.Load()
		total := it.TotalBytes

		// Status indicator.
		var statusStr string
		switch status {
		case downloader.StatusQueued:
			statusStr = helpStyle.Render("[Queued]")
		case downloader.StatusActive:
			statusStr = successStyle.Render("[Downloading]")
		case downloader.StatusCompleted:
			statusStr = successStyle.Render("[Done]")
		case downloader.StatusFailed:
			statusStr = errorStyle.Render("[Failed]")
		case downloader.StatusPaused:
			statusStr = helpStyle.Render("[Paused]")
		}

		// Progress bar.
		bar := renderProgressBar(progress, barWidth)

		// Speed/size info.
		var sizeInfo string
		if total > 0 {
			sizeInfo = fmt.Sprintf("%s / %s", util.FormatBytes(done), util.FormatBytes(total))
		} else if done > 0 {
			sizeInfo = util.FormatBytes(done)
		}

		var speedInfo string
		if status == downloader.StatusActive && speed > 0 {
			speedInfo = fmt.Sprintf(" %s/s", util.FormatBytes(int64(speed)))
		}

		line := fmt.Sprintf("  %s %s  %s  %s%s",
			statusStr, name, bar, sizeInfo, speedInfo)

		if errVal != nil {
			line += "  " + errorStyle.Render(errVal.Error())
		}

		if isSelected {
			line = selectedStyle.Render(line)
		}

		sb.WriteString(line)
		sb.WriteString("\n")

		if width > 80 {
			dest := helpStyle.Render("    to: " + util.TruncatePath(it.DestPath, width-8))
			sb.WriteString(dest)
			sb.WriteString("\n")
		}
	}

	if len(d.items) > d.height {
		pct := float64(d.offset) / float64(len(d.items)-d.height) * 100
		sb.WriteString(helpStyle.Render(
			fmt.Sprintf("  %d/%d downloads (%.0f%%)", d.cursor+1, len(d.items), pct),
		))
		sb.WriteString("\n")
	}

	return sb.String()
}

func renderProgressBar(progress float64, width int) string {
	filled := int(progress * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled

	bar := progressBarFilled.Render(strings.Repeat("█", filled)) +
		progressBarEmpty.Render(strings.Repeat("░", empty))

	return fmt.Sprintf("[%s] %3.0f%%", bar, progress*100)
}
