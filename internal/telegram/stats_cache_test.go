package telegram

import "testing"

func TestStatsCardCacheFIFO(t *testing.T) {
	c := newStatsCardCache(2)
	c.Put("a", "file_a")
	c.Put("b", "file_b")
	c.Put("c", "file_c") // evicts "a"

	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("oldest entry was not evicted")
	}
	if id, ok := c.Get("b"); !ok || id != "file_b" {
		t.Fatalf("b = %q, %t", id, ok)
	}
	if id, ok := c.Get("c"); !ok || id != "file_c" {
		t.Fatalf("c = %q, %t", id, ok)
	}
}

func TestStatsCardCacheUpdateAndDelete(t *testing.T) {
	c := newStatsCardCache(4)
	c.Put("a", "old")
	c.Put("a", "new")
	if id, _ := c.Get("a"); id != "new" {
		t.Fatalf("id = %q, want new", id)
	}
	if c.Len() != 1 {
		t.Fatalf("len = %d, want 1", c.Len())
	}
	c.Delete("a")
	if _, ok := c.Get("a"); ok {
		t.Fatal("entry not deleted")
	}
	if c.Len() != 0 {
		t.Fatalf("len = %d, want 0", c.Len())
	}
	c.Put("", "file")
	c.Put("token", "")
	if c.Len() != 0 {
		t.Fatalf("empty token/file should be ignored, len = %d", c.Len())
	}
}
