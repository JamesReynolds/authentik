package application

import (
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/securecookie"
	"github.com/mitchellh/mapstructure"
)

type OAuthState struct {
	Issuer    string `json:"iss" mapstructure:"iss"`
	SessionID string `json:"sid" mapstructure:"sid"`
	State     string `json:"state" mapstructure:"state"`
	Redirect  string `json:"redirect" mapstructure:"redirect"`
}

func (oas *OAuthState) GetExpirationTime() (*jwt.NumericDate, error) { return nil, nil }
func (oas *OAuthState) GetIssuedAt() (*jwt.NumericDate, error)       { return nil, nil }
func (oas *OAuthState) GetNotBefore() (*jwt.NumericDate, error)      { return nil, nil }
func (oas *OAuthState) GetIssuer() (string, error)                   { return oas.Issuer, nil }
func (oas *OAuthState) GetSubject() (string, error)                  { return oas.State, nil }
func (oas *OAuthState) GetAudience() (jwt.ClaimStrings, error)       { return nil, nil }

var base32RawStdEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func (a *Application) createState(r *http.Request, fwd string) (string, error) {
	s, _ := a.sessions.Get(r, a.SessionName())
	if s.ID == "" {
		// Ensure session has an ID
		s.ID = base32RawStdEncoding.EncodeToString(securecookie.GenerateRandomKey(32))
	}
	st := &OAuthState{
		Issuer:    fmt.Sprintf("goauthentik.io/outpost/%s", a.proxyConfig.GetClientId()),
		State:     base64.RawURLEncoding.EncodeToString(securecookie.GenerateRandomKey(32)),
		SessionID: s.ID,
		Redirect:  fwd,
	}
	if fwd == "" {
		// This should only really be hit for nginx forward_auth
		// as for that the auth start redirect URL is generated by the
		// reverse proxy, and as such we won't have a request we just
		// denied to reference for final URL
		rd, ok := a.checkRedirectParam(r)
		if ok {
			a.log.WithField("rd", rd).Trace("Setting redirect")
			st.Redirect = rd
		}
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, st)
	tokenString, err := token.SignedString([]byte(a.proxyConfig.GetCookieSecret()))
	if err != nil {
		return "", err
	}
	return tokenString, nil
}

func (a *Application) stateFromRequest(r *http.Request) *OAuthState {
	stateJwt := r.URL.Query().Get("state")
	token, err := jwt.Parse(stateJwt, func(token *jwt.Token) (interface{}, error) {
		// Don't forget to validate the alg is what you expect:
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(a.proxyConfig.GetCookieSecret()), nil
	})
	if err != nil {
		a.log.WithError(err).Warning("failed to parse state jwt")
		return nil
	}
	iss, err := token.Claims.GetIssuer()
	if err != nil {
		a.log.WithError(err).Warning("state jwt without issuer")
		return nil
	}
	if iss != fmt.Sprintf("goauthentik.io/outpost/%s", a.proxyConfig.GetClientId()) {
		a.log.WithField("issuer", iss).Warning("invalid state jwt issuer")
		return nil
	}
	claims := &OAuthState{}
	err = mapstructure.Decode(token.Claims, &claims)
	if err != nil {
		a.log.WithError(err).Warning("failed to mapdecode")
		return nil
	}
	s, _ := a.sessions.Get(r, a.SessionName())
	if claims.SessionID != s.ID {
		a.log.WithField("is", claims.SessionID).WithField("should", s.ID).Warning("mismatched session ID")
		return nil
	}
	return claims
}
