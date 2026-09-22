package bambulab

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (p *Printer) command(ctx context.Context, section, command string, fields any, qos byte) error {
	_, err := p.Send(ctx, section, command, fields, qos)
	return err
}
func (p *Printer) Pause(ctx context.Context) error {
	return p.command(ctx, "print", "pause", map[string]string{"param": ""}, 1)
}
func (p *Printer) Resume(ctx context.Context) error {
	return p.command(ctx, "print", "resume", map[string]string{"param": ""}, 1)
}
func (p *Printer) Stop(ctx context.Context) error {
	return p.command(ctx, "print", "stop", map[string]string{"param": ""}, 1)
}
func (p *Printer) GetVersion(ctx context.Context) error {
	return p.command(ctx, "info", "get_version", nil, 0)
}

// PushAll requests a full status. Avoid calling more often than every five minutes on P1P.
func (p *Printer) PushAll(ctx context.Context) error {
	return p.command(ctx, "pushing", "pushall", map[string]int{"version": 1, "push_target": 1}, 0)
}
func (p *Printer) GetAccessCode(ctx context.Context) error {
	return p.command(ctx, "system", "get_access_code", nil, 0)
}
func (p *Printer) Calibrate(ctx context.Context) error {
	return p.command(ctx, "print", "calibration", nil, 0)
}
func (p *Printer) UnloadFilament(ctx context.Context) error {
	return p.command(ctx, "print", "unload_filament", nil, 0)
}

type PrintSpeed int

const (
	SpeedSilent    PrintSpeed = 1
	SpeedStandard  PrintSpeed = 2
	SpeedSport     PrintSpeed = 3
	SpeedLudicrous PrintSpeed = 4
)

func (p *Printer) SetPrintSpeed(ctx context.Context, speed PrintSpeed) error {
	if speed < SpeedSilent || speed > SpeedLudicrous {
		return fmt.Errorf("bambulab: print speed must be 1 through 4")
	}
	return p.command(ctx, "print", "print_speed", map[string]string{"param": strconv.Itoa(int(speed))}, 0)
}
func (p *Printer) SendGCode(ctx context.Context, code string) error {
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("bambulab: G-code is required")
	}
	return p.command(ctx, "print", "gcode_line", map[string]string{"param": code}, 0)
}
func (p *Printer) PrintGCodeFile(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("bambulab: G-code path is required")
	}
	return p.command(ctx, "print", "gcode_file", map[string]string{"param": path}, 0)
}

type ProjectFile struct {
	Param                string `json:"param"`
	ProjectID            string `json:"project_id"`
	ProfileID            string `json:"profile_id"`
	TaskID               string `json:"task_id"`
	SubtaskID            string `json:"subtask_id"`
	SubtaskName          string `json:"subtask_name"`
	File                 string `json:"file"`
	URL                  string `json:"url"`
	MD5                  string `json:"md5"`
	Timelapse            bool   `json:"timelapse"`
	BedType              string `json:"bed_type"`
	BedLevelling         bool   `json:"bed_levelling"`
	FlowCalibration      bool   `json:"flow_cali"`
	VibrationCalibration bool   `json:"vibration_cali"`
	LayerInspect         bool   `json:"layer_inspect"`
	AMSMapping           []int  `json:"ams_mapping"`
	UseAMS               bool   `json:"use_ams"`
}

// LocalProjectFile builds local-print metadata; calibration choices remain explicit.
func LocalProjectFile(fileURL string, plate int) ProjectFile {
	return ProjectFile{URL: fileURL, Param: fmt.Sprintf("Metadata/plate_%d.gcode", plate), ProjectID: "0", ProfileID: "0", TaskID: "0", SubtaskID: "0", BedType: "auto", AMSMapping: []int{}}
}
func (p *Printer) PrintProject(ctx context.Context, file ProjectFile) error {
	if file.Param == "" || (file.URL == "" && file.File == "") {
		return fmt.Errorf("bambulab: project plate path and URL or file are required")
	}
	if file.AMSMapping == nil {
		file.AMSMapping = []int{}
	}
	return p.command(ctx, "print", "project_file", file, 0)
}
func (p *Printer) SkipObjects(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return fmt.Errorf("bambulab: object IDs are required")
	}
	for _, id := range ids {
		if id < 0 {
			return fmt.Errorf("bambulab: object IDs must be nonnegative")
		}
	}
	return p.command(ctx, "print", "skip_objects", map[string]any{"obj_list": ids}, 0)
}

type AMSUserSetting struct {
	AMSID       int  `json:"ams_id"`
	StartupRead bool `json:"startup_read_option"`
	TrayRead    bool `json:"tray_read_option"`
}
type AMSFilamentSetting struct {
	AMSID         int    `json:"ams_id"`
	TrayID        int    `json:"tray_id"`
	TrayInfoIndex string `json:"tray_info_idx"`
	Color         string `json:"tray_color"`
	NozzleTempMin int    `json:"nozzle_temp_min"`
	NozzleTempMax int    `json:"nozzle_temp_max"`
	Type          string `json:"tray_type"`
}

