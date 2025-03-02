// Created by Mike Nestor <me@mikenestor.org>
package types

type ResponseCAList struct {
	*ResponseRender
	StatusText string `json:"status"`
	CA         string `json:"ca"`
}

func NewCAListResponse(s string, ca string) *ResponseCAList {
	return &ResponseCAList{
		StatusText: s,
		CA:         ca,
	}
}
