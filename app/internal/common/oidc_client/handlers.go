package oidc_client

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const (
	// The following consts are the keys where information is added to the gin context.
	// e.g. to get the access token, use c.Get(AccessTokenKey).
	// where appropriate, they match the IANA token hints
	// https://www.iana.org/assignments/oauth-parameters/oauth-parameters.xml#token-type-hint

	// c.Get(AccessTokenKey) -> string
	AccessTokenKey = "access_token"
	// c.Get(RefreshTokenKey) -> string
	RefreshTokenKey = "refresh_token"
	// c.Get(IDTokenKey) -> string
	IDTokenKey = "id_token"
	// Claims and Subject aren't tokens. They contain information pulled out of the ID token for convenience.
	// c.Get(ClaimsKey) -> map[string]any
	ClaimsKey = "claims"
	// c.Get(SubjectKey) -> string
	SubjectKey = "subject"
	// c.Get("oidc_provider") -> *oidc.Provider
	OIDCProviderKey = "oidc_provider"
	// c.Get("oauth_config") -> *oauth2.Config
	OAuthConfigKey = "oauth_config"
)

// HandleLogin
// This function initiates the login process.
// It is responsible for setting up the oauth state and a unique PKCE challenge,
// and sends the user off to go authenticate with the auth server.
// The state will eventually be used by the HandleRedirect function
// to direct the user to the page they want to look at, so we need
// to set that up now. This function takes url query parameter ?next=
// with the value set to the next URL base64url-encoded.
// The redirect will follow the discovered Auth Code URL.
func (h *OauthHandlers) HandleLogin(c *gin.Context) {
	ses := sessions.DefaultMany(c, h.oauthSessionName)
	csrf := base64.RawURLEncoding.EncodeToString(random32())
	ses.Set("csrf", csrf)
	err := ses.Save()
	if err != nil {
		oops500(c, err)
		return
	}
	pkcePlain, pkceChallenge, err := generatePKCE()
	if err != nil {
		oops500(c, err)
		return
	}
	nextUrlEnc := c.Query("next")
	nextUrlDec, err := base64.RawURLEncoding.DecodeString(nextUrlEnc)
	if err != nil {
		nextUrlDec = []byte("/")
	}
	var nextUrl *url.URL
	nextUrl, err = url.Parse(string(nextUrlDec))
	if err != nil {
		nextUrl, _ = url.Parse("/")
	}
	state := &oauthState{
		NextUrl:   nextUrl,
		PKCEPlain: pkcePlain,
	}
	stateStr, err := marshalOauthState(state, oauthStatePassword(h.oauthPKCESecret, ses.ID(), csrf))
	if err != nil {
		oops500(c, err)
		return
	}
	// PKCEPlain is known only to us.
	// The csrf is known to us and to the browser. Use those
	// to generate a nonce.
	hash := sha256.New()
	hash.Write(pkcePlain)
	hash.Write([]byte(csrf))
	nonce := base64.RawURLEncoding.EncodeToString(hash.Sum([]byte(ses.ID())))
	url := h.oauth2config.AuthCodeURL(stateStr,
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("code_challenge", pkceChallenge),
		oauth2.SetAuthURLParam("nonce", nonce),
	)
	c.Redirect(http.StatusTemporaryRedirect, url)
}

// HandleLogout
// This function handles the logout process.
// It is responsible for removing the id_token from the user's session
// and it sends the user to the specified logout url.
// Many Oauth/OIDC servers have an endpoint that instructs the auth server to invalidate tokens.
// If you have one of those, it's good go use that as the Logout URL.
// Some have a revocation endpoint. If we discover a revocation end point during OIDC discovery,
// I'll try to revoke tokens with that before sending the user on to the oauthlogoutUrl
func (h *OauthHandlers) HandleLogout(c *gin.Context) {
	ses := sessions.DefaultMany(c, h.oauthSessionName)
	// Try the revocation endpoint if there is one.
	// Errors are ignored
	if h.oauthRevocationUrl != "" {
		if accessToken, ok := ses.Get(AccessTokenKey).(string); ok {
			RevokeToken(h.oauthRevocationUrl, h.oauth2config.ClientID, h.oauth2config.ClientSecret, accessToken, AccessTokenKey)
		}
		if refreshToken, ok := ses.Get(RefreshTokenKey).(string); ok {
			RevokeToken(h.oauthRevocationUrl, h.oauth2config.ClientID, h.oauth2config.ClientSecret, refreshToken, RefreshTokenKey)
		}
	}
	// ses.Delete(IDTokenKey)
	// ses.Delete(AccessTokenKey)
	// ses.Delete(RefreshTokenKey)
	// err := ses.Save()
	ses.Clear()
	// if err != nil {
	// 	oops500(c, err)
	// 	// don't return for this error. We stil want to redirect the user to continue the logout process.
	// }
	c.Redirect(http.StatusTemporaryRedirect, h.oauthLogoutUrl)
}

