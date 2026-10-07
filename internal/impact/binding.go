package impact

import (
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"sync"
	"time"
)

// BindingSource records where a run-to-revision binding came from, so an audit
// can distinguish an explicit freeze-time binding from a later attribution.
type BindingSource string

const (
	BindingAtStart    BindingSource = "run_start"
	BindingManual     BindingSource = "manual"
	BindingReconciled BindingSource = "reconciled"
)

// RunBinding pins a run to the exact frozen revision it was executed against.
// model.Run itself never stores a revision number; without a binding a run
// cannot be attributed to either side of a comparison.
type RunBinding struct {
	RunID          model.ID      `json:"run_id"`
	RecipeID       model.ID      `json:"recipe_id"`
	RevisionNumber int           `json:"revision_number"`
	Source         BindingSource `json:"source"`
	BoundAt        time.Time     `json:"bound_at"`
}

// Binder is the authoritative run-to-frozen-revision ledger.
type Binder struct {
	mu       sync.RWMutex
	bindings map[model.ID]RunBinding
}

func NewBinder() *Binder { return &Binder{bindings: map[model.ID]RunBinding{}} }

// Bind records a binding. It refuses to overwrite an existing binding with a
// different revision: rebinding history would corrupt the audit trail. The
// caller must first prove the revision is frozen.
func (b *Binder) Bind(runID, recipeID model.ID, revision int, source BindingSource) (RunBinding, error) {
	if runID == "" || recipeID == "" {
		return RunBinding{}, fmt.Errorf("binding identity is required")
	}
	if revision < 1 {
		return RunBinding{}, fmt.Errorf("binding revision must be a frozen revision number")
	}
	if source == "" {
		source = BindingManual
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.bindings[runID]; ok && (existing.RecipeID != recipeID || existing.RevisionNumber != revision) {
		return RunBinding{}, fmt.Errorf("run %s is already bound to frozen revision %d; rebinding is forbidden", runID, existing.RevisionNumber)
	}
	binding := RunBinding{RunID: runID, RecipeID: recipeID, RevisionNumber: revision, Source: source, BoundAt: time.Now().UTC()}
	b.bindings[runID] = binding
	return binding, nil
}

func (b *Binder) Binding(runID model.ID) (RunBinding, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	value, ok := b.bindings[runID]
	return value, ok
}

func (b *Binder) ForRecipe(recipeID model.ID) []RunBinding {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]RunBinding, 0)
	for _, value := range b.bindings {
		if value.RecipeID == recipeID {
			out = append(out, value)
		}
	}
	return out
}
