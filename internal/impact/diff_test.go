package impact

import (
	"github.com/wyw14/cry-161/internal/model"
	"testing"
)

func layer(number int, material string, target, rate float64) model.Layer {
	return model.Layer{Number: number, Material: material, TargetNanometers: target, RateNanometers: rate}
}

func containsDimension(diff Diff, want Dimension) bool {
	for _, got := range diff.Dimensions {
		if got == want {
			return true
		}
	}
	return false
}

func TestComputeDiffIdenticalRevisions(t *testing.T) {
	layers := []model.Layer{layer(1, "SiO2", 100, 2), layer(2, "TiO2", 60, 3)}
	diff := ComputeDiff(RevisionView{RecipeID: "r", Number: 1, Layers: layers}, RevisionView{RecipeID: "r", Number: 2, Layers: layers})
	if len(diff.Changes) != 0 || len(diff.CommandChanges) != 0 || len(diff.Dimensions) != 0 {
		t.Fatalf("expected empty diff, got %+v", diff)
	}
}

func TestComputeDiffTargetThickness(t *testing.T) {
	from := RevisionView{RecipeID: "r", Number: 1, Layers: []model.Layer{layer(1, "SiO2", 100, 2)}}
	to := RevisionView{RecipeID: "r", Number: 2, Layers: []model.Layer{layer(1, "SiO2", 120, 2)}}
	diff := ComputeDiff(from, to)
	if !containsDimension(diff, DimensionTarget) {
		t.Fatalf("expected target_thickness dimension, got %v", diff.Dimensions)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].Type != ChangeChanged {
		t.Fatalf("expected one changed layer, got %+v", diff.Changes)
	}
	field := diff.Changes[0].Fields[0]
	if field.Field != "target_nm" || field.Before != 100 || field.After != 120 {
		t.Fatalf("unexpected field change %+v", field)
	}
}

func TestComputeDiffMaterialAndRate(t *testing.T) {
	from := RevisionView{RecipeID: "r", Number: 1, Layers: []model.Layer{layer(1, "SiO2", 100, 0.5)}}
	to := RevisionView{RecipeID: "r", Number: 2, Layers: []model.Layer{layer(1, "Ta2O5", 100, 2)}}
	diff := ComputeDiff(from, to)
	if !containsDimension(diff, DimensionMaterial) || !containsDimension(diff, DimensionRate) {
		t.Fatalf("expected material+rate dimensions, got %v", diff.Dimensions)
	}
	// Rate crossing 1 changes the rotation command: 12 rpm -> 18 rpm.
	var rotate *CommandChange
	for i := range diff.CommandChanges {
		if diff.CommandChanges[i].Kind == "rotate" {
			rotate = &diff.CommandChanges[i]
		}
	}
	if rotate == nil {
		t.Fatalf("expected rotate command change, got %+v", diff.CommandChanges)
	}
	if rotate.Before != "rpm=12 duration_s=11" || rotate.After != "rpm=18 duration_s=11" {
		t.Fatalf("unexpected rotate detail %q -> %q", rotate.Before, rotate.After)
	}
	if !containsDimension(diff, DimensionCommand) {
		t.Fatalf("expected device_command dimension")
	}
}

func TestComputeDiffLayerAddRemoveAndReorder(t *testing.T) {
	// Gap numbering: layers 1 and 3 occupy positions 1 and 2.
	from := RevisionView{RecipeID: "r", Number: 1, Layers: []model.Layer{layer(1, "SiO2", 100, 2), layer(3, "TiO2", 60, 3)}}
	// Filling the gap with layer 2 keeps both identities; layer 3 moves from
	// position 2 to position 3 and layer 2 is added.
	to := RevisionView{RecipeID: "r", Number: 2, Layers: []model.Layer{
		layer(1, "SiO2", 100, 2),
		layer(2, "Al2O3", 40, 1),
		layer(3, "TiO2", 60, 3),
	}}
	diff := ComputeDiff(from, to)
	if !containsDimension(diff, DimensionOrder) {
		t.Fatalf("expected layer_order dimension, got %v", diff.Dimensions)
	}
	byLayer := map[int]LayerChange{}
	for _, change := range diff.Changes {
		byLayer[change.Layer] = change
	}
	if byLayer[2].Type != ChangeAdded {
		t.Fatalf("expected layer 2 added, got %+v", byLayer[2])
	}
	if byLayer[3].Type != ChangeMoved || byLayer[3].BeforeLayer != 2 || byLayer[3].AfterLayer != 3 {
		t.Fatalf("expected layer 3 moved 2->3, got %+v", byLayer[3])
	}
	// New layer contributes 4 new commands (condition/rotate/open/close).
	addedCommands := 0
	for _, command := range diff.CommandChanges {
		if command.Layer == 2 && command.Type == "added" {
			addedCommands++
		}
	}
	if addedCommands != 4 {
		t.Fatalf("expected 4 added commands for new layer, got %d (%+v)", addedCommands, diff.CommandChanges)
	}

	// Removing a layer produces a removed change.
	back := ComputeDiff(to, from)
	if back.ToNumber != 1 || back.FromNumber != 2 {
		t.Fatalf("unexpected pair %d->%d", back.FromNumber, back.ToNumber)
	}
	foundRemoved := false
	for _, change := range back.Changes {
		if change.Type == ChangeRemoved && change.Layer == 2 {
			foundRemoved = true
		}
	}
	if !foundRemoved {
		t.Fatalf("expected layer 2 removed, got %+v", back.Changes)
	}
}

func TestProjectCommandsOrder(t *testing.T) {
	commands := ProjectCommands([]model.Layer{layer(1, "SiO2", 100, 2)})
	want := []string{"pump", "condition", "rotate", "open_shutter", "close_shutter", "vent"}
	if len(commands) != len(want) {
		t.Fatalf("expected %d commands, got %d", len(want), len(commands))
	}
	for i, command := range commands {
		if command.Kind != want[i] {
			t.Fatalf("command %d: want %s got %s", i, want[i], command.Kind)
		}
		if command.Sequence != i+1 {
			t.Fatalf("command %d sequence = %d", i, command.Sequence)
		}
	}
}
