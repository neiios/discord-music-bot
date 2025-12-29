package api

type ApplicationCommandOption struct {
	Type        int    `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type CreateGlobalApplicationCommandRequest struct {
	Name        string                     `json:"name"`
	Type        int                        `json:"type"`
	Description string                     `json:"description"`
	Options     []ApplicationCommandOption `json:"options,omitempty"`
}

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
