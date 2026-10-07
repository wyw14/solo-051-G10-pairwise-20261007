package impact

import (
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/rotation"
)

// DeviceCommand is one entry of the command stream that a frozen recipe
// deterministically projects onto the hardware. Projection mirrors the real
// production path: run.Coordinator drives pump/vent, service.RunLayer issues
// condition per layer, rotation.ProfileFor derives the rotation command and
// source.ExecuteLayer guarantees an open/close shutter pair per layer.
type DeviceCommand struct {
	Kind     string `json:"kind"`
	Layer    int    `json:"layer"` // 0 = recipe-level command (pump/vent)
	Sequence int    `json:"sequence"`
	Detail   string `json:"detail"`
}

func commandKey(c DeviceCommand) string { return fmt.Sprintf("%s@%d", c.Kind, c.Layer) }

// ProjectCommands expands frozen layers into the device command stream in
// execution order. It never reads mutable recipe or draft state.
func ProjectCommands(layers []model.Layer) []DeviceCommand {
	sequence := 0
	emit := func(kind string, layer int, detail string) DeviceCommand {
		sequence++
		return DeviceCommand{Kind: kind, Layer: layer, Sequence: sequence, Detail: detail}
	}
	commands := []DeviceCommand{emit(string(model.CommandPump), 0, "vacuum-production-profile")}
	for _, layer := range layers {
		profile := rotation.ProfileFor(layer)
		commands = append(commands,
			emit(string(model.CommandCondition), layer.Number, fmt.Sprintf("material=%s", layer.Material)),
			emit(string(model.CommandRotate), layer.Number, fmt.Sprintf("rpm=%.0f duration_s=%d", profile.RPM, profile.DurationSeconds)),
			emit(string(model.CommandOpenShutter), layer.Number, "deposition"),
			emit(string(model.CommandCloseShutter), layer.Number, "deposition-end"),
		)
	}
	commands = append(commands, emit(string(model.CommandVent), 0, "process-complete"))
	return commands
}
