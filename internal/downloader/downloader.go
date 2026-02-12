package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JohnDeved/myrient-cli/internal/client"
)

// Status represents a download's state.
type Status int

const (
	StatusQueued Status = iota
	StatusActive
	StatusPaused
	StatusCompleted
	StatusFailed
)

func (s Status) String() string {
	switch s {
	case StatusQueued:
		return "Queued"
	case StatusActive:
		return "Downloading"
	case StatusPaused:
		return "Paused"
	case StatusCompleted:
		return "Completed"
	case StatusFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// Item represents a single download.
type Item struct {
	ID          int
	Name        string
	URL         string
	DestPath    string
	TotalBytes  int64
	DoneBytes   atomic.Int64
	Status      Status
	Error       error
	StartedAt   time.Time
	CompletedAt time.Time
	cancel      context.CancelFunc
	Mu          sync.Mutex
}

// Progress returns a snapshot of the download's progress.
func (it *Item) Progress() float64 {
	total := it.TotalBytes
	done := it.DoneBytes.Load()
	if total <= 0 {
		return 0
	}
	return float64(done) / float64(total)
}

// Speed returns the approximate download speed in bytes per second.
func (it *Item) Speed() float64 {
	it.Mu.Lock()
	started := it.StartedAt
	it.Mu.Unlock()

	if started.IsZero() {
		return 0
	}
	elapsed := time.Since(started).Seconds()
	if elapsed < 0.1 {
		return 0
	}
	return float64(it.DoneBytes.Load()) / elapsed
}

// Manager manages concurrent downloads.
type Manager struct {
	client      *client.Client
	downloadDir string
	maxParallel int

	mu         sync.Mutex
	items      []*Item
	nextID     int
	sem        chan struct{}
	onChange   func()
	lastNotify time.Time
}

var errCancelled = errors.New("cancelled")

// NewManager creates a download manager.
func NewManager(c *client.Client, downloadDir string, maxParallel int) *Manager {
	return &Manager{
		client:      c,
		downloadDir: downloadDir,
		maxParallel: maxParallel,
		sem:         make(chan struct{}, maxParallel),
	}
}

// SetOnChange sets a callback invoked when any download's state changes.
func (m *Manager) SetOnChange(fn func()) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

func (m *Manager) notify(force bool) {
	m.mu.Lock()
	fn := m.onChange
	if !force {
		now := time.Now()
		if now.Sub(m.lastNotify) < 100*time.Millisecond {
			m.mu.Unlock()
			return
		}
		m.lastNotify = now
	} else {
		m.lastNotify = time.Now()
	}
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Enqueue adds a download to the queue and starts processing.
// Returns the item and whether a new queue entry was created.
func (m *Manager) Enqueue(name, fileURL, subdir string) (*Item, bool) {
	m.mu.Lock()
	destDir := m.downloadDir
	if subdir != "" {
		destDir = filepath.Join(destDir, subdir)
	}
	destPath := filepath.Join(destDir, name)

	for _, it := range m.items {
		it.Mu.Lock()
		duplicate := (it.URL == fileURL || it.DestPath == destPath) && it.Status != StatusFailed
		it.Mu.Unlock()
		if duplicate {
			m.mu.Unlock()
			return it, false
		}
	}

	m.nextID++
	id := m.nextID

	item := &Item{
		ID:       id,
		Name:     name,
		URL:      fileURL,
		DestPath: destPath,
		Status:   StatusQueued,
	}
	m.items = append(m.items, item)
	m.mu.Unlock()

	m.notify(true)

	// Start download in background.
	go m.processItem(item)

	return item, true
}

// HasActive returns true when any item is queued, active, or paused.
func (m *Manager) HasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		it.Mu.Lock()
		status := it.Status
		it.Mu.Unlock()
		if status == StatusQueued || status == StatusActive || status == StatusPaused {
			return true
		}
	}
	return false
}

// CancelAll cancels all active or queued downloads.
func (m *Manager) CancelAll() {
	m.mu.Lock()
	for _, it := range m.items {
		it.Mu.Lock()
		switch it.Status {
		case StatusQueued, StatusActive, StatusPaused:
			if it.cancel != nil {
				it.cancel()
			}
			it.Status = StatusFailed
			it.Error = errCancelled
		}
		it.Mu.Unlock()
	}
	m.mu.Unlock()
	m.notify(true)
}

// Items returns a snapshot of all download items.
func (m *Manager) Items() []*Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*Item, len(m.items))
	copy(result, m.items)
	return result
}

// ClearFinished removes completed and failed downloads from the list.
func (m *Manager) ClearFinished() int {
	m.mu.Lock()
	kept := m.items[:0]
	removed := 0
	for _, it := range m.items {
		it.Mu.Lock()
		status := it.Status
		it.Mu.Unlock()
		if status == StatusCompleted || status == StatusFailed {
			removed++
			continue
		}
		kept = append(kept, it)
	}
	m.items = kept
	m.mu.Unlock()
	if removed > 0 {
		m.notify(true)
	}
	return removed
}

