package index

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/JohnDeved/myrient-cli/internal/client"
)

// CrawlProgress reports crawl progress.
type CrawlProgress struct {
	CurrentPath   string
	DirsProcessed int64
	FilesFound    int64
	Errors        int64
}

// Crawler recursively indexes Myrient directory listings.
type Crawler struct {
	client     *client.Client
	db         *DB
	staleDays  int
	force      bool
	workers    int
	progress   atomic.Pointer[CrawlProgress]
	onProgress func(CrawlProgress)
	mu         sync.Mutex
	dirsProc   atomic.Int64
	filesFound atomic.Int64
	errCount   atomic.Int64
}

// SetForce controls whether stale checks are skipped.
func (cr *Crawler) SetForce(force bool) {
	cr.force = force
}

// NewCrawler creates a new crawler.
func NewCrawler(c *client.Client, db *DB, staleDays int) *Crawler {
	cr := &Crawler{
		client:    c,
		db:        db,
		staleDays: staleDays,
		workers:   4,
	}
	return cr
}

// SetWorkers controls collection crawl worker parallelism.
func (cr *Crawler) SetWorkers(workers int) {
	if workers < 1 {
		workers = 1
	}
	cr.workers = workers
}

// SetProgressCallback sets a function called on progress updates.
func (cr *Crawler) SetProgressCallback(fn func(CrawlProgress)) {
	cr.onProgress = fn
}

// Progress returns the latest crawl progress.
func (cr *Crawler) Progress() CrawlProgress {
	p := cr.progress.Load()
	if p == nil {
		return CrawlProgress{}
	}
	return *p
}

func (cr *Crawler) reportProgress(path string) {
	p := CrawlProgress{
		CurrentPath:   path,
		DirsProcessed: cr.dirsProc.Load(),
		FilesFound:    cr.filesFound.Load(),
		Errors:        cr.errCount.Load(),
	}
	cr.progress.Store(&p)
	if cr.onProgress != nil {
		cr.onProgress(p)
	}
}

// CrawlAll crawls all top-level collections.
func (cr *Crawler) CrawlAll(ctx context.Context) error {
	entries, err := cr.client.ListDirectory(ctx, "")
	if err != nil {
		return fmt.Errorf("listing root: %w", err)
	}

	var collections []string
	for _, e := range entries {
		if e.IsDir {
			collections = append(collections, e.Name)
		}
	}
	if len(collections) == 0 {
		return nil
	}

	workers := cr.workers
	if workers > len(collections) {
		workers = len(collections)
	}

	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for collection := range jobs {
				if err := cr.CrawlCollection(ctx, collection); err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("Error crawling collection %s: %v", collection, err)
					cr.errCount.Add(1)
				}
			}
		}()
	}

	for _, name := range collections {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case jobs <- name:
		}
	}
	close(jobs)
	wg.Wait()

	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

// CrawlCollection crawls a single top-level collection.
func (cr *Crawler) CrawlCollection(ctx context.Context, collectionName string) error {
	collPath := collectionName + "/"
	colID, err := cr.db.UpsertCollection(collectionName, collPath, "")
	if err != nil {
		return fmt.Errorf("upserting collection %s: %w", collectionName, err)
	}

	return cr.crawlDir(ctx, collPath, colID)
}

// crawlDir recursively crawls a directory.
func (cr *Crawler) crawlDir(ctx context.Context, dirPath string, colID int64) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	cr.reportProgress(dirPath)

	// Check if this directory is stale.
	if !cr.force {
		stale, err := cr.db.IsDirectoryStale(dirPath, cr.staleDays)
		if err != nil {
			return err
		}
		if !stale {
			cr.dirsProc.Add(1)
			return nil
		}
	}

	entries, err := cr.client.ListDirectory(ctx, dirPath)
	if err != nil {
		cr.errCount.Add(1)
		return fmt.Errorf("listing %s: %w", dirPath, err)
	}

	dirID, err := cr.db.UpsertDirectory(dirPath, colID)
	if err != nil {
		return err
	}

	// Clear old files for this directory before re-indexing.
	if err := cr.db.ClearDirectoryFiles(dirID); err != nil {
		return err
	}

	// Separate files and subdirectories.
	var files []FileRecord
	var subdirs []string

	for _, e := range entries {
		if e.IsDir {
			subdirs = append(subdirs, dirPath+e.Name+"/")
		} else {
			files = append(files, FileRecord{
				Name:         e.Name,
				Path:         dirPath + e.Name,
				URL:          e.URL,
				Size:         e.Size,
				Date:         e.Date,
				DirectoryID:  dirID,
				CollectionID: colID,
			})
		}
	}

	// Batch insert files.
	if len(files) > 0 {
		if err := cr.db.InsertFileBatch(files); err != nil {
			return fmt.Errorf("inserting files for %s: %w", dirPath, err)
		}
		cr.filesFound.Add(int64(len(files)))
	}

	// Mark directory as crawled.
	if err := cr.db.MarkDirectoryCrawled(dirID); err != nil {
		return err
	}
	cr.dirsProc.Add(1)

	// Recurse into subdirectories.
	for _, subdir := range subdirs {
		if err := cr.crawlDir(ctx, subdir, colID); err != nil {
			// Log error but continue with other subdirectories.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("Error crawling %s: %v", subdir, err)
			cr.errCount.Add(1)
		}
	}

	return nil
}

// CollectionDescriptions maps known collection names to their descriptions.
var CollectionDescriptions = map[string]string{
	"No-Intro":                      "Content for non-optical disk-based systems and digital platforms",
	"Redump":                        "Content for optical disc-based systems",
	"TOSEC":                         "Software for various non-optical disk-based electronics",
	"TOSEC-ISO":                     "Software for various optical disc-based electronics",
	"TOSEC-PIX":                     "Scans of various software and hardware manuals and magazines",
	"MAME":                          "Content for the arcade emulator MAME",
	"HBMAME":                        "Homebrew content not cataloged in MAME",
	"FinalBurn Neo":                 "Content for the multi-system arcade emulator FinalBurn Neo",
	"Hardware Target Game Database": "Content for use with flash carts",
	"Internet Archive":              "Content at risk of removal from the Internet Archive",
	"Eggman's Arcade Repository":    "A collection of arcade dumps",
	"RetroAchievements":             "Content compatible with RetroAchievements",
	"T-En Collection":               "Content translated into English",
	"Total DOS Collection":          "DOS and bootable games for IBM PC",
	"TeknoParrot":                   "Content for the arcade emulator TeknoParrot",
	"bitsavers":                     "Software and documentation for vintage computers",
	"eXo":                           "Projects focused on preserving content for various platforms",
	"Laserdisc Collection":          "A collection of Laserdisc content",
	"Lost Level":                    "Content not cataloged in No-Intro or Redump",
	"Miscellaneous":                 "Various content requested to be added",
	"Touhou Project Collection":     "Content relating to the Touhou Project series",
}

// GetCollectionDescription returns a description for a known collection.
func GetCollectionDescription(name string) string {
	// Try exact match first.
	if desc, ok := CollectionDescriptions[name]; ok {
		return desc
	}
	// Try prefix match for sub-collections.
	for key, desc := range CollectionDescriptions {
		if strings.HasPrefix(name, key) {
			return desc
		}
	}
	return ""
}
