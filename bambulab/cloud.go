package bambulab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

type MessageQuery struct {
	Type  *int // nil omits the filter; a pointer to zero includes type=0.
	After string
	Limit int
}
type TaskQuery struct {
	DeviceID string
	After    string
	Limit    int
}

func pagination(after string, limit int) (url.Values, error) {
	if limit < 0 {
		return nil, fmt.Errorf("bambulab: limit must not be negative")
	}
	q := url.Values{}
	if after != "" {
		q.Set("after", after)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return q, nil
}
func idPath(prefix, id string) (string, error) {
	if id == "" || id == "." || id == ".." {
		return "", fmt.Errorf("bambulab: a nonempty resource ID is required")
	}
	return prefix + url.PathEscape(id), nil
}
func (c *Client) ListMessages(ctx context.Context, options MessageQuery) (response MessagesResponse, err error) {
	q, err := pagination(options.After, options.Limit)
	if err != nil {
		return response, err
	}
	if options.Type != nil {
		q.Set("type", strconv.Itoa(*options.Type))
	}
	err = c.Do(ctx, http.MethodGet, "/v1/user-service/my/messages", q, nil, &response)
	return
}
func (c *Client) ListTasks(ctx context.Context, options TaskQuery) (response TasksResponse, err error) {
	q, err := pagination(options.After, options.Limit)
	if err != nil {
		return response, err
	}
	if options.DeviceID != "" {
		q.Set("deviceId", options.DeviceID)
	}
	err = c.Do(ctx, http.MethodGet, "/v1/user-service/my/tasks", q, nil, &response)
	return
}
func (c *Client) CreateTask(ctx context.Context, task Task) (response Task, err error) {
	err = c.Do(ctx, http.MethodPost, "/v1/user-service/my/task", nil, task, &response)
	return
}

// GetTicket retains the undocumented ticket schema as JSON fields.
func (c *Client) GetTicket(ctx context.Context, id string) (response Object, err error) {
	p, err := idPath("/v1/user-service/my/ticket/", id)
	if err != nil {
		return nil, err
	}
	err = c.Do(ctx, http.MethodGet, p, nil, nil, &response)
	return
}
func (c *Client) GetPreference(ctx context.Context) (response Preference, err error) {
	err = c.Do(ctx, http.MethodGet, "/v1/design-user-service/my/preference", nil, nil, &response)
	return
}

// GetResources accepts type-to-version query pairs, e.g. slicer/plugins/cloud.
func (c *Client) GetResources(ctx context.Context, versions map[string]string) (response ResourcesResponse, err error) {
	q := url.Values{}
	for k, v := range versions {
		q.Set(k, v)
	}
	err = c.Do(ctx, http.MethodGet, "/v1/iot-service/api/slicer/resource", q, nil, &response)
	return
}
func (c *Client) ListSettings(ctx context.Context, version string) (response SettingsResponse, err error) {
	q := url.Values{}
	if version != "" {
		q.Set("version", version)
	}
	err = c.Do(ctx, http.MethodGet, "/v1/iot-service/api/slicer/setting", q, nil, &response)
	return
}
func (c *Client) GetSetting(ctx context.Context, id string) (response Setting, err error) {
	p, err := idPath("/v1/iot-service/api/slicer/setting/", id)
	if err != nil {
		return response, err
	}
	err = c.Do(ctx, http.MethodGet, p, nil, nil, &response)
	return
}

// UpdateDeviceInfo submits documented dev_id plus firmware-specific fields.
func (c *Client) UpdateDeviceInfo(ctx context.Context, id string, fields map[string]any) error {
	if id == "" {
		return fmt.Errorf("bambulab: device ID is required")
	}
	body := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		body[k] = v
	}
	body["dev_id"] = id
	return c.Do(ctx, http.MethodPatch, "/v1/iot-service/api/user/device/info", nil, body, nil)
}
func (c *Client) GetDeviceVersion(ctx context.Context, id string) (response DeviceVersionsResponse, err error) {
	if id == "" {
		return response, fmt.Errorf("bambulab: device ID is required")
	}
	err = c.Do(ctx, http.MethodGet, "/v1/iot-service/api/user/device/version", url.Values{"dev_id": {id}}, nil, &response)
	return
}
func (c *Client) GetNotification(ctx context.Context, action, ticket string) (response Object, err error) {
	if (action != "upload" && action != "import_mesh") || ticket == "" {
		return nil, fmt.Errorf("bambulab: action must be upload or import_mesh, and ticket is required")
	}
	err = c.Do(ctx, http.MethodGet, "/v1/iot-service/api/user/notification", url.Values{"action": {action}, "ticket": {ticket}}, nil, &response)
	return
}
func (c *Client) GetPrintStatus(ctx context.Context, force bool) (response PrintStatusResponse, err error) {
	err = c.Do(ctx, http.MethodGet, "/v1/iot-service/api/user/print", url.Values{"force": {strconv.FormatBool(force)}}, nil, &response)
	return
}
func (c *Client) GetProjectProfile(ctx context.Context, id, modelID string) (response ProjectProfile, err error) {
	p, err := idPath("/v1/iot-service/api/user/profile/", id)
	if err != nil {
		return response, err
	}
	q := url.Values{}
	if modelID != "" {
		q.Set("model_id", modelID)
	}
	err = c.Do(ctx, http.MethodGet, p, q, nil, &response)
	return
}
func (c *Client) ListProjects(ctx context.Context) (response ProjectsResponse, err error) {
	err = c.Do(ctx, http.MethodGet, "/v1/iot-service/api/user/project", nil, nil, &response)
	return
}
func (c *Client) GetProject(ctx context.Context, id string) (response Project, err error) {
	p, err := idPath("/v1/iot-service/api/user/project/", id)
	if err != nil {
		return response, err
	}
	err = c.Do(ctx, http.MethodGet, p, nil, nil, &response)
	return
}

// GetTask exposes the IoT task endpoint, which can reject even the user's own tasks.
func (c *Client) GetTask(ctx context.Context, id string) (response Object, err error) {
	p, err := idPath("/v1/iot-service/api/user/task/", id)
	if err != nil {
		return nil, err
	}
	err = c.Do(ctx, http.MethodGet, p, nil, nil, &response)
	return
}
func (c *Client) GetTTCode(ctx context.Context, id string) (response TTCodeResponse, err error) {
	if id == "" {
		return response, fmt.Errorf("bambulab: device ID is required")
	}
	err = c.Do(ctx, http.MethodPost, "/v1/iot-service/api/user/ttcode", nil, map[string]string{"dev_id": id}, &response)
	return
}