// ActiveCount returns the number of currently downloading items.
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, it := range m.items {
		it.Mu.Lock()
		if it.Status == StatusActive {
			count++
		}
		it.Mu.Unlock()
	}
	return count
}

// Cancel cancels a download by ID.
func (m *Manager) Cancel(id int) {
	m.mu.Lock()
	for _, it := range m.items {
		if it.ID == id {
			it.Mu.Lock()
			if it.Status == StatusQueued || it.Status == StatusActive || it.Status == StatusPaused {
				it.Status = StatusFailed
				it.Error = errCancelled
			}
			if it.cancel != nil {
				it.cancel()
			}
			it.Mu.Unlock()
			break
		}
	}
	m.mu.Unlock()
	m.notify(true)
}

// Pause pauses an active or queued download.
func (m *Manager) Pause(id int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.ID != id {
			continue
		}
		it.Mu.Lock()
		if it.Status != StatusActive && it.Status != StatusQueued {
			it.Mu.Unlock()
			return false
		}
		it.Status = StatusPaused
		it.Error = nil
		if it.cancel != nil {
			it.cancel()
		}
		it.Mu.Unlock()
		go m.notify(true)
		return true
	}
	return false
}

// Resume resumes a paused download.
func (m *Manager) Resume(id int) bool {
	m.mu.Lock()
	var target *Item
	for _, it := range m.items {
		if it.ID == id {
			target = it
			break
		}
	}
	m.mu.Unlock()
	if target == nil {
		return false
	}

	target.Mu.Lock()
	if target.Status != StatusPaused {
		target.Mu.Unlock()
		return false
	}
	target.Status = StatusQueued
	target.Error = nil
	target.cancel = nil
	target.Mu.Unlock()

	m.notify(true)
	go m.processItem(target)
	return true
}

// Retry restarts a failed download item.
func (m *Manager) Retry(id int) bool {
	m.mu.Lock()
	var target *Item
	for _, it := range m.items {
		if it.ID == id {
			target = it
			break
		}
	}
	m.mu.Unlock()

	if target == nil {
		return false
	}

	target.Mu.Lock()
	if target.Status != StatusFailed {
		target.Mu.Unlock()
		return false
	}
	target.Status = StatusQueued
	target.Error = nil
	target.StartedAt = time.Time{}
	target.CompletedAt = time.Time{}
	target.cancel = nil
	target.Mu.Unlock()

	m.notify(true)
	go m.processItem(target)
	return true
}

func (m *Manager) processItem(item *Item) {
	// Acquire semaphore slot.
	m.sem <- struct{}{}
	defer func() { <-m.sem }()

	ctx, cancel := context.WithCancel(context.Background())
	item.Mu.Lock()
	if item.Status == StatusFailed && errors.Is(item.Error, errCancelled) {
		item.Mu.Unlock()
		cancel()
		return
	}
	if item.Status == StatusPaused {
		item.Mu.Unlock()
		cancel()
		return
	}
	item.cancel = cancel
	item.Status = StatusActive
	item.StartedAt = time.Now()
	item.Error = nil
	item.Mu.Unlock()
	m.notify(true)

	err := m.downloadFile(ctx, item)

	item.Mu.Lock()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			if item.Status != StatusPaused {
				item.Status = StatusFailed
				item.Error = errCancelled
			}
		} else {
			item.Status = StatusFailed
			item.Error = err
		}
	} else {
		item.Status = StatusCompleted
		item.CompletedAt = time.Now()
	}
	item.Mu.Unlock()
	cancel()
	m.notify(true)
}

func (m *Manager) downloadFile(ctx context.Context, item *Item) error {
	// Ensure destination directory exists.
	dir := filepath.Dir(item.DestPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	partPath := item.DestPath + ".part"

	// Check for existing partial download.
	var resumeFrom int64
	if info, err := os.Stat(partPath); err == nil {
		resumeFrom = info.Size()
		item.DoneBytes.Store(resumeFrom)
	}

	body, contentLength, resumed, err := m.client.DownloadFile(ctx, item.URL, resumeFrom)
	if err != nil {
		return err
	}
	defer body.Close()

	// Calculate total size.
	if contentLength > 0 {
		if resumed {
			item.TotalBytes = resumeFrom + contentLength
		} else {
			item.TotalBytes = contentLength
		}
	}

	// Open file for writing (append if resuming).
	flags := os.O_WRONLY | os.O_CREATE
	if resumed {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		item.DoneBytes.Store(0)
	}

	f, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	// Copy with progress tracking.
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return fmt.Errorf("writing file: %w", werr)
			}
			item.DoneBytes.Add(int64(n))
			m.notify(false)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading response: %w", err)
		}
	}

	// Rename .part to final name.
	f.Close()
	if err := os.Rename(partPath, item.DestPath); err != nil {
		return fmt.Errorf("renaming file: %w", err)
	}

	return nil
}
