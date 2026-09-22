package bambulab

import "encoding/json"

// PrintStatus exposes commonly used fields. Snapshot retains every raw field.
// A zero value can mean either zero or not yet reported; inspect Snapshot to
// distinguish those cases before acting on printer state.
type PrintStatus struct {
	Command                 string        `json:"command"`
	GCodeState              string        `json:"gcode_state"`
	GCodeFile               string        `json:"gcode_file"`
	Percent                 int           `json:"mc_percent"`
	RemainingMinutes        int           `json:"mc_remaining_time"`
	Layer                   int           `json:"layer_num"`
	TotalLayers             int           `json:"total_layer_num"`
	NozzleTemperature       float64       `json:"nozzle_temper"`
	NozzleTargetTemperature float64       `json:"nozzle_target_temper"`
	BedTemperature          float64       `json:"bed_temper"`
	BedTargetTemperature    float64       `json:"bed_target_temper"`
	ChamberTemperature      float64       `json:"chamber_temper"`
	PrintError              int64         `json:"print_error"`
	SpeedLevel              PrintSpeed    `json:"spd_lvl"`
	SpeedMagnitude          int           `json:"spd_mag"`
	WiFiSignal              string        `json:"wifi_signal"`
	TaskID                  string        `json:"task_id"`
	SubtaskName             string        `json:"subtask_name"`
	SDCard                  bool          `json:"sdcard"`
	Lights                  []LightStatus `json:"lights_report"`
	AMS                     AMSStatus     `json:"ams"`
	ExternalSpool           Tray          `json:"vt_tray"`
	HMS                     []HMSError    `json:"hms"`
	Camera                  Object        `json:"ipcam"`
	XCam                    Object        `json:"xcam"`
	Upgrade                 Object        `json:"upgrade_state"`
}
type LightStatus struct {
	Node string `json:"node"`
	Mode string `json:"mode"`
}
type HMSError struct {
	Attr uint32 `json:"attr"`
	Code uint32 `json:"code"`
}
type AMSStatus struct {
	Units        []AMSUnit `json:"ams"`
	CurrentTray  string    `json:"tray_now"`
	TargetTray   string    `json:"tray_tar"`
	PreviousTray string    `json:"tray_pre"`
	Exists       string    `json:"ams_exist_bits"`
	TrayExists   string    `json:"tray_exist_bits"`
}
type AMSUnit struct {
	ID          string `json:"id"`
	Humidity    string `json:"humidity"`
	Temperature string `json:"temp"`
	Trays       []Tray `json:"tray"`
}
type Tray struct {
	ID            string `json:"id"`
	Type          string `json:"tray_type"`
	Color         string `json:"tray_color"`
	InfoIndex     string `json:"tray_info_idx"`
	Remaining     int    `json:"remain"`
	NozzleTempMin string `json:"nozzle_temp_min"`
	NozzleTempMax string `json:"nozzle_temp_max"`
	TagUID        string `json:"tag_uid"`
	UUID          string `json:"tray_uuid"`
}
type VersionModule struct {
	Name            string `json:"name"`
	Serial          string `json:"sn"`
	HardwareVersion string `json:"hw_ver"`
	SoftwareVersion string `json:"sw_ver"`
}

func (p *Printer) Status() (PrintStatus, error) {
	data, err := json.Marshal(p.Snapshot())
	if err != nil {
		return PrintStatus{}, err
	}
	var status PrintStatus
	err = json.Unmarshal(data, &status)
	return status, err
}
