package apitypes

// RetrieveRequestBody is the POST /api/certs/service/retrieve request
// body. Only the code is posted — never a public key, so a stolen code
// can't be paired with an attacker's keypair (see
// docs/internals/design-brief.md, "Service enrollment").
type RetrieveRequestBody struct {
	Code string `json:"code" binding:"required"`
}

// RetrieveResponse is POST /api/certs/service/retrieve's response body.
type RetrieveResponse struct {
	Certificate string `json:"certificate" validate:"required"`
}
