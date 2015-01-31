package oauth2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hooklift/oauth2/providers/test"
	"github.com/hooklift/oauth2/types"
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

	// makes sure the same state parameter value received to acquire
	// the authorization grant is send back when delivering the access token.
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

// TestReplayAttackProtection tests that the authorization grant can be used
// only once.
func TestReplayAttackProtection(t *testing.T) {
	provider, authzCode := getTestAuthzCode(t)

	req := AuthzGrantTokenRequestTest(t, "authorization_code", authzCode)
	req.SetBasicAuth("test_client_id", "test_client_id")

	w := httptest.NewRecorder()
	IssueToken(w, req, provider)
	token := types.Token{}
	err := json.Unmarshal(w.Body.Bytes(), &token)
	ok(t, err)
	equals(t, "bearer", token.Type)
	equals(t, "600", token.ExpiresIn)

	w2 := httptest.NewRecorder()
	IssueToken(w2, req, provider)

	// http://tools.ietf.org/html/rfc6749#section-4.1.4
	authzErr := types.AuthzError{}
	//log.Printf("%s", w2.Body.String())
	err = json.Unmarshal(w2.Body.Bytes(), &authzErr)
	ok(t, err)
	equals(t, "invalid_grant", authzErr.Code)
	equals(t, "Grant code was revoked, expired or already used.", authzErr.Desc)

}

// TestRedirectURLMatch makes sure redirect_uri for requesting an authorization
// grant is the same as the redirect_uri provided to get the correspondent access token.
// This is intended to mitigate the risk of account hijacking by leaking
// authorization codes.
func TestRedirectURLMatch(t *testing.T) {
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

	// Sending post to acquire authorization token
	values.Set("redirect_uri", "https://attacker.com/callback")
	queryStr2 := values.Encode()
	buffer := bytes.NewBufferString(queryStr2)
	req, err = http.NewRequest("POST", "https://example.com/oauth2/authzs", buffer)
	ok(t, err)

	req.Header.Set("Content-type", "application/x-www-form-urlencoded")

	w2 := httptest.NewRecorder()
	CreateGrant(w2, req, provider)
	body := w2.Body.String()
	assert(t, strings.Contains(body, "access_denied"), "access_denied was expected as response")
	assert(t, strings.Contains(body, "3rd-party client app provided a redirect_uri that does not match the URI registered for this client in our database."), "unexpected error description.")
}

// TestAccessTokenOwnership makes sure a token belongs to the client_id making
// the request with it. This mitigates account hijacking as well.
func TestAccessTokenOwnership(t *testing.T) {

}

// TestAccessTokenExpiration makes sure that access tokens are actually expired.
func TestAccessTokenExpiration(t *testing.T) {

}

// TestScopeIsRequired makes sure it requires clients to provide access scopes when
// getting authorization codes.
func TestScopeIsRequired(t *testing.T) {
	provider := test.NewProvider(true)
	state := "my-state"
	grantType := "code"

	values := url.Values{
		"client_id":     {provider.Client.ID},
		"response_type": {grantType},
		"state":         {state},
		"redirect_uri":  {provider.Client.RedirectURL.String()},
	}

	// http://tools.ietf.org/html/rfc6749#section-4.1.1
	queryStr := values.Encode()
	req, err := http.NewRequest("GET",
		"https://example.com/oauth2/authzs?"+queryStr, nil)
	ok(t, err)

	w := httptest.NewRecorder()
	CreateGrant(w, req, provider)
	equals(t, http.StatusFound, w.Code)
	u, err := url.Parse(w.Header().Get("Location"))
	ok(t, err)
	equals(t, "invalid_request", u.Query().Get("error"))
	equals(t, "scope parameter is required by this authorization server.", u.Query().Get("error_description"))
}

// TestStateIsRequired makes sure it requires clients to provide a state when
// getting authorization codes.
func TestStateIsRequired(t *testing.T) {
	provider := test.NewProvider(true)

	scopes := "read write identity"
	grantType := "code"

	values := url.Values{
		"client_id":     {provider.Client.ID},
		"response_type": {grantType},
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
	equals(t, http.StatusFound, w.Code)
	u, err := url.Parse(w.Header().Get("Location"))
	ok(t, err)
	equals(t, "invalid_request", u.Query().Get("error"))
	equals(t, "state parameter is required by this authorization server.", u.Query().Get("error_description"))
}

// TestSecurityHeaders makes sure security headers are sent along the authorization form.
func TestSecurityHeaders(t *testing.T) {
	provider := test.NewProvider(true)

	state := "mystate"
	scopes := "read write identity"
	grantType := "code"

	values := url.Values{
		"client_id":     {provider.Client.ID},
		"state":         {state},
		"response_type": {grantType},
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
	//log.Printf("%+v", w.HeaderMap)

	equals(t, "max-age=0", w.Header().Get("Strict-Transport-Security"))
	equals(t, "1; mode=block", w.Header().Get("X-XSS-Protection"))
	equals(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	equals(t, "SAMEORIGIN", w.Header().Get("X-Frame-Options"))
}

// TestRedirectURIScheme makes sure clients provide redirect URLs that use TLS
func TestRedirectURIScheme(t *testing.T) {
	provider := test.NewProvider(true)

	state := "state-test"
	scopes := "read write identity"
	grantType := "code"

	values := url.Values{
		"client_id":     {provider.Client.ID},
		"response_type": {grantType},
		"state":         {state},
		"redirect_uri":  {"http://attacker.com/callback"},
		"scope":         {scopes},
	}

	// http://tools.ietf.org/html/rfc6749#section-4.1.1
	queryStr := values.Encode()
	req, err := http.NewRequest("GET",
		"https://example.com/oauth2/authzs?"+queryStr, nil)
	ok(t, err)

	w := httptest.NewRecorder()
	CreateGrant(w, req, provider)
	body := w.Body.String()
	assert(t, strings.Contains(body, "access_denied") == true, "access-denied was not found in response body")
	assert(t, strings.Contains(body, "3rd-party client app provided an invalid redirect_uri. It does not comply with http://tools.ietf.org/html/rfc3986#section-4.3 or does not use HTTPS") == true, "error description does not match.")
}
