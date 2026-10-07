package impact

import (
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
)

// LatestRevision asks for the highest frozen revision number.
const LatestRevision = 0

var (
	ErrRevisionNotFrozen = fmt.Errorf("revision is not frozen")
	ErrDraftNotPublished = fmt.Errorf("draft or mutable recipe state is not a frozen revision and cannot be compared")
)

// ResolveFrozen maps a revision number to an immutable frozen view through the
// revision store. This is the only sanctioned way to obtain a comparison
// input: drafts, the active recipe and any caller-supplied layer lists are
// rejected here by never being consulted. number == LatestRevision selects the
// newest frozen revision.
func ResolveFrozen(store *recipe.RevisionStore, recipeID model.ID, number int) (recipe.Revision, error) {
	if store == nil {
		return recipe.Revision{}, fmt.Errorf("frozen revision store is required")
	}
	if recipeID == "" {
		return recipe.Revision{}, fmt.Errorf("recipe id is required")
	}
	frozen := store.List(recipeID)
	if len(frozen) == 0 {
		return recipe.Revision{}, fmt.Errorf("%w: recipe %s has no frozen revisions", ErrDraftNotPublished, recipeID)
	}
	if number == LatestRevision {
		return frozen[len(frozen)-1], nil
	}
	for _, revision := range frozen {
		if revision.Number == number {
			return revision, nil
		}
	}
	return recipe.Revision{}, fmt.Errorf("%w: recipe %s has no frozen revision %d", ErrRevisionNotFrozen, recipeID, number)
}

// ResolvePair resolves and validates the two sides of a comparison. Both must
// be frozen revisions of the same recipe, in ascending order.
func ResolvePair(store *recipe.RevisionStore, recipeID model.ID, fromNumber, toNumber int) (recipe.Revision, recipe.Revision, error) {
	from, err := ResolveFrozen(store, recipeID, fromNumber)
	if err != nil {
		return recipe.Revision{}, recipe.Revision{}, err
	}
	to, err := ResolveFrozen(store, recipeID, toNumber)
	if err != nil {
		return recipe.Revision{}, recipe.Revision{}, err
	}
	if from.RecipeID != to.RecipeID {
		return recipe.Revision{}, recipe.Revision{}, fmt.Errorf("revisions belong to different recipes")
	}
	if from.Number >= to.Number {
		return recipe.Revision{}, recipe.Revision{}, fmt.Errorf("comparison requires an ascending pair: from=%d to=%d", from.Number, to.Number)
	}
	return from, to, nil
}

func asView(revision recipe.Revision) RevisionView {
	return RevisionView{RecipeID: revision.RecipeID, Number: revision.Number, Layers: revision.Layers}
}
