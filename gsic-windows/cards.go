package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Skill card format is shared with the Android GSIC agent:
// a card is an id + trigger words + an ordered list of steps
// (the same step objects /task accepts).
type Card struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	TriggerWords []string          `json:"trigger_words,omitempty"`
	Steps        []json.RawMessage `json:"steps"`
	UpdatedAt    string            `json:"updated_at,omitempty"`
}

type cardFile struct {
	Cards []Card `json:"cards"`
}

// CardStore persists cards to skill_cards.json with a mutex.
type CardStore struct {
	path   string
	mu     sync.RWMutex
	cards  map[string]*Card
	order  []string // insertion order for listing
	broken bool     // true when the backing file could not be used
}

// NewCardStore loads cards from path. A missing file starts empty;
// a corrupt file returns an error so the caller can decide.
func NewCardStore(path string) (*CardStore, error) {
	s := &CardStore{path: path, cards: map[string]*Card{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return s, nil
	}
	// Accept both {"cards": [...]} and a bare [...] array.
	var cf cardFile
	if err := json.Unmarshal(data, &cf); err != nil {
		var arr []Card
		if err2 := json.Unmarshal(data, &arr); err2 != nil {
			return nil, fmt.Errorf("parse skill_cards.json: %w", err)
		}
		cf.Cards = arr
	}
	for i := range cf.Cards {
		c := cf.Cards[i]
		if c.ID == "" {
			continue
		}
		cp := c
		s.cards[c.ID] = &cp
		s.order = append(s.order, c.ID)
	}
	return s, nil
}

// NewCardStoreOrEmpty never fails: on error it logs and keeps an
// in-memory-only store so the server can still start.
func NewCardStoreOrEmpty(path string) *CardStore {
	s, err := NewCardStore(path)
	if err != nil {
		Logf("WARN: card store %s unusable (%v); using memory-only store", path, err)
		return &CardStore{path: path, cards: map[string]*Card{}, broken: true}
	}
	return s
}

func (s *CardStore) saveLocked() error {
	if s.broken {
		return nil
	}
	list := make([]Card, 0, len(s.order))
	for _, id := range s.order {
		if c, ok := s.cards[id]; ok {
			list = append(list, *c)
		}
	}
	data, err := json.MarshalIndent(cardFile{Cards: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns all cards in insertion order.
func (s *CardStore) List() []Card {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Card, 0, len(s.order))
	for _, id := range s.order {
		if c, ok := s.cards[id]; ok {
			out = append(out, *c)
		}
	}
	return out
}

// Get returns a copy of the card, or nil.
func (s *CardStore) Get(id string) *Card {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c, ok := s.cards[id]; ok {
		cp := *c
		return &cp
	}
	return nil
}

// Put inserts or replaces a card (id required, steps required).
func (s *CardStore) Put(c Card) error {
	if c.ID == "" {
		return fmt.Errorf("card id is required")
	}
	if len(c.Steps) == 0 {
		return fmt.Errorf("card %q has no steps", c.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.UpdatedAt = time.Now().Format(time.RFC3339)
	if _, exists := s.cards[c.ID]; !exists {
		s.order = append(s.order, c.ID)
	}
	cp := c
	s.cards[c.ID] = &cp
	return s.saveLocked()
}

// Delete removes a card; returns false when it did not exist.
func (s *CardStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cards[id]; !ok {
		return false
	}
	delete(s.cards, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	_ = s.saveLocked()
	return true
}
