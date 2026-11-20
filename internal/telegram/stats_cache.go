package telegram

import "sync"

// statsCacheLimit bounds the RAM used by the token -> file_id map. Entries are
// tiny strings, so a few hundred cover the active players of a busy bot.
const statsCacheLimit = 512

// statsCardCache remembers the Telegram file_id of an already uploaded card.
// Reusing a file_id avoids rendering and re-uploading the same PNG on every
// /stats. It is process-local: a restart simply re-uploads once.
type statsCardCache struct {
	mu      sync.Mutex
	entries map[string]string
	order   []string
	limit   int
}

func newStatsCardCache(limit int) *statsCardCache {
	if limit <= 0 {
		limit = statsCacheLimit
	}
	return &statsCardCache{entries: make(map[string]string), limit: limit}
}

func (c *statsCardCache) Get(token string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.entries[token]
	return id, ok
}

func (c *statsCardCache) Put(token, fileID string) {
	if token == "" || fileID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[token]; exists {
		c.entries[token] = fileID
		return
	}
	if len(c.order) >= c.limit {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	c.order = append(c.order, token)
	c.entries[token] = fileID
}

func (c *statsCardCache) Delete(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[token]; !exists {
		return
	}
	delete(c.entries, token)
	for i, t := range c.order {
		if t == token {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

func (c *statsCardCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
