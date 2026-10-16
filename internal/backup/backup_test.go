package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLayoutAndProtection(t *testing.T) {
	store := t.TempDir()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	id, partial, err := Begin(store, "vm", "web1", now)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(partial, "disk-vda.qcow2"), make([]byte, 1000), 0o600)
	if l, _ := List(store); len(l) != 0 {
		t.Fatal("partial backup listed")
	}
	if err := Commit(store, partial, &Manifest{ID: id, Kind: "vm", Name: "web1", Created: now}); err != nil {
		t.Fatal(err)
	}
	l, _ := List(store)
	if len(l) != 1 || l[0].Size < 1000 {
		t.Fatalf("list: %+v", l)
	}
	if err := SetProtected(store, id, true); err != nil {
		t.Fatal(err)
	}
	if err := Delete(store, id); err != ErrProtected {
		t.Fatalf("protected backup deleted: %v", err)
	}
	SetProtected(store, id, false)
	if err := Delete(store, id); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"vm/../../etc/2026-01-01T00-00-00Z", "xx/a/2026-01-01T00-00-00Z", "vm/a/b", "vm/a/2026-13-01T00-00-00Z"} {
		if _, err := Dir(store, bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	// Leftovers of an interrupted backup are cleaned.
	_, p2, _ := Begin(store, "ct", "c1", now)
	if r := CleanPartial(store); len(r) != 1 || r[0] != p2 {
		t.Fatalf("clean: %v", r)
	}
}

func TestPrune(t *testing.T) {
	base := time.Date(2026, 10, 16, 23, 0, 0, 0, time.UTC)
	var list []Manifest
	// Two backups per day for 40 days, newest first.
	for d := 0; d < 40; d++ {
		for h := 0; h < 2; h++ {
			ts := base.Add(-time.Duration(d)*24*time.Hour - time.Duration(h)*6*time.Hour)
			list = append(list, Manifest{ID: NewID("vm", "a", ts), Created: ts})
		}
	}
	list[79].Protected = true // the oldest
	keep, remove := Prune(list, Keep{Last: 3, Daily: 7, Weekly: 4}, time.UTC)
	if len(keep)+len(remove) != len(list) {
		t.Fatal("lost backups")
	}
	// last 3 = day0 ×2 + day1 newest; daily 7 = days 1..7 not yet covered → day1 already
	// covered, so days 2..8; weekly 4 = 4 older ISO weeks not covered; + protected.
	if len(keep) != 3+7+4+1 {
		t.Fatalf("kept %d", len(keep))
	}
	for _, k := range keep {
		if k.ID == list[79].ID {
			return
		}
	}
	t.Fatal("protected backup removed")
}

func TestPruneEmptyKeepsAll(t *testing.T) {
	l := []Manifest{{ID: "1"}, {ID: "2"}}
	if k, r := Prune(l, Keep{}, time.UTC); len(k) != 2 || len(r) != 0 {
		t.Fatal("empty policy removed backups")
	}
}
