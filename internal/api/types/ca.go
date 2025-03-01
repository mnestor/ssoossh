// Created by Mike Nestor <me@mikenestor.org>
package types

type ResponseCAList struct {
	*ResponseBase
	CA string `json:"ca"`
}

func NewCAListResponse(s string, ca string) *ResponseCAList {
	return &ResponseCAList{
		ResponseBase: &ResponseBase{StatusText: s},
		CA:           ca,
	}
}
