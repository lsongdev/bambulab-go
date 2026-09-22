package bambulab

import "encoding/json"

// Object preserves fields whose schema is undocumented or firmware dependent.
type Object map[string]json.RawMessage

type Profile struct {
	ID              int      `json:"uid"`
	Account         string   `json:"account"`
	Name            string   `json:"name"`
	Avatar          string   `json:"avatar"`
	FanCount        int      `json:"fanCount"`
	FollowCount     int      `json:"followCount"`
	LikeCount       int      `json:"likeCount"`
	CollectionCount int      `json:"collectionCount"`
	DownloadCount   int      `json:"downloadCount"`
	ProductModels   []string `json:"productModels"`
	Personal        struct {
		Bio           string   `json:"bio"`
		Links         []string `json:"links"`
		BackgroundUrl string   `json:"backgroundUrl"`
	} `json:"personal"`
}
type Preference struct {
	ID            int64    `json:"uid"`
	Name          string   `json:"name"`
	Handle        string   `json:"handle"`
	Avatar        string   `json:"avatar"`
	Bio           string   `json:"bio"`
	Links         []string `json:"links"`
	BackgroundURL string   `json:"backgroundUrl"`
}
type Device struct {
	ID          string `json:"dev_id"`
	Name        string `json:"name"`
	Online      bool   `json:"online"`
	PrintStatus string `json:"print_status"`
	Model       string `json:"dev_model_name"`
	Product     string `json:"dev_product_name"`
	AccessCode  string `json:"dev_access_code"`
}
type BindResponse struct {
	ErrorResponse
	Devices []Device `json:"devices"`
}
type Message struct {
	ID          int64           `json:"id"`
	Type        int             `json:"type"`
	Design      json.RawMessage `json:"design"`
	Comment     json.RawMessage `json:"comment"`
	TaskMessage *TaskMessage    `json:"taskMessage"`
	From        *Profile        `json:"from"`
	CreateTime  string          `json:"createTime"`
}
type TaskMessage struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Cover    string `json:"cover"`
	Status   int    `json:"status"`
	DeviceID string `json:"deviceId"`
}
type MessagesResponse struct {
	Hits []Message `json:"hits"`
}
type Task struct {
	ID               int64             `json:"id,omitempty"`
	DesignID         int64             `json:"designId,omitempty"`
	ModelID          string            `json:"modelId,omitempty"`
	Title            string            `json:"title"`
	Cover            string            `json:"cover,omitempty"`
	Status           int               `json:"status"`
	FeedbackStatus   int               `json:"feedbackStatus,omitempty"`
	StartTime        string            `json:"startTime,omitempty"`
	EndTime          string            `json:"endTime,omitempty"`
	Weight           float64           `json:"weight,omitempty"`
	CostTime         int               `json:"costTime,omitempty"`
	ProfileID        int64             `json:"profileId,omitempty"`
	PlateIndex       int               `json:"plateIndex,omitempty"`
	DeviceID         string            `json:"deviceId"`
	AMSDetailMapping []json.RawMessage `json:"amsDetailMapping,omitempty"`
	Mode             string            `json:"mode,omitempty"`
}
type TasksResponse struct {
	Total int    `json:"total"`
	Hits  []Task `json:"hits"`
}
type Resource struct {
	Type        string `json:"type"`
	Version     string `json:"version"`
	Description string `json:"description"`
	URL         string `json:"url"`
	ForceUpdate bool   `json:"force_update"`
}
type ResourcesResponse struct {
	ErrorResponse
	Software  *Resource       `json:"software"`
	Guide     json.RawMessage `json:"guide"`
	Resources []Resource      `json:"resources"`
}
type SettingSummary struct {
	ID         string `json:"setting_id"`
	Version    string `json:"version"`
	Name       string `json:"name"`
	Nickname   string `json:"nickname"`
	FilamentID string `json:"filament_id"`
}
type SettingGroup struct {
	Public  []SettingSummary `json:"public"`
	Private []SettingSummary `json:"private"`
}
type SettingsResponse struct {
	ErrorResponse
	Print    SettingGroup `json:"print"`
	Printer  SettingGroup `json:"printer"`
	Filament SettingGroup `json:"filament"`
}
type Setting struct {
	ErrorResponse
	Public     bool   `json:"public"`
	Version    string `json:"version"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Nickname   string `json:"nickname"`
	BaseID     string `json:"base_id"`
	FilamentID string `json:"filament_id"`
	Setting    Object `json:"setting"`
}
type DeviceVersion struct {
	DeviceID string            `json:"dev_id"`
	Version  string            `json:"version"`
	Firmware []Resource        `json:"firmware"`
	AMS      []json.RawMessage `json:"ams"`
}
type DeviceVersionsResponse struct {
	ErrorResponse
	Devices []DeviceVersion `json:"devices"`
}
type DevicePrintStatus struct {
	ID         string  `json:"dev_id"`
	Name       string  `json:"dev_name"`
	Model      string  `json:"dev_model_name"`
	Product    string  `json:"dev_product_name"`
	Online     bool    `json:"dev_online"`
	AccessCode string  `json:"dev_access_code"`
	TaskID     *string `json:"task_id"`
	TaskName   *string `json:"task_name"`
	TaskStatus *string `json:"task_status"`
	ModelID    *string `json:"model_id"`
	ProjectID  *string `json:"project_id"`
	ProfileID  *string `json:"profile_id"`
	StartTime  *string `json:"start_time"`
	Prediction *int    `json:"prediction"`
	Progress   *int    `json:"progress"`
	Thumbnail  *string `json:"thumbnail"`
}
type PrintStatusResponse struct {
	ErrorResponse
	Devices []DevicePrintStatus `json:"devices"`
}
type FileReference struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
	URL  string `json:"url"`
}
type Plate struct {
	Index      int             `json:"index"`
	Thumbnail  FileReference   `json:"thumbnail"`
	Prediction int             `json:"prediction"`
	Weight     float64         `json:"weight"`
	GCode      FileReference   `json:"gcode"`
	Filaments  []PlateFilament `json:"filaments"`
}
type PlateFilament struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Color string `json:"color"`
	UsedM string `json:"used_m"`
	UsedG string `json:"used_g"`
}
type ProjectContext struct {
	Compatibility struct {
		Model          string  `json:"dev_model_name"`
		Product        string  `json:"dev_product_name"`
		NozzleDiameter float64 `json:"nozzle_diameter"`
	} `json:"compatibility"`
	Pictures  []FileReference `json:"pictures"`
	Configs   []FileReference `json:"configs"`
	Plates    []Plate         `json:"plates"`
	Materials []struct {
		Color    string `json:"color"`
		Material string `json:"material"`
	} `json:"materials"`
	AuxiliaryPictures []json.RawMessage `json:"auxiliary_pictures"`
	AuxiliaryBOM      []json.RawMessage `json:"auxiliary_bom"`
	AuxiliaryGuide    []json.RawMessage `json:"auxiliary_guide"`
	AuxiliaryOther    []json.RawMessage `json:"auxiliary_other"`
}
type ProjectProfile struct {
	ErrorResponse
	ID          string          `json:"profile_id"`
	ModelID     string          `json:"model_id"`
	Status      string          `json:"status"`
	Name        string          `json:"name"`
	Content     json.RawMessage `json:"content"`
	CreateTime  string          `json:"create_time"`
	UpdateTime  string          `json:"update_time"`
	Context     ProjectContext  `json:"context"`
	Filename    string          `json:"filename"`
	URL         string          `json:"url"`
	MD5         string          `json:"md5"`
	KeystoreXML string          `json:"keystore_xml"`
}
type Project struct {
	ErrorResponse
	ID           string           `json:"project_id"`
	UserID       string           `json:"user_id"`
	ModelID      string           `json:"model_id"`
	Status       string           `json:"status"`
	Name         string           `json:"name"`
	Content      json.RawMessage  `json:"content"`
	CreateTime   string           `json:"create_time"`
	UpdateTime   string           `json:"update_time"`
	Profiles     []ProjectProfile `json:"profiles"`
	DownloadURL  string           `json:"download_url"`
	DownloadMD5  string           `json:"download_md5"`
	KeystoreXML  string           `json:"keystore_xml"`
	UploadURL    string           `json:"upload_url"`
	UploadTicket string           `json:"upload_ticket"`
}
type ProjectsResponse struct {
	ErrorResponse
	Projects []Project `json:"projects"`
}
type TTCodeResponse struct {
	ErrorResponse
	TTCode   string `json:"ttcode"`
	Password string `json:"passwd"`
	AuthKey  string `json:"authkey"`
}
