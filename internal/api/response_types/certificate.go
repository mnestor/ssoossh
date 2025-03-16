// Created by Mike Nestor <me@mikenestor.org>
package types

type CertificateRequest struct {
	ID string `json:"id"`
}

type CertificateRequestResponse struct {
	*ResponseRender
	StatusText  string `json:"status"`
	Message     string `json:"message"`
	Certificate string `json:"certificate"`
}

func NewCertificateRequestResponse(s string, c string) *CertificateRequestResponse {
	return &CertificateRequestResponse{
		StatusText:  s,
		Certificate: c,
	}
}
