package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type GatewayURLProvider interface {
	GetGatewayUrl() (string, error)
}

type Client struct {
	BaseUrl string
	AppId   string
	Client  *http.Client
}

var _ GatewayURLProvider = (*Client)(nil)

type authTransport struct {
	Token     string
	Transport http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	newReq := req.Clone(req.Context())

	newReq.Header.Set("Authorization", fmt.Sprintf("Bot %s", t.Token))
	newReq.Header.Set("Accept", "application/json")

	transport := t.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(newReq)
}

func NewClient(url string, token string) (*Client, error) {
	client := &Client{
		BaseUrl: url,
		Client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &authTransport{
				Token: token,
			},
		},
	}

	application, err := client.GetCurrentApplication()
	if err != nil {
		return nil, err
	}
	client.AppId = application.ID

	return client, nil
}

func (c *Client) GetCurrentApplication() (Application, error) {
	res, err := c.Client.Get(fmt.Sprintf("%s/applications/@me", c.BaseUrl))
	if err != nil {
		return Application{}, err
	}
	defer res.Body.Close()

	var response Application
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return Application{}, err
	}

	return response, nil
}

func (c *Client) GetGatewayUrl() (string, error) {
	res, err := c.Client.Get(fmt.Sprintf("%s/gateway", c.BaseUrl))
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var response GetGatewayResponse
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return "", err
	}

	return response.Url, nil
}

func (c *Client) SendMessage(channelID, content string) error {
	body, err := json.Marshal(CreateMessageRequest{Content: content})
	if err != nil {
		return err
	}

	res, err := c.Client.Post(
		fmt.Sprintf("%s/channels/%s/messages", c.BaseUrl, channelID),
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("failed to send message, status code: %d, body: %s", res.StatusCode, b)
	}

	return nil
}