// HandleRedirect
// This function is the callback function where the user is redirected after login.
// It is responsible for completing the Oauth process. It extracts the auth code
// from the front channel and exchanges it for an access token on the back channel.
// Additionally, this function decodes the state parameter created at the HandleLogin phase.
// It uses the state parameter to determine where to send the the user next.
// Finally, saves the ID token to the user's session store and sends the user along.
func (h *OauthHandlers) HandleRedirect(c *gin.Context) {
	ses := sessions.DefaultMany(c, h.oauthSessionName)
	csrf, ok := ses.Get("csrf").(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "csrf missing from session"})
		return
	}
	stateStr := c.Query("state")
	if stateStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing state"})
		return
	}
	state, err := unmarshalOauthState(stateStr, oauthStatePassword(h.oauthPKCESecret, ses.ID(), csrf))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "state invalid. tampered?"})
		return
	}
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing auth code"})
		return
	}
	// Compute the nonce using our challenge and browser session.
	// If this is different than the nonce we generated in HandleLogin, the auth server should reject it.
	hash := sha256.New()
	hash.Write(state.PKCEPlain)
	hash.Write([]byte(csrf))
	nonce := base64.RawURLEncoding.EncodeToString(hash.Sum([]byte(ses.ID())))
	token, err := h.oauth2config.Exchange(context.Background(), code,
		oauth2.SetAuthURLParam("code_verifier", base64.RawURLEncoding.EncodeToString(state.PKCEPlain)),
		oauth2.SetAuthURLParam("nonce", nonce),
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed key exchange"})
		// log this error, since it might be a problem with the auth server.
		c.Error(fmt.Errorf("failed key exchange: %w", err))
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing id_token"})
		c.Error(fmt.Errorf("missing id_token in token response"))
		return
	}
	// decrypt and verify the ID token so we can verify the nonce.
	decIDToken, err := h.decrypter.DecryptToken(c, []byte(rawIDToken))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed id_token decryption"})
		c.Error(fmt.Errorf("failed id_token decryption: %w", err))
		return
	}
	idToken, err := h.verifier.Verify(c, string(decIDToken))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed id_token verification"})
		c.Error(fmt.Errorf("failed id_token verification: %w", err))
		return
	}
	// This ID Token should include our nonce. If it doesn't, something is fishy.
	if idToken.Nonce != nonce {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nonce does not match"})
		c.Error(fmt.Errorf("nonce does not match: %s != %s", idToken.Nonce, nonce))
		return
	}
	// Store tokens in the session.
	// If JWE encryption is used, we should store the encrypted tokens here since the session
	// may be in plain view of the user.
	ses.Set(IDTokenKey, rawIDToken)
	ses.Set(AccessTokenKey, token.AccessToken)
	ses.Set(RefreshTokenKey, token.RefreshToken)
	ses.Delete("csrf")
	err = ses.Save()
	if err != nil {
		oops500(c, err)
		return
	}
	c.Redirect(http.StatusTemporaryRedirect, state.NextUrl.String())
}

// MiddlewareRequireLogin
// This is a middleware that will block users who have not yet logged in or have an invalid token.
// It looks for an id_token in the user's session. This item is created by the HandleRedirect handler
// if the user has logged in previously.
// If there is no token, or if the token is invlid, then the user is redirected to the loginUrl.
// If everything goes well, then the user is passed to the next function. To make things a little easier
// for the next guy, a couple of values are set to the gin Context.
// "subject" -> the subject of the ID token. Can be used as a user identifier.
// "claims"  -> a map containing whatever claims came with the token. This will vary depending on the
// requested scopes and the behavior of the auth server.
func (h *OauthHandlers) MiddlewareRequireLogin(loginUrl string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ses := sessions.DefaultMany(c, h.oauthSessionName)
		encIDToken, ok := ses.Get(IDTokenKey).(string)
		if !ok {
			if loginUrl == "" {
				c.Abort()
				_ = c.Error(errors.New(`{"error":"You are not signed in"}`))
				return
			}
			next := base64.RawURLEncoding.EncodeToString([]byte(c.Request.URL.String()))
			nexturl := loginUrl + "?next=" + next
			c.Redirect(http.StatusTemporaryRedirect, nexturl)
			c.Abort()
			return
		}
		decIDToken, err := h.decrypter.DecryptToken(c, []byte(encIDToken))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed id_token decryption"})
			c.Error(fmt.Errorf("failed id_token decryption: %w", err))
			c.Abort()
			return
		}
		idToken, err := h.verifier.Verify(c, string(decIDToken))
		if err != nil {
			if loginUrl == "" {
				c.Abort()
				_ = c.Error(err)
				return
			}
			next := base64.RawURLEncoding.EncodeToString([]byte(c.Request.URL.String()))
			nexturl := loginUrl + "?next=" + next
			c.Redirect(http.StatusTemporaryRedirect, nexturl)
			c.Abort()
			return
		}
		c.Set(IDTokenKey, string(decIDToken))
		c.Set(SubjectKey, idToken.Subject)
		c.Set(OIDCProviderKey, h.oidcProvider)
		c.Set(OAuthConfigKey, h.oauth2config)
		claim := make(map[string]any)
		if err = idToken.Claims(&claim); err == nil {
			c.Set(ClaimsKey, claim)
		}
		if encAccessToken, ok := ses.Get(AccessTokenKey).(string); ok {
			accessToken, _ := h.decrypter.DecryptToken(c, []byte(encAccessToken))
			c.Set(AccessTokenKey, string(accessToken))
		}
		if encRefreshToken, ok := ses.Get(RefreshTokenKey).(string); ok {
			refreshToken, _ := h.decrypter.DecryptToken(c, []byte(encRefreshToken))
			c.Set(RefreshTokenKey, string(refreshToken))
		}
		c.Next()
	}
}