func (p *Printer) ChangeFilament(ctx context.Context, target, currentTemp, targetTemp int) error {
	if target < 0 || currentTemp < 0 || targetTemp < 0 {
		return fmt.Errorf("bambulab: tray and temperatures must be nonnegative")
	}
	return p.command(ctx, "print", "ams_change_filament", map[string]int{"target": target, "curr_temp": currentTemp, "tar_temp": targetTemp}, 0)
}
func (p *Printer) SetAMSUserSetting(ctx context.Context, setting AMSUserSetting) error {
	if setting.AMSID < 0 {
		return fmt.Errorf("bambulab: AMS ID must be nonnegative")
	}
	return p.command(ctx, "print", "ams_user_setting", setting, 0)
}
func (p *Printer) SetAMSFilament(ctx context.Context, setting AMSFilamentSetting) error {
	if setting.AMSID < 0 || setting.TrayID < 0 || setting.NozzleTempMin < 0 || setting.NozzleTempMax < setting.NozzleTempMin {
		return fmt.Errorf("bambulab: invalid filament settings")
	}
	if len(setting.Color) != 8 {
		return fmt.Errorf("bambulab: filament color must be RRGGBBAA")
	}
	if _, err := strconv.ParseUint(setting.Color, 16, 32); err != nil {
		return fmt.Errorf("bambulab: invalid filament color")
	}
	return p.command(ctx, "print", "ams_filament_setting", setting, 0)
}
func (p *Printer) ControlAMS(ctx context.Context, action string) error {
	if action != "resume" && action != "reset" && action != "pause" {
		return fmt.Errorf("bambulab: AMS action must be resume, reset or pause")
	}
	return p.command(ctx, "print", "ams_control", map[string]string{"param": action}, 0)
}

type LEDControl struct {
	Node         string `json:"led_node"`
	Mode         string `json:"led_mode"`
	OnTime       int    `json:"led_on_time"`
	OffTime      int    `json:"led_off_time"`
	LoopTimes    int    `json:"loop_times"`
	IntervalTime int    `json:"interval_time"`
}

func (p *Printer) SetLED(ctx context.Context, setting LEDControl) error {
	if setting.Node != "chamber_light" && setting.Node != "work_light" {
		return fmt.Errorf("bambulab: unknown LED node")
	}
	if setting.Mode != "on" && setting.Mode != "off" && setting.Mode != "flashing" {
		return fmt.Errorf("bambulab: LED mode must be on, off or flashing")
	}
	if setting.OnTime < 0 || setting.OffTime < 0 || setting.LoopTimes < 0 || setting.IntervalTime < 0 {
		return fmt.Errorf("bambulab: LED timing must be nonnegative")
	}
	return p.command(ctx, "system", "ledctrl", setting, 0)
}
func (p *Printer) SetLight(ctx context.Context, on bool) error {
	mode := "off"
	if on {
		mode = "on"
	}
	return p.SetLED(ctx, LEDControl{Node: "chamber_light", Mode: mode, OnTime: 500, OffTime: 500, LoopTimes: 1, IntervalTime: 1000})
}
func (p *Printer) SetRecording(ctx context.Context, enabled bool) error {
	return p.cameraControl(ctx, "ipcam_record_set", enabled)
}
func (p *Printer) SetTimelapse(ctx context.Context, enabled bool) error {
	return p.cameraControl(ctx, "ipcam_timelapse", enabled)
}
func (p *Printer) cameraControl(ctx context.Context, command string, enabled bool) error {
	control := "disable"
	if enabled {
		control = "enable"
	}
	return p.command(ctx, "camera", command, map[string]string{"control": control}, 0)
}
func (p *Printer) SetXCam(ctx context.Context, module string, enabled, halt bool) error {
	if module != "first_layer_inspector" && module != "spaghetti_detector" {
		return fmt.Errorf("bambulab: unsupported XCam module")
	}
	return p.command(ctx, "xcam", "xcam_control_set", map[string]any{"module_name": module, "control": enabled, "print_halt": halt}, 0)
}
func (p *Printer) ConfirmUpgrade(ctx context.Context) error {
	return p.command(ctx, "upgrade", "upgrade_confirm", map[string]int{"src_id": 1}, 0)
}
func (p *Printer) ConfirmConsistency(ctx context.Context) error {
	return p.command(ctx, "upgrade", "consistency_confirm", map[string]int{"src_id": 1}, 0)
}
func (p *Printer) GetUpgradeHistory(ctx context.Context) error {
	return p.command(ctx, "upgrade", "get_history", nil, 0)
}
func (p *Printer) StartUpgrade(ctx context.Context, module, version, url string) error {
	if (module != "ota" && module != "ams") || version == "" || url == "" {
		return fmt.Errorf("bambulab: module (ota/ams), version and URL are required")
	}
	return p.command(ctx, "upgrade", "start", map[string]any{"src_id": 1, "module": module, "version": version, "url": url}, 0)
}
