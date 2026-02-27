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

func (c *Client) RegisterDefaultCommands() error {
	req := CreateGlobalApplicationCommandRequest{
		Name:        "play",
		Type:        1,
		Description: "Play a song from URL",
		Options: []ApplicationCommandOption{
			{
				Type:        3,
				Name:        "url",
				Description: "The URL of the song",
				Required:    true,
			},
			{
				Type:        3,
				Name:        "timestamp",
				Description: "The timestamp to start playing from",
				Required:    false,
			},
		},
	}

	return c.CreateGlobalApplicationCommand(req)
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

func (c *Client) CreateGlobalApplicationCommand(req CreateGlobalApplicationCommandRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	res, err := c.Client.Post(
		fmt.Sprintf("%s/applications/%s/commands", c.BaseUrl, c.AppId),
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if b, err := io.ReadAll(res.Body); err == nil {
		println("Response body:", string(b))
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to create command, status code: %d", res.StatusCode)
	}

	return nil
}

func (c *Client) DeleteGlobalApplicationCommand(commandID string) error {
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/applications/%s/commands/%s", c.BaseUrl, c.AppId, commandID), nil)
	if err != nil {
		fmt.Println("Error creating request:", err)
		return err
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		fmt.Println("Error sending request:", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to delete command, status code: %d", resp.StatusCode)
	}

	return nil
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
		return fmt.Errorf("failed to send message, status code: %d", res.StatusCode)
	}

	return nil
}

func (c *Client) BulkOverwriteGlobalApplicationCommands() error {
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/applications/%s/commands", c.BaseUrl, c.AppId), nil)
	if err != nil {
		fmt.Println("Error creating request:", err)
		return err
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		fmt.Println("Error sending request:", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete command, status code: %d", resp.StatusCode)
	}

	return nil
}
