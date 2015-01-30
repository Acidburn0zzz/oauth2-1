package oauth2

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hooklift/oauth2/providers/test"
)

// getTestAuthzCode returns authorization tokens for access tokens issuing tests
func getTestAuthzCode(t *testing.T) (Provider, string) {
	provider := test.NewProvider(true)

	state := "state-test"
	scopes := "read write identity"
	grantType := "code"

	values := url.Values{
		"client_id":     {provider.Client.ID},
		"response_type": {grantType},
		"state":         {state},
		"redirect_uri":  {provider.Client.RedirectURL.String()},
		"scope":         {scopes},
	}

	// http://tools.ietf.org/html/rfc6749#section-4.1.1
	queryStr := values.Encode()
	req, err := http.NewRequest("GET",
		"https://example.com/oauth2/authzs?"+queryStr, nil)
	ok(t, err)

	w := httptest.NewRecorder()
	CreateGrant(w, req, provider)
	equals(t, http.StatusOK, w.Code)

	body := w.Body.String()
	stringz := []string{
		"client_id",
		"redirect_uri",
		"response_type",
		"state",
		"scope",
		"code",
		"read write identity",
		"state-test",
	}

	for _, s := range stringz {
		assert(t, strings.Contains(body, s), "Does not look like we got an authorization form: '%s' was not found in %v", s, body)
	}

	// Sending post to acquire authorization token
	buffer := bytes.NewBufferString(queryStr)
	req, err = http.NewRequest("POST", "https://example.com/oauth2/authzs", buffer)
	ok(t, err)

	req.Header.Set("Content-type", "application/x-www-form-urlencoded")

	w = httptest.NewRecorder()
	CreateGrant(w, req, provider)

	// Tests http://tools.ietf.org/html/rfc6749#section-4.1.2
	equals(t, http.StatusFound, w.Code)

	redirectTo := w.Header().Get("Location")
	u, err := url.Parse(redirectTo)
	ok(t, err)

	authzCode := u.Query().Get("code")
	assert(t, authzCode != "", "It looks like the authorization code came back empty: %s", authzCode)
	equals(t, state, u.Query().Get("state"))

	return provider, authzCode
}

// TestAuthorizationGrant tests a happy web authorization flow in accordance with
// http://tools.ietf.org/html/rfc6749#section-4.1
func TestAuthorizationGrant(t *testing.T) {
	getTestAuthzCode(t)
}

// TestLoginRedirect tests that logging in is required for a resource owner to
// grant any authorization codes to clients.
func TestLoginRedirect(t *testing.T) {
	provider := test.NewProvider(false)

	state := "state-test"
	scopes := "read write identity"
	grantType := "code"
	clientID := provider.Client.ID
	redirectURL := provider.Client.RedirectURL.String()

	values := url.Values{
		"client_id":     {clientID},
		"response_type": {grantType},
		"state":         {state},
		"redirect_uri":  {redirectURL},
		"scope":         {scopes},
	}

	// http://tools.ietf.org/html/rfc6749#section-4.1.1
	queryStr := values.Encode()
	authzURL := "https://example.com/oauth2/authzs?" + queryStr
	req, err := http.NewRequest("GET", authzURL, nil)
	ok(t, err)

	w := httptest.NewRecorder()
	CreateGrant(w, req, provider)
	equals(t, http.StatusFound, w.Code)
	equals(t, provider.LoginURL(authzURL), w.Header().Get("Location"))
}

// TestImplicitGrant tests a happy implicit flow
func TestImplicitGrant(t *testing.T) {
	provider := test.NewProvider(true)

	state := "state-test"
	scopes := "read write identity"
	grantType := "token"
	clientID := provider.Client.ID
	redirectURL := provider.Client.RedirectURL.String()

	values := url.Values{
		"client_id":     {clientID},
		"response_type": {grantType},
		"state":         {state},
		"redirect_uri":  {redirectURL},
		"scope":         {scopes},
	}

	// http://tools.ietf.org/html/rfc6749#section-4.2.1
	queryStr := values.Encode()
	authzURL := "https://example.com/oauth2/authzs?" + queryStr
	req, err := http.NewRequest("GET", authzURL, nil)
	ok(t, err)

	w := httptest.NewRecorder()
	CreateGrant(w, req, provider)
	body := w.Body.String()
	stringz := []string{
		"client_id",
		"redirect_uri",
		"response_type",
		"state",
		"scope",
		"token",
		"read write identity",
		"state-test",
	}

	for _, s := range stringz {
		assert(t, strings.Contains(body, s), "Does not look like we got an authorization form: '%s' was not found in %v", s, body)
	}

	// Sending post to acquire authorization token
	buffer := bytes.NewBufferString(queryStr)
	req, err = http.NewRequest("POST", "https://example.com/oauth2/authzs", buffer)
	ok(t, err)

	req.Header.Set("Content-type", "application/x-www-form-urlencoded")

	w = httptest.NewRecorder()
	CreateGrant(w, req, provider)

	// Tests http://tools.ietf.org/html/rfc6749#section-4.2.2
	equals(t, http.StatusFound, w.Code)

	redirectTo := w.Header().Get("Location")
	u, err := url.Parse(redirectTo)
	ok(t, err)

	fragment, err := url.ParseQuery(strings.TrimPrefix(u.Fragment, "#"))
	ok(t, err)
	accessToken := fragment.Get("access_token")
	assert(t, accessToken != "", "It looks like the authorization code came back empty: ->%s<-", accessToken)
	equals(t, state, fragment.Get("state"))
	equals(t, "600", fragment.Get("expires_in"))
	equals(t, scopes, fragment.Get("scope"))
	equals(t, "bearer", fragment.Get("token_type"))

	// Implict flow should not emit refresh tokens
	refreshToken := fragment.Get("refresh_token")
	equals(t, "", refreshToken)
}

// TestReplayAttackMitigation tests that the authorization grant can be used
// only once, and that if there are attempts to use it multiple times, it is
// revoked along with all the access tokens generated with it.
func TestReplayAttackMitigation(t *testing.T) {

}

// TestRedirectURLMatch makes sure redirect_uri for requesting an authorization
// grant is the same as the redirect_uri provided to get the correspondent access token.
// This is intended to mitigate the risk of account hijacking by leaking
// authorization codes.
func TestRedirectURLMatch(t *testing.T) {

}

// TestAccessTokenIssuer makes sure a token belongs to the client_id making
// the request with it. This mitigates account hijacking as well.
func TestAccessTokenOwnership(t *testing.T) {

}

// TestAccessTokenExpiration makes sure that access tokens are actually expired.
func TestAccessTokenExpiration(t *testing.T) {

}

// TestScopeIsRequired makes sure it requires clients to provide access scopes when
// getting authorization codes.
func TestScopeIsRequired(t *testing.T) {

}

// TestStateIsRequired makes sure it requires clients to provide a state when
// getting authorization codes.
func TestStateIsRequired(t *testing.T) {

}

// TestSecurityHeaders makes sure security headers are sent along the authorization form.
func TestSecurityHeaders(t *testing.T) {

}

// TestRedirectURIScheme makes sure clients provide redirect URLs that use TLS
func TestRedirectURIScheme(t *testing.T) {

}

// TestRedirectURIUniqueness makes sure there redirect URL is unique across the
// entire system.
func TestRedirectURIUniqueness(t *testing.T) {

}

// TestStateParam makes sure the same state parameter value received to acquire
// the authorization grant is send back when delivering the access token.
func TestStateParam(t *testing.T) {

}
