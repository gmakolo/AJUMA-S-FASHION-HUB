package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// newTestStore opens a store in a directory that disappears with the test.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return store
}

func TestOpenStoreCreatesADefaultCatalogue(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "catalogue.json")); err != nil {
		t.Fatalf("the catalogue was not written: %v", err)
	}
	if set := store.Settings(); set.BrandName != "Ajuma Fashion Hub" {
		t.Errorf("BrandName = %q, want the default", set.BrandName)
	}
	if len(store.Dresses()) != 0 {
		t.Error("a fresh shop should have no dresses")
	}
}

func TestStoreSurvivesAReopen(t *testing.T) {
	dir := t.TempDir()
	first, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	made, err := first.Create(Dress{Name: "Adaeze Wrap Dress", PriceMinor: 3450000, Category: "Wrap"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	set := first.Settings()
	set.WhatsApp = "2348155604988"
	if err := first.SaveSettings(set); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	second, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	back, ok := second.BySlug("adaeze-wrap-dress")
	if !ok {
		t.Fatal("the dress did not survive the reopen")
	}
	if back.ID != made.ID || back.Ref != made.Ref || back.PriceMinor != 3450000 {
		t.Errorf("the dress came back changed: %+v", back)
	}
	if second.Settings().WhatsApp != "2348155604988" {
		t.Error("the settings did not survive the reopen")
	}
	// References keep counting rather than starting over.
	next, err := second.Create(Dress{Name: "Zuri Column Gown"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if next.Ref != "AJM-002" {
		t.Errorf("Ref = %q, want AJM-002", next.Ref)
	}
}

func TestCreateAllocatesIdentity(t *testing.T) {
	store := newTestStore(t)
	first, _ := store.Create(Dress{Name: "Adaeze Wrap Dress"})
	if first.ID == "" || !strings.HasPrefix(first.ID, "d_") {
		t.Errorf("ID = %q, want a d_ prefix", first.ID)
	}
	if first.Ref != "AJM-001" {
		t.Errorf("Ref = %q, want AJM-001", first.Ref)
	}
	if first.Slug != "adaeze-wrap-dress" {
		t.Errorf("Slug = %q", first.Slug)
	}
	if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
		t.Error("the timestamps were not stamped")
	}

	// A repeated name must not steal the first dress's URL.
	same, _ := store.Create(Dress{Name: "Adaeze Wrap Dress"})
	if same.Slug != "adaeze-wrap-dress-2" {
		t.Errorf("Slug = %q, want adaeze-wrap-dress-2", same.Slug)
	}
	// A name with nothing sluggable in it still needs a URL.
	odd, _ := store.Create(Dress{Name: "!!!"})
	if odd.Slug != "dress" {
		t.Errorf("Slug = %q, want dress", odd.Slug)
	}
}

// The newest piece leads the lookbook, and positions stay contiguous.
func TestCreatePutsTheNewestFirst(t *testing.T) {
	store := newTestStore(t)
	for _, name := range []string{"First", "Second", "Third"} {
		if _, err := store.Create(Dress{Name: name}); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}
	all := store.Dresses()
	if len(all) != 3 {
		t.Fatalf("got %d dresses, want 3", len(all))
	}
	if all[0].Name != "Third" || all[2].Name != "First" {
		t.Errorf("order = %s, %s, %s", all[0].Name, all[1].Name, all[2].Name)
	}
	for i, d := range all {
		if d.Position != i {
			t.Errorf("%s has position %d, want %d", d.Name, d.Position, i)
		}
	}
}

func TestUpdateKeepsWhatItShould(t *testing.T) {
	store := newTestStore(t)
	store.Create(Dress{Name: "Filler"})
	original, _ := store.Create(Dress{Name: "Zuri Column Gown", PriceMinor: 5900000})

	edited := original
	edited.Name = "Zuri Gown"
	edited.PriceMinor = 6200000
	edited.Ref = "NONSENSE"
	edited.Position = 99
	saved, err := store.Update(edited)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if saved.Ref != original.Ref {
		t.Errorf("Ref = %q, want it kept as %q", saved.Ref, original.Ref)
	}
	if saved.Position != original.Position {
		t.Errorf("Position = %d, want it kept as %d", saved.Position, original.Position)
	}
	if !saved.CreatedAt.Equal(original.CreatedAt) {
		t.Error("CreatedAt should not move on an edit")
	}
	if saved.Slug != "zuri-gown" {
		t.Errorf("Slug = %q, want it to follow the new name", saved.Slug)
	}
	if saved.PriceMinor != 6200000 {
		t.Errorf("PriceMinor = %d, want the new price", saved.PriceMinor)
	}
	// The old URL is free again, and the new one resolves.
	if _, ok := store.BySlug("zuri-column-gown"); ok {
		t.Error("the old slug still resolves")
	}
	if _, ok := store.BySlug("zuri-gown"); !ok {
		t.Error("the new slug does not resolve")
	}
	// Editing a dress must not make its own slug collide with itself.
	again, _ := store.Update(saved)
	if again.Slug != "zuri-gown" {
		t.Errorf("Slug = %q after a second save, want zuri-gown", again.Slug)
	}
}

func TestUpdateAndDeleteReportAMissingDress(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Update(Dress{ID: "d_nope", Name: "Ghost"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update of an unknown id returned %v, want ErrNotFound", err)
	}
	if _, err := store.Delete("d_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete of an unknown id returned %v, want ErrNotFound", err)
	}
	if err := store.Move("d_nope", -1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Move of an unknown id returned %v, want ErrNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	store := newTestStore(t)
	keep, _ := store.Create(Dress{Name: "Keep"})
	drop, _ := store.Create(Dress{Name: "Drop", Images: []Image{{Src: "/media/x.jpg"}}})

	gone, err := store.Delete(drop.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// The caller needs the images back so the files can be cleaned up.
	if len(gone.Images) != 1 {
		t.Error("Delete should hand back the dress it removed, photographs included")
	}
	if _, ok := store.ByID(drop.ID); ok {
		t.Error("the dress is still in the catalogue")
	}
	all := store.Dresses()
	if len(all) != 1 || all[0].ID != keep.ID || all[0].Position != 0 {
		t.Errorf("after a delete the catalogue is %+v", all)
	}
}

func TestMove(t *testing.T) {
	store := newTestStore(t)
	store.Create(Dress{Name: "First"})
	store.Create(Dress{Name: "Second"})
	store.Create(Dress{Name: "Third"}) // order is Third, Second, First

	names := func() string {
		var out []string
		for _, d := range store.Dresses() {
			out = append(out, d.Name)
		}
		return strings.Join(out, ",")
	}
	first := store.Dresses()[2]
	if err := store.Move(first.ID, -1); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if got := names(); got != "Third,First,Second" {
		t.Errorf("after moving up: %s", got)
	}
	if err := store.Move(first.ID, 1); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if got := names(); got != "Third,Second,First" {
		t.Errorf("after moving back down: %s", got)
	}
	// Off either end is a no-op, not an error.
	top := store.Dresses()[0]
	if err := store.Move(top.ID, -1); err != nil {
		t.Errorf("moving the first dress up returned %v", err)
	}
	if err := store.Move(first.ID, 1); err != nil {
		t.Errorf("moving the last dress down returned %v", err)
	}
	if got := names(); got != "Third,Second,First" {
		t.Errorf("a no-op move changed the order: %s", got)
	}
}

func TestCategories(t *testing.T) {
	store := newTestStore(t)
	store.Create(Dress{Name: "A", Category: "Wrap"})
	store.Create(Dress{Name: "B", Category: "Gown"})
	store.Create(Dress{Name: "C", Category: "Wrap"})
	store.Create(Dress{Name: "D", Category: "  "})
	store.Create(Dress{Name: "E"})

	if got := strings.Join(store.Categories(), ","); got != "Gown,Wrap" {
		t.Errorf("Categories = %q, want %q", got, "Gown,Wrap")
	}
}

// Dresses hands out a copy: a caller poking at the slice must not reach into
// the catalogue behind the mutex.
func TestDressesIsACopy(t *testing.T) {
	store := newTestStore(t)
	store.Create(Dress{Name: "Adaeze Wrap Dress"})

	all := store.Dresses()
	all[0].Name = "Vandalised"
	if store.Dresses()[0].Name != "Adaeze Wrap Dress" {
		t.Error("editing the returned slice changed the catalogue")
	}
}

func TestOpenStoreRepairsAThinDocument(t *testing.T) {
	dir := t.TempDir()
	raw := `{"next_ref":0,"settings":{"whatsapp":"2348155604988"},"dresses":[]}`
	if err := os.WriteFile(filepath.Join(dir, "catalogue.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	set := store.Settings()
	if set.BrandName == "" || set.MessageTemplate == "" {
		t.Error("a document missing its name or message template should be filled in")
	}
	if set.WhatsApp != "2348155604988" {
		t.Error("the number that was there should be kept")
	}
	made, err := store.Create(Dress{Name: "First"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if made.Ref != "AJM-001" {
		t.Errorf("Ref = %q, want AJM-001 from a repaired counter", made.Ref)
	}
}

func TestOpenStoreRefusesRubbish(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "catalogue.json"), []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := OpenStore(dir)
	if err == nil {
		t.Fatal("OpenStore accepted a broken catalogue")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

// Writes from several requests at once must not lose a dress or repeat a
// reference. Worth running with -race.
func TestStoreIsSafeForConcurrentUse(t *testing.T) {
	store := newTestStore(t)
	const writers = 12

	var wg sync.WaitGroup
	refs := make(chan string, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			d, err := store.Create(Dress{Name: "Dress " + string(rune('A'+n))})
			if err != nil {
				t.Errorf("Create: %v", err)
				return
			}
			refs <- d.Ref
			store.Dresses()
			store.Categories()
			store.Settings()
		}(i)
	}
	wg.Wait()
	close(refs)

	seen := map[string]bool{}
	for ref := range refs {
		if seen[ref] {
			t.Errorf("reference %s was handed out twice", ref)
		}
		seen[ref] = true
	}
	if len(store.Dresses()) != writers {
		t.Errorf("got %d dresses, want %d", len(store.Dresses()), writers)
	}
}
