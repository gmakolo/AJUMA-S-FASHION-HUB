package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned when a dress id or slug matches nothing.
var ErrNotFound = errors.New("dress not found")

// Store is the whole database: one JSON document, read into memory at boot
// and rewritten atomically on every change. A dressmaker's catalogue is a
// few dozen rows, so this is honest sizing rather than a shortcut — and it
// means the project clones and runs with nothing else installed.
type Store struct {
	mu   sync.RWMutex
	path string
	doc  document

	// baseOverride is set only by the static export; see OverrideBaseURL.
	baseOverride string
}

type document struct {
	NextRef  int      `json:"next_ref"`
	Settings Settings `json:"settings"`
	Dresses  []Dress  `json:"dresses"`
}

// OpenStore loads the catalogue from dir, creating it with defaults if absent.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	s := &Store{path: filepath.Join(dir, "catalogue.json")}

	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.doc = document{NextRef: 1, Settings: DefaultSettings()}
		if err := s.persist(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, fmt.Errorf("read catalogue: %w", err)
	default:
		if err := json.Unmarshal(raw, &s.doc); err != nil {
			return nil, fmt.Errorf("catalogue.json is not valid JSON: %w", err)
		}
	}

	if s.doc.NextRef < 1 {
		s.doc.NextRef = 1
	}
	if s.doc.Settings.MessageTemplate == "" {
		s.doc.Settings.MessageTemplate = DefaultMessageTemplate
	}
	if s.doc.Settings.BrandName == "" {
		s.doc.Settings.BrandName = DefaultSettings().BrandName
	}
	s.sortLocked()
	return s, nil
}

// persist writes the document to a temporary file and renames it over the
// real one, so a crash mid-write cannot leave a half-written catalogue.
func (s *Store) persist() error {
	raw, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode catalogue: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write catalogue: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace catalogue: %w", err)
	}
	return nil
}

// sortLocked orders the catalogue and renumbers positions 0..n-1 so the
// admin's up/down buttons always have contiguous numbers to work with.
func (s *Store) sortLocked() {
	sort.SliceStable(s.doc.Dresses, func(i, j int) bool {
		a, b := s.doc.Dresses[i], s.doc.Dresses[j]
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		return a.CreatedAt.After(b.CreatedAt)
	})
	for i := range s.doc.Dresses {
		s.doc.Dresses[i].Position = i
	}
}

// Settings returns a copy of the current settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := s.doc.Settings
	if s.baseOverride != "" {
		set.BaseURL = s.baseOverride
	}
	return set
}

// OverrideBaseURL replaces the shop's recorded public address for the lifetime
// of this process without writing anything to the catalogue. The static export
// uses it so that a build can be aimed at a host — where the absolute links in
// a WhatsApp message have to point — without editing the owner's settings.
func (s *Store) OverrideBaseURL(base string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baseOverride = strings.TrimRight(strings.TrimSpace(base), "/")
}

// SaveSettings replaces the settings, stamping the change time.
func (s *Store) SaveSettings(next Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next.UpdatedAt = time.Now()
	s.doc.Settings = next
	return s.persist()
}

// Dresses returns every dress in display order.
func (s *Store) Dresses() []Dress {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Dress, len(s.doc.Dresses))
	copy(out, s.doc.Dresses)
	return out
}

func (s *Store) DressesByOwner(ownerID string) []Dress {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Dress
	for _, d := range s.doc.Dresses {
		if d.OwnerID == ownerID {
			out = append(out, d)
		}
	}
	return out
}

