package api

type CreateMessageRequest struct {
	Content string `json:"content"`
}

type Application struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type GetGatewayResponse struct {
	Url string `json:"url"`
}
