package impact

import (
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"sort"
)

// Dimension is one audited comparison axis. Every dimension is backed by data
// that actually exists on a frozen revision; nothing here reads draft state.
type Dimension string

const (
	DimensionOrder    Dimension = "layer_order"
	DimensionMaterial Dimension = "material"
	DimensionTarget   Dimension = "target_thickness"
	DimensionRate     Dimension = "rate"
	DimensionCommand  Dimension = "device_command"
)

// ChangeType classifies how a layer participates in the diff.
type ChangeType string

const (
	ChangeAdded   ChangeType = "added"
	ChangeRemoved ChangeType = "removed"
	ChangeMoved   ChangeType = "moved"
	ChangeChanged ChangeType = "changed"
)

// FieldChange is one before/after scalar on a retained layer.
type FieldChange struct {
	Field    string  `json:"field"`
	Before   float64 `json:"before,omitempty"`
	After    float64 `json:"after,omitempty"`
	OldValue string  `json:"old_value,omitempty"`
	NewValue string  `json:"new_value,omitempty"`
}

// LayerChange describes what happened to one layer slot between the revisions.
type LayerChange struct {
	Type         ChangeType      `json:"type"`
	Layer        int             `json:"layer"`
	BeforeLayer  int             `json:"before_layer,omitempty"` // position (1-based) before; set on moved/changed
	AfterLayer   int             `json:"after_layer,omitempty"`  // position (1-based) after
	Material     string          `json:"material,omitempty"`
	Fields       []FieldChange   `json:"fields,omitempty"`
	CommandDelta []CommandChange `json:"command_delta,omitempty"`
}