// utility function to generate a random 32-byte slice.
// PKCE and CSRF use this function.
func random32() []byte {
	b := make([]byte, 32)
	_, err := io.ReadFull(rand.Reader, b)
	if err != nil {
		panic(err)
	}
	return b
}

// returns generate PKCE verifier (plain, not encoded) and challenge (encoded)
// According to section 7.1 of RFC7636, the verifier should have 256 bits of entropy,
// so we use a 32 byte slice with random data.
// For the S256 challenge, we are supposed to use this formula:
//
//	code_challenge = BASE64URL-ENCODE(SHA256(ASCII(code_verifier)
//
// The code_verifier needs to be transmitted.
// Simply converting a random byte slice to ascii would result in transmitting
// odd non-printing characters. To avoid that, my ASCII() function is instead
// a second base64-url enoding. I'm returning the "plain" form since it's more compact.
// This is the same thing done in https://cs.opensource.google/go/x/oauth2/+/refs/tags/v0.28.0:pkce.go
// excpet I want the more compact "plain" form to be returned.
func generatePKCE() ([]byte, string, error) {
	plain := random32()
	verifier := base64.RawURLEncoding.EncodeToString(plain)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return plain, challenge, nil
}

type oauthState struct {
	NextUrl   *url.URL `json:"n"` // the URL we want to redirect to after login
	PKCEPlain []byte   `json:"p"` // the PKCE verifier. This is a short-lived secret. You should encrypt before sending.
}

// encryption key to consist of secrets only we know and unique elements of the session.
func oauthStatePassword(pkcesecret, sessionid, csrf string) string {
	return pkcesecret + sessionid + csrf
}

// encode struct, encrypt, and urlencode.
func marshalOauthState(o *oauthState, password string) (string, error) {
	keysum := sha256.Sum256([]byte(password))
	key := keysum[:]
	b, err := json.Marshal(o)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	ahead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, ahead.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	if err != nil {
		return "", err
	}
	ct := ahead.Seal(nonce, nonce, b, nil)
	return base64.RawURLEncoding.EncodeToString(ct), nil
}

// urldecode, decrypt, decode struct.
func unmarshalOauthState(state, password string) (*oauthState, error) {
	keysum := sha256.Sum256([]byte(password))
	key := keysum[:]
	ct, err := base64.RawURLEncoding.DecodeString(state)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	ahead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := ahead.NonceSize()
	if len(ct) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := ct[:nonceSize], ct[nonceSize:]
	b, err := ahead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}
	o := &oauthState{}
	err = json.Unmarshal(b, o)
	if err != nil {
		return nil, err
	}
	return o, nil
}

// RFC7009
// RevokeToken
// Authenticate with basic auth, Revoke a single token.
func RevokeToken(revocationEndpoint, clientid, clientsecret, token, hint string) error {
	values := url.Values{}
	values.Set("token", token)
	values.Set("token_type_hint", hint)
	body := strings.NewReader(values.Encode())
	req, err := http.NewRequest(http.MethodPost, revocationEndpoint, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(clientid, clientsecret)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

// Report a 500 error to the user and log it.
// The random UUID is to allow users to reference the error in support tickets
// without leaking sensitive information.
func oops500(c *gin.Context, err error) {
	id, _ := uuid.NewRandom()
	c.JSON(http.StatusInternalServerError, gin.H{"error": id.String()})
	c.Error(fmt.Errorf("ID: %s Error: %w", id.String(), err))
}