// Categories lists the distinct categories in use, alphabetically.
func (s *Store) Categories() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	for _, d := range s.doc.Dresses {
		if c := strings.TrimSpace(d.Category); c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// BySlug finds a dress by its URL fragment.
func (s *Store) BySlug(slug string) (Dress, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.doc.Dresses {
		if d.Slug == slug {
			return d, true
		}
	}
	return Dress{}, false
}

// RecordView counts one view per signed-in member for each design.
func (s *Store) RecordView(id, memberID string) (Dress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.doc.Dresses {
		d := &s.doc.Dresses[i]
		if d.ID != id {
			continue
		}
		for _, viewer := range d.Viewers {
			if viewer == memberID {
				return *d, nil
			}
		}
		before := *d
		d.Viewers = append(d.Viewers, memberID)
		if err := s.persist(); err != nil {
			*d = before
			return Dress{}, err
		}
		return *d, nil
	}
	return Dress{}, ErrNotFound
}

// ToggleReaction records or removes one reaction per signed-in member.
func (s *Store) ToggleReaction(id, memberID string) (Dress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.doc.Dresses {
		d := &s.doc.Dresses[i]
		if d.ID != id {
			continue
		}
		before := *d
		before.Viewers = append([]string(nil), d.Viewers...)
		before.Reactions = append([]string(nil), d.Reactions...)
		for j, reaction := range d.Reactions {
			if reaction == memberID {
				d.Reactions = append(d.Reactions[:j], d.Reactions[j+1:]...)
				if err := s.persist(); err != nil {
					*d = before
					return Dress{}, err
				}
				return *d, nil
			}
		}
		d.Reactions = append(d.Reactions, memberID)
		if err := s.persist(); err != nil {
			*d = before
			return Dress{}, err
		}
		return *d, nil
	}
	return Dress{}, ErrNotFound
}

// ByID finds a dress by its identifier.
func (s *Store) ByID(id string) (Dress, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.doc.Dresses {
		if d.ID == id {
			return d, true
		}
	}
	return Dress{}, false
}

// Create files a new dress, allocating its id, reference and slug.
func (s *Store) Create(d Dress) (Dress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	d.ID = NewID("d_")
	d.Ref = fmt.Sprintf("AJM-%03d", s.doc.NextRef)
	s.doc.NextRef++
	d.Slug = s.uniqueSlugLocked(Slugify(d.Name), "")
	d.CreatedAt, d.UpdatedAt = now, now
	d.Position = -1 // newest work leads the lookbook

	s.doc.Dresses = append(s.doc.Dresses, d)
	s.sortLocked()
	if err := s.persist(); err != nil {
		return Dress{}, err
	}
	// Hand back the stored copy rather than the local one: sortLocked has just
	// renumbered every position, including this dress's.
	for _, stored := range s.doc.Dresses {
		if stored.ID == d.ID {
			return stored, nil
		}
	}
	return d, nil
}

// Update overwrites the stored dress with the same id, keeping its
// reference, creation time and position.
func (s *Store) Update(d Dress) (Dress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.doc.Dresses {
		if existing.ID != d.ID {
			continue
		}
		d.Ref = existing.Ref
		d.CreatedAt = existing.CreatedAt
		d.Position = existing.Position
		d.UpdatedAt = time.Now()
		d.Slug = s.uniqueSlugLocked(Slugify(d.Name), d.ID)
		s.doc.Dresses[i] = d
		if err := s.persist(); err != nil {
			return Dress{}, err
		}
		return d, nil
	}
	return Dress{}, ErrNotFound
}

// Delete removes a dress and returns it so its uploads can be cleaned up.
func (s *Store) Delete(id string) (Dress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.doc.Dresses {
		if d.ID != id {
			continue
		}
		s.doc.Dresses = append(s.doc.Dresses[:i], s.doc.Dresses[i+1:]...)
		s.sortLocked()
		if err := s.persist(); err != nil {
			return Dress{}, err
		}
		return d, nil
	}
	return Dress{}, ErrNotFound
}

// DeleteOwned removes a member's dress only when the signed-in member owns it.
func (s *Store) DeleteOwned(id, ownerID string) (Dress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.doc.Dresses {
		if d.ID != id || d.OwnerID != ownerID {
			continue
		}
		s.doc.Dresses = append(s.doc.Dresses[:i], s.doc.Dresses[i+1:]...)
		s.sortLocked()
		if err := s.persist(); err != nil {
			return Dress{}, err
		}
		return d, nil
	}
	return Dress{}, ErrNotFound
}

// Move shifts a dress one place up (-1) or down (+1) the lookbook.
func (s *Store) Move(id string, delta int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.doc.Dresses {
		if d.ID != id {
			continue
		}
		j := i + delta
		if j < 0 || j >= len(s.doc.Dresses) {
			return nil // already at the end; nothing to do
		}
		s.doc.Dresses[i], s.doc.Dresses[j] = s.doc.Dresses[j], s.doc.Dresses[i]
		for k := range s.doc.Dresses {
			s.doc.Dresses[k].Position = k
		}
		return s.persist()
	}
	return ErrNotFound
}

// uniqueSlugLocked keeps slugs distinct, appending -2, -3 … when a name repeats.
func (s *Store) uniqueSlugLocked(base, exceptID string) string {
	if base == "" {
		base = "dress"
	}
	taken := func(candidate string) bool {
		for _, d := range s.doc.Dresses {
			if d.Slug == candidate && d.ID != exceptID {
				return true
			}
		}
		return false
	}
	slug := base
	for n := 2; taken(slug); n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	return slug
}
