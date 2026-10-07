package recipe

import (
	"github.com/wyw14/cry-161/internal/model"
	"sync"
)

type DraftBook struct {
	mu     sync.RWMutex
	drafts map[model.ID]*model.Recipe
}

func NewDraftBook() *DraftBook { return &DraftBook{drafts: map[model.ID]*model.Recipe{}} }
func (b *DraftBook) Save(recipe *model.Recipe) *model.Recipe {
	b.mu.Lock()
	defer b.mu.Unlock()
	copy := cloneRecipe(recipe)
	copy.Published = false
	b.drafts[copy.ID] = copy
	return cloneRecipe(copy)
}
func (b *DraftBook) Get(id model.ID) (*model.Recipe, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	value, ok := b.drafts[id]
	if !ok {
		return nil, false
	}
	return cloneRecipe(value), true
}

// ListIDs returns the ids of recipes that currently hold mutable draft edits.
// Impact analysis cites these as excluded inputs, never as published changes.
func (b *DraftBook) ListIDs() []model.ID {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]model.ID, 0, len(b.drafts))
	for id := range b.drafts {
		out = append(out, id)
	}
	return out
}
func (b *DraftBook) UpdateLayer(id model.ID, layer model.Layer) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	value, ok := b.drafts[id]
	if !ok {
		return false
	}
	value.Layers[layer.Number] = layer
	value.Revision++
	return true
}
