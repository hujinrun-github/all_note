package model

type CalendarProjectSource struct {
	ProjectID  string `json:"project_id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Enabled    bool   `json:"enabled"`
	Default    bool   `json:"default"`
	Color      string `json:"color"`
	OrderIndex int    `json:"order_index"`
}

type CalendarProjectSourcesResponse struct {
	Sources           []CalendarProjectSource `json:"sources"`
	AvailableProjects []CalendarProjectSource `json:"available_projects"`
}

type CalendarProjectSourceInput struct {
	ProjectID  string `json:"project_id"`
	Enabled    bool   `json:"enabled"`
	Color      string `json:"color"`
	OrderIndex int    `json:"order_index"`
}

type SaveCalendarProjectSourcesRequest struct {
	Sources []CalendarProjectSourceInput `json:"sources"`
}
