package recipe

import (
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"sort"
	"sync"
	"time"
)

type Revision struct {
	ID        model.ID      `json:"id"`
	RecipeID  model.ID      `json:"recipe_id"`
	Name      string        `json:"name"`
	Number    int           `json:"number"`
	CreatedAt time.Time     `json:"created_at"`
	Layers    []model.Layer `json:"layers"`
}

type RevisionStore struct {
	mu     sync.RWMutex
	values map[model.ID][]Revision
}

func NewRevisionStore() *RevisionStore {
	return &RevisionStore{values: map[model.ID][]Revision{}}
}

func (s *RevisionStore) Freeze(recipe *model.Recipe) (Revision, error) {
	plan, err := Compile(recipe)
	if err != nil {
		return Revision{}, err
	}
	revision := Revision{ID: model.NewID(), RecipeID: plan.RecipeID, Name: recipe.Name, Number: plan.Revision, CreatedAt: time.Now().UTC(), Layers: append([]model.Layer(nil), plan.Layers...)}
	s.mu.Lock()
	existing := s.values[recipe.ID]
	for _, value := range existing {
		if value.Number == revision.Number {
			s.mu.Unlock()
			return Revision{}, fmt.Errorf("revision already frozen")
		}
	}
	s.values[recipe.ID] = append(existing, revision)
	s.mu.Unlock()
	return revision, nil
}

func (s *RevisionStore) List(recipeID model.ID) []Revision {
	s.mu.RLock()
	result := append([]Revision(nil), s.values[recipeID]...)
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].Number < result[j].Number })
	return result
}

// Get returns a frozen revision by recipe and revision number. It resolves
// only immutable revisions produced by Freeze; drafts are not present here and
// therefore can never masquerade as released versions in an impact report.
func (s *RevisionStore) Get(recipeID model.ID, number int) (Revision, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, revision := range s.values[recipeID] {
		if revision.Number == number {
			copied := revision
			copied.Layers = append([]model.Layer(nil), revision.Layers...)
			return copied, true
		}
	}
	return Revision{}, false
}
