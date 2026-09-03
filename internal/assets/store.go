package assets

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// StoredImage holds the extracted image binary data and metadata.
type StoredImage struct {
	ID          string
	Filename    string
	ContentType string
	SizeBytes   int64
	AltText     string
	Location    string
	Data        []byte
	CreatedAt   time.Time
}

// Store is a thread-safe in-memory asset cache with capacity management.
type Store struct {
	mu       sync.RWMutex
	items    map[string]*StoredImage
	maxItems int
}

var (
	defaultStore *Store
	once         sync.Once
)

// GetDefaultStore returns the global shared asset store.
func GetDefaultStore() *Store {
	once.Do(func() {
		defaultStore = NewStore(500)
	})
	return defaultStore
}

// NewStore initializes a new Store with a maximum item limit.
func NewStore(maxItems int) *Store {
	if maxItems <= 0 {
		maxItems = 500
	}
	s := &Store{
		items:    make(map[string]*StoredImage),
		maxItems: maxItems,
	}
	// Start periodic cleanup of items older than 2 hours
	go s.cleanupLoop()
	return s
}

// Save stores an image and returns its unique ID and URL path.
func (s *Store) Save(filename, contentType, altText, location string, data []byte) (id, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If at capacity, evict oldest items
	if len(s.items) >= s.maxItems {
		s.evictOldest(s.maxItems / 5) // evict 20%
	}

	randomBytes := make([]byte, 12)
	_, _ = rand.Read(randomBytes)
	id = hex.EncodeToString(randomBytes)

	if contentType == "" {
		contentType = DetectImageMIME(filename, data)
	}

	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ExtensionFromMIME(contentType)
	}

	url = fmt.Sprintf("/api/v1/assets/%s%s", id, ext)

	s.items[id] = &StoredImage{
		ID:          id,
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		AltText:     altText,
		Location:    location,
		Data:        data,
		CreatedAt:   time.Now(),
	}

	return id, url
}

// Get retrieves an image by ID or filename.
func (s *Store) Get(query string) (*StoredImage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Direct ID match
	if item, exists := s.items[query]; exists {
		return item, true
	}

	// 2. Clean ID without ext
	cleanID := strings.TrimSuffix(query, filepath.Ext(query))
	if item, exists := s.items[cleanID]; exists {
		return item, true
	}

	// 3. Match by Filename
	for _, item := range s.items {
		if strings.EqualFold(item.Filename, query) || strings.EqualFold(item.Filename, filepath.Base(query)) {
			return item, true
		}
	}

	return nil, false
}

func (s *Store) evictOldest(count int) {
	if count <= 0 {
		count = 10
	}
	type entry struct {
		id string
		t  time.Time
	}
	var entries []entry
	for k, v := range s.items {
		entries = append(entries, entry{id: k, t: v.CreatedAt})
	}
	// Sort oldest first
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].t.Before(entries[i].t) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	for i := 0; i < count && i < len(entries); i++ {
		delete(s.items, entries[i].id)
	}
}

func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	for range ticker.C {
		s.mu.Lock()
		cutoff := time.Now().Add(-2 * time.Hour)
		for k, v := range s.items {
			if v.CreatedAt.Before(cutoff) {
				delete(s.items, k)
			}
		}
		s.mu.Unlock()
	}
}

// DetectImageMIME detects MIME type from extension or magic bytes.
func DetectImageMIME(filename string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".bmp":
		return "image/bmp"
	}

	if len(data) > 0 {
		mimeType := http.DetectContentType(data)
		if strings.HasPrefix(mimeType, "image/") {
			return mimeType
		}
	}

	return "image/png"
}

// ExtensionFromMIME maps MIME type to extension.
func ExtensionFromMIME(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	default:
		return ".png"
	}
}
