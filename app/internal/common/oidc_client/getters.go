package oidc_client

import (
	"context"
	"errors"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
)

// This file contains helper functions for users of this package to get information.
// Most of the functions simply get information out of the *gin.Context

var (
	ErrClaimNotExists = errors.New("claim not found in context")
	ErrKeyNotExists   = errors.New("key not found in context")
	ErrKeyWrongType   = errors.New("key is not of the expected type")
)

func GetAccessTokenStringFromContext(c *gin.Context) (string, error) {
	return GetStringFromContext(c, AccessTokenKey)
}

func GetRefreshTokenStringFromContext(c *gin.Context) (string, error) {
	return GetStringFromContext(c, RefreshTokenKey)
}

func GetIDTokenStringFromContext(c *gin.Context) (string, error) {
	return GetStringFromContext(c, IDTokenKey)
}

func GetSubjectFromContext(c *gin.Context) (string, error) {
	return GetStringFromContext(c, SubjectKey)
}

func GetStringFromContext(c *gin.Context, key string) (string, error) {
	idToken, exists := c.Get(key)
	if !exists {
		return "", ErrKeyNotExists
	}
	idTokenStr, ok := idToken.(string)
	if !ok {
		return "", ErrKeyWrongType
	}
	return idTokenStr, nil
}

func GetClaimsFromContext(c *gin.Context) (map[string]any, error) {
	claims, exists := c.Get(ClaimsKey)
	if !exists {
		return nil, ErrKeyNotExists
	}
	claimsMap, ok := claims.(map[string]any)
	if !ok {
		return nil, ErrKeyWrongType
	}
	return claimsMap, nil
}

func GetClaimFromContext(c *gin.Context, claim string) (any, error) {
	claims, err := GetClaimsFromContext(c)
	if err != nil {
		return nil, err
	}
	claimValue, exists := claims[claim]
	if !exists {
		return nil, ErrClaimNotExists
	}
	return claimValue, nil
}

// Token source for users that have OauthHandlers
// outside of a request context
func TokenSource(oh *OauthHandlers, ctx context.Context, refreshToken string) oauth2.TokenSource {
	return oh.oauth2config.TokenSource(ctx, &oauth2.Token{
		RefreshToken: refreshToken,
	})
}

// Token source for users inside a request context.
func TokenSourceFromContext(c *gin.Context) (oauth2.TokenSource, error) {
	oauth2configVal, exists := c.Get(OAuthConfigKey)
	if !exists {
		return nil, ErrKeyNotExists
	}
	oauth2config, ok := oauth2configVal.(*oauth2.Config)
	if !ok {
		return nil, ErrKeyWrongType
	}
	refreshToken, _ := GetRefreshTokenStringFromContext(c)
	accessToken, _ := GetAccessTokenStringFromContext(c)

	return oauth2config.TokenSource(c.Request.Context(), &oauth2.Token{
		RefreshToken: refreshToken,
		AccessToken:  accessToken,
	}), nil
}

// OIDC Provider for for users that have OauthHandlers
// outside of a request context
func OIDCProvider(oh *OauthHandlers) *oidc.Provider {
	return oh.oidcProvider
}

// OIDC Provider for users inside a request context
func OIDCProviderFromContext(c *gin.Context) (*oidc.Provider, error) {
	providerVal, exists := c.Get(OIDCProviderKey)
	if !exists {
		return nil, ErrKeyNotExists
	}
	provider, ok := providerVal.(*oidc.Provider)
	if !ok {
		return nil, ErrKeyWrongType
	}
	return provider, nil
}

// helper to hit the OIDC UserInfo endpoint
func UserInfoFromContext(c *gin.Context) (*oidc.UserInfo, error) {
	ts, err := TokenSourceFromContext(c)
	if err != nil {
		return nil, err
	}
	provider, err := OIDCProviderFromContext(c)
	if err != nil {
		return nil, err
	}
	return provider.UserInfo(c.Request.Context(), ts)
}
