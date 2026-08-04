package models

import "database/sql"

type VueTable struct {
	ID        int64  `json:"id" form:"id" query:"id"`
	ClientID  int64  `json:"client_id" form:"client_id" query:"client_id"`
	Page      int64  `json:"page" form:"page" query:"page"`
	PerPage   int64  `json:"per_page" form:"per_page" query:"per_page"`
	Sort      string `json:"sort" form:"sort" query:"sort"`
	StartDate string `json:"start_date" form:"start_date" query:"start_date"`
	EndDate   string `json:"end_date" form:"end_date" query:"end_date"`
	Search    string `json:"search" form:"search" query:"search"`
	Status    int64  `json:"status" form:"status" query:"status"`
	UserType  int64  `json:"user_type" form:"user_type" query:"user_type"`
	Download  int64  `json:"download" form:"download" query:"download"`
}

type Paginator struct {
	Dialect    string
	VueTable   VueTable
	TableName  string
	PrimaryKey string
	Fields     []string
	Joins      []string
	GroupBy    []string
	OrWhere    []string
	Having     []string
	Params     []interface{}
	Results    func(row *sql.Rows) []interface{}
}

type Pagination struct {
	Total       int         `json:"total"`
	PerPage     int         `json:"per_page"`
	NextPageUrl string      `json:"next_page_url"`
	PrevPageUrl string      `json:"prev_page_url"`
	CurrentPage int         `json:"current_page"`
	LastPage    int         `json:"last_page"`
	From        int         `json:"from"`
	To          int         `json:"to"`
	Data        interface{} `json:"data"`
}
