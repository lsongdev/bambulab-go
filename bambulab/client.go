package bambulab

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
)

type Client struct {
	client *http.Client
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
	Code  int    `json:"code"`
	Error string `json:"error"`
}

func (c *Client) Login(credential *Credential) (resp *LoginResponse, err error) {
	// POST https://api.bambulab.com/v1/user-service/user/login
	body, err := json.Marshal(credential)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.bambulab.com/v1/user-service/user/login", bytes.NewBuffer(body))
	if err != nil {
		return
	}
	req.Header.Add("content-type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return
	}
	if res.StatusCode != 200 {
		errResp := ErrorResponse{}
		json.NewDecoder(res.Body).Decode(&errResp)
		err = errors.New(errResp.Error)
		return
	}
	// aaa, _ := io.ReadAll(res.Body)
	// log.Println(res.StatusCode, string(aaa))
	resp = &LoginResponse{}
	err = json.NewDecoder(res.Body).Decode(&resp)
	return
}
