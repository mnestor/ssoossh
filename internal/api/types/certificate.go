// Created by Mike Nestor <me@mikenestor.org>
package types

type CertificateRequest struct {
	ID string `json:"id"`
}

type CertificateRequestResponse struct {
	*ResponseBase
	Certificate string `json:"certificate"`
}

func NewCertificateRequestResponse(s string, c string) *CertificateRequestResponse {
	return &CertificateRequestResponse{
		ResponseBase: &ResponseBase{StatusText: s},
		Certificate:  c,
	}
}

// func (a *CertificateRequest) Bind(r *http.Request) error {
// 	if a.ID == "" {
// 		return errors.New("missing required signrequest fields")
// 	}

// 	// just a post-process after a decode..
// 	return nil
// }
