package bambulab

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
)

const API = "https://api.bambulab.cn"

type Client struct {
	client      *http.Client
	AccessToken string
}

func NewClient() *Client {
	return &Client{
		client: http.DefaultClient,
	}
}

type Credential struct {
	Account  string `json:"account"`
	Password string `json:"password,omitempty"`
	Code     string `json:"code,omitempty"`
}

type LoginResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	LoginType    string `json:"loginType"`
	ExpiresIn    int    `json:"expiresIn"`
}

type ErrorResponse struct {
	Code    int    `json:"code"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (c *Client) request(method, path string, body any) (respBody []byte, err error) {
	var reqBody io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewBuffer(bodyBytes)
	}
	request, err := http.NewRequest(method, API+path, reqBody)
	if err != nil {
		return
	}
	if c.AccessToken != "" {
		request.Header.Add("Authorization", "Bearer "+c.AccessToken)
	}
	if body != nil {
		request.Header.Add("content-type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	respBody, err = io.ReadAll(response.Body)
	// log.Println(response.StatusCode, string(respBody))
	if response.StatusCode != 200 {
		errResp := ErrorResponse{}
		json.Unmarshal(respBody, &errResp)
		err = errors.New(errResp.Error)
		return
	}
	return
}

func (c *Client) Login(credential *Credential) (resp *LoginResponse, err error) {
	respBody, err := c.request(http.MethodPost, "/v1/user-service/user/login", credential)
	if err != nil {
		return
	}
	resp = &LoginResponse{}
	err = json.Unmarshal(respBody, resp)
	return
}

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

func (c *Client) GetProfile() (resp Profile, err error) {
	body, err := c.request(http.MethodGet, "/v1/user-service/my/profile", nil)
	if err != nil {
		return
	}
	log.Println(string(body))
	err = json.Unmarshal(body, &resp)
	return
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

	Devices []Device
}

func (c *Client) ListDevices() (resp BindResponse, err error) {
	body, err := c.request(http.MethodGet, "/v1/iot-service/api/user/bind", nil)
	if err != nil {
		return
	}
	err = json.Unmarshal(body, &resp)
	return
}
