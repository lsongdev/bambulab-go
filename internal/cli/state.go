package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/lsongdev/bambulab-go/bambulab"
)

func (a *app) state(ctx context.Context) error {
	printer, err := a.dialPrinter(ctx)
	if err != nil {
		return err
	}
	defer printer.Close()
	if err := printer.PushAll(ctx); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-printer.Done():
			select {
			case err := <-printer.Errors():
				return err
			default:
				return bambulab.ErrClosed
			}
		case err := <-printer.Errors():
			return err
		case report := <-printer.Reports():
			if _, ok := report.Payload["print"]; !ok {
				continue
			}
			status, err := printer.Status()
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.output(struct {
					bambulab.PrintStatus
					HeadXYZ any `json:"head_xyz"`
				}{PrintStatus: status})
			}
			return a.outputLiveState(status)
		}
	}
}

func (a *app) outputLiveState(status bambulab.PrintStatus) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Printer: %s\nRun state: %s\n", a.device, status.GCodeState)
	if status.SubtaskName != "" {
		fmt.Fprintf(&b, "Task: %s\n", status.SubtaskName)
	}
	fmt.Fprintf(&b, "Progress: %d%%\n", status.Percent)
	if status.TotalLayers > 0 {
		fmt.Fprintf(&b, "Layer: %d/%d\n", status.Layer, status.TotalLayers)
	}
	if status.RemainingMinutes > 0 {
		fmt.Fprintf(&b, "Remaining: %d min\n", status.RemainingMinutes)
	}
	fmt.Fprintf(&b, "Nozzle: %.1f/%.1f °C\nBed: %.1f/%.1f °C\n", status.NozzleTemperature, status.NozzleTargetTemperature, status.BedTemperature, status.BedTargetTemperature)
	fmt.Fprintf(&b, "Speed: level %d (%d%%)\n", status.SpeedLevel, status.SpeedMagnitude)
	if len(status.Lights) == 0 {
		b.WriteString("Lights: unavailable\n")
	} else {
		b.WriteString("Lights:\n")
		for _, light := range status.Lights {
			fmt.Fprintf(&b, "  %s: %s\n", light.Node, light.Mode)
		}
	}
	if raw, ok := status.Camera["ipcam_dev"]; ok {
		var available string
		if json.Unmarshal(raw, &available) == nil {
			fmt.Fprintf(&b, "Camera reported: %s\n", available)
		}
	}
	b.WriteString("Head XYZ: unavailable (not reported in printer status)\n")
	_, err := io.WriteString(a.out, b.String())
	return err
}
