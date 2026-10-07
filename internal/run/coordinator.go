package run

import (
	"context"
	"fmt"
	"github.com/wyw14/cry-161/internal/chamber"
	"github.com/wyw14/cry-161/internal/journal"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
	"github.com/wyw14/cry-161/internal/substrate"
	"github.com/wyw14/cry-161/internal/vacuum"
	"time"
)

type Coordinator struct {
	Registry  *Registry
	Recipes   *recipe.Store
	Revisions *recipe.RevisionStore
	Carrier   *substrate.Carrier
	Chamber   *chamber.Chamber
	Vacuum    *vacuum.Controller
	Journal   *journal.Store
}

// latestFrozenRevision resolves the released version a new run must execute:
// the highest-numbered immutable revision frozen for the recipe. A run may
// only start against a real frozen version, never a draft.
func latestFrozenRevision(store *recipe.RevisionStore, recipeID model.ID) (recipe.Revision, error) {
	if store == nil {
		return recipe.Revision{}, fmt.Errorf("frozen revision store is not configured")
	}
	frozen := store.List(recipeID)
	if len(frozen) == 0 {
		return recipe.Revision{}, fmt.Errorf("recipe has no frozen released revision")
	}
	return frozen[len(frozen)-1], nil
}

func (c *Coordinator) Start(ctx context.Context, batch string, recipeID model.ID) (*model.Run, error) {
	if _, err := c.Recipes.Active(recipeID); err != nil {
		return nil, err
	}
	revision, err := latestFrozenRevision(c.Revisions, recipeID)
	if err != nil {
		return nil, err
	}
	run := &model.Run{ID: model.NewID(), Batch: batch, RecipeID: recipeID, RecipeRevision: revision.Number, State: model.Loading, CreatedAt: timeNow(), UpdatedAt: timeNow()}
	if err := c.Carrier.Load(batch, run.ID); err != nil {
		return nil, err
	}
	c.Registry.Add(run)
	if !c.Registry.Transition(run.ID, model.Pumping) {
		return nil, fmt.Errorf("state transition failed")
	}
	if err := c.Vacuum.Start(ctx); err != nil {
		c.Registry.UpdateError(run.ID, err)
		return nil, err
	}
	c.Chamber.SetVacuum(c.Vacuum.Pressure(), c.Vacuum.Stable())
	c.Chamber.SetInterlock("vacuum", true)
	_ = c.Journal.Append(journal.RunStarted(*run))
	value, _ := c.Registry.Get(run.ID)
	return value, nil
}
func timeNow() time.Time { return time.Now().UTC() }
