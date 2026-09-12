package wiki

import "time"

type Page struct {
	ID            int64
	ProjectID     int64
	Title         string
	Body          string
	CreatedBy     int64
	CreatedByName string
	UpdatedBy     int64
	UpdatedByName string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type PageInput struct {
	Title string
	Body  string
}