// CommandChange records a device-command-stream divergence between revisions.
type CommandChange struct {
	Kind   string `json:"kind"`
	Layer  int    `json:"layer"`
	Type   string `json:"type"` // added, removed, detail_changed
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// Diff is the structural comparison of two frozen revisions.
type Diff struct {
	RecipeID         model.ID        `json:"recipe_id"`
	FromNumber       int             `json:"from_revision"`
	ToNumber         int             `json:"to_revision"`
	LayerCountBefore int             `json:"layer_count_before"`
	LayerCountAfter  int             `json:"layer_count_after"`
	Changes          []LayerChange   `json:"layer_changes"`
	CommandChanges   []CommandChange `json:"command_changes"`
	Dimensions       []Dimension     `json:"dimensions"`
}

// RevisionView is the frozen input surface the diff engine accepts. It is
// satisfied by recipe.Revision but decouples this package from storage.
type RevisionView struct {
	RecipeID model.ID
	Number   int
	Layers   []model.Layer
}

// sortedLayers copies layers into Number order (the order Compile freezes).
func sortedLayers(layers []model.Layer) []model.Layer {
	out := append([]model.Layer(nil), layers...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// ComputeDiff compares two fully resolved frozen revisions. Callers must only
// pass revisions returned by the frozen revision store; this function never
// reads mutable recipe or draft state, so it cannot publish a draft delta.
func ComputeDiff(from, to RevisionView) Diff {
	before := sortedLayers(from.Layers)
	after := sortedLayers(to.Layers)
	diff := Diff{
		RecipeID:         to.RecipeID,
		FromNumber:       from.Number,
		ToNumber:         to.Number,
		LayerCountBefore: len(before),
		LayerCountAfter:  len(after),
		Changes:          []LayerChange{},
		CommandChanges:   []CommandChange{},
		Dimensions:       []Dimension{},
	}
	touched := map[Dimension]bool{}
	mark := func(d Dimension) {
		if !touched[d] {
			touched[d] = true
			diff.Dimensions = append(diff.Dimensions, d)
		}
	}

	oldCommands := ProjectCommands(before)
	newCommands := ProjectCommands(after)
	oldByKey := map[string]DeviceCommand{}
	for _, command := range oldCommands {
		oldByKey[commandKey(command)] = command
	}
	newByKey := map[string]DeviceCommand{}
	for _, command := range newCommands {
		newByKey[commandKey(command)] = command
	}
	for _, command := range newCommands {
		previous, ok := oldByKey[commandKey(command)]
		if !ok {
			diff.CommandChanges = append(diff.CommandChanges, CommandChange{Kind: command.Kind, Layer: command.Layer, Type: "added", After: command.Detail})
			mark(DimensionCommand)
		} else if previous.Detail != command.Detail {
			diff.CommandChanges = append(diff.CommandChanges, CommandChange{Kind: command.Kind, Layer: command.Layer, Type: "detail_changed", Before: previous.Detail, After: command.Detail})
			mark(DimensionCommand)
		}
	}
	for _, command := range oldCommands {
		if _, ok := newByKey[commandKey(command)]; !ok {
			diff.CommandChanges = append(diff.CommandChanges, CommandChange{Kind: command.Kind, Layer: command.Layer, Type: "removed", Before: command.Detail})
			mark(DimensionCommand)
		}
	}
	changesForLayer := func(layer int) []CommandChange {
		out := []CommandChange{}
		for _, change := range diff.CommandChanges {
			if change.Layer == layer {
				out = append(out, change)
			}
		}
		return out
	}

	// Layer.Number is the stable slot identity (map key, scheduler unit and
	// evidence index), so alignment is by number. Position displacement is
	// detected separately from the sorted index, which catches reorders and
	// insertions that shift the stack.
	beforeByNumber := map[int]model.Layer{}
	for index, layer := range before {
		beforeByNumber[layer.Number] = layer
		_ = index
	}
	afterByNumber := map[int]model.Layer{}
	for _, layer := range after {
		afterByNumber[layer.Number] = layer
	}
	positionBefore := map[int]int{}
	for index, layer := range before {
		positionBefore[layer.Number] = index + 1
	}
	positionAfter := map[int]int{}
	for index, layer := range after {
		positionAfter[layer.Number] = index + 1
	}

	emit := func(change LayerChange) { diff.Changes = append(diff.Changes, change) }
	for _, layer := range after {
		old, ok := beforeByNumber[layer.Number]
		if !ok {
			emit(LayerChange{Type: ChangeAdded, Layer: layer.Number, AfterLayer: positionAfter[layer.Number], Material: layer.Material, CommandDelta: changesForLayer(layer.Number)})
			mark(DimensionOrder)
			continue
		}
		change := LayerChange{Type: ChangeChanged, Layer: layer.Number, BeforeLayer: positionBefore[layer.Number], AfterLayer: positionAfter[layer.Number], Material: layer.Material, CommandDelta: changesForLayer(layer.Number)}
		if change.BeforeLayer != change.AfterLayer {
			change.Type = ChangeMoved
			mark(DimensionOrder)
		}
		if old.Material != layer.Material {
			change.Fields = append(change.Fields, FieldChange{Field: "material", OldValue: old.Material, NewValue: layer.Material})
			mark(DimensionMaterial)
		}
		if old.TargetNanometers != layer.TargetNanometers {
			change.Fields = append(change.Fields, FieldChange{Field: "target_nm", Before: old.TargetNanometers, After: layer.TargetNanometers})
			mark(DimensionTarget)
		}
		if old.RateNanometers != layer.RateNanometers {
			change.Fields = append(change.Fields, FieldChange{Field: "rate_nm_s", Before: old.RateNanometers, After: layer.RateNanometers})
			mark(DimensionRate)
		}
		if len(change.Fields) > 0 || change.Type == ChangeMoved || len(change.CommandDelta) > 0 {
			emit(change)
		}
	}
	for _, layer := range before {
		if _, ok := afterByNumber[layer.Number]; !ok {
			emit(LayerChange{Type: ChangeRemoved, Layer: layer.Number, BeforeLayer: positionBefore[layer.Number], Material: layer.Material, CommandDelta: changesForLayer(layer.Number)})
			mark(DimensionOrder)
		}
	}
	sort.SliceStable(diff.Changes, func(i, j int) bool {
		if diff.Changes[i].Layer != diff.Changes[j].Layer {
			return diff.Changes[i].Layer < diff.Changes[j].Layer
		}
		return diff.Changes[i].Type < diff.Changes[j].Type
	})
	// Dimensions emit in a fixed, auditable axis order regardless of map scan.
	ordered := []Dimension{DimensionOrder, DimensionMaterial, DimensionTarget, DimensionRate, DimensionCommand}
	diff.Dimensions = diff.Dimensions[:0]
	for _, d := range ordered {
		if touched[d] {
			diff.Dimensions = append(diff.Dimensions, d)
		}
	}
	return diff
}

// String gives a stable human-readable summary used by the text audit report.
func (d Diff) String() string {
	if len(d.Changes) == 0 && len(d.CommandChanges) == 0 {
		return fmt.Sprintf("revision %d -> %d: no changes", d.FromNumber, d.ToNumber)
	}
	return fmt.Sprintf("revision %d -> %d: %d layer change(s), %d command change(s)", d.FromNumber, d.ToNumber, len(d.Changes), len(d.CommandChanges))
}
