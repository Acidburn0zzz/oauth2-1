package oauth2

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/hooklift/oauth2/internal/render"
	"github.com/hooklift/oauth2/types"
)

// Handlers is a map to functions where each function handles a particular HTTP
// verb or method.
var AuthzHandlers map[string]func(http.ResponseWriter, *http.Request, *config, http.Handler) = map[string]func(http.ResponseWriter, *http.Request, *config, http.Handler){
	"GET":    CreateGrant,
	"POST":   CreateGrant,
	"DELETE": RevokeGrant,
}

// AuthzData defines properties used to render the authorization form view
// that asks for authorization to the resource owner when using the web flow.
type AuthzData struct {
	// Client information.
	Client types.Client
	// Requested scope access from 3rd-party client
	Scopes []types.Scope
	// List of errors to display to the resource owner.
	Errors []AuthzError
	// Grant type is either "code" or "token" for implicit authorizations.
	GrantType string
	// State can be used to store CSRF tokens by the 3rd-party client app
	State string
}

// CreateGrant generates the authorization code for 3rd-party clients to use
// in order to get access and refresh tokens, asking the resource owner for authorization.
func CreateGrant(w http.ResponseWriter, req *http.Request, cfg *config, _ http.Handler) {
	if yes := cfg.provider.IsUserAuthenticated(); !yes {
		loginURL := cfg.provider.LoginURL(req.URL.String())
		http.Redirect(w, req, loginURL, http.StatusFound)
		return
	}

	vars := []string{"client_id", "state", "redirect_uri", "scope", "response_type"}
	params := make(map[string]string)
	for _, v := range vars {
		// FormValue also parses query string if method is GET
		params[v] = req.FormValue(v)
	}

	authzData := authCodeGrant1(w, req, cfg, params)
	if authzData == nil {
		// A response with an error was already sent back
		return
	}

	if req.Method == "GET" {
		// Displays authorization form to resource owner in order for her to
		// authorize 3rd-party client app.
		// TODO(c4milo): Figure out how to generate a CSRF token not tied to user's session
		render.HTML(w, render.Options{
			Status:    http.StatusOK,
			Data:      authzData,
			Template:  cfg.authzForm,
			STSMaxAge: cfg.stsMaxAge,
		})
		return
	}

	if params["response_type"] == "token" {
		// Continue with implicit grant flow
		implicitGrant(w, req, cfg, authzData)
		return
	}

	// 4.1.2.  Authorization Response
	// If the resource owner grants the access request, the authorization
	// server issues an authorization code and delivers it to the client by
	// adding the following parameters to the query component of the
	// redirection URI using the "application/x-www-form-urlencoded" format,
	// per Appendix B:
	// http://tools.ietf.org/html/rfc6749#section-4.2.1
	grantCode, err := cfg.provider.GenAuthzCode(authzData.Client, authzData.Scopes)
	if err != nil {
		render.HTML(w, render.Options{
			Status: http.StatusOK,
			Data: AuthzData{
				Errors: []AuthzError{
					ErrServerError("", err),
				}},
			Template: cfg.authzForm,
		})
		return
	}

	u := authzData.Client.RedirectURL
	query := u.Query()
	query.Set("code", grantCode.Code)
	query.Set("state", authzData.State)
	u.RawQuery = query.Encode()

	// log.Printf("[DEBUG] Redirect to: %s", u.String())
	http.Redirect(w, req, u.String(), http.StatusFound)
}

// AuthCodeGrant1 implements http://tools.ietf.org/html/rfc6749#section-4.1.1 and
// http://tools.ietf.org/html/rfc6749#section-4.2.1
func authCodeGrant1(w http.ResponseWriter, req *http.Request, cfg *config, params map[string]string) *AuthzData {
	// If the client identifier is missing or invalid, the authorization server
	// SHOULD inform the resource owner of the error and MUST NOT automatically
	// redirect the user-agent to the invalid redirection URI.
	clientID := params["client_id"]
	if clientID == "" {
		render.HTML(w, render.Options{
			Status: http.StatusOK,
			Data: AuthzData{
				Errors: []AuthzError{
					ErrClientIDMissing,
				},
			},
			Template: cfg.authzForm,
		})
		return nil
	}

	cinfo, err := cfg.provider.ClientInfo(clientID)
	if err != nil {
		render.HTML(w, render.Options{
			Status: http.StatusOK,
			Data: AuthzData{
				Errors: []AuthzError{
					ErrServerError("", err),
				},
			},
			Template: cfg.authzForm,
		})
		return nil
	}

	if &cinfo == nil {
		render.HTML(w, render.Options{
			Status: http.StatusOK,
			Data: AuthzData{
				Errors: []AuthzError{
					ErrClientIDNotFound,
				},
			},
			Template: cfg.authzForm,
		})
		return nil
	}

	// If the request fails due to a missing, invalid, or mismatching
	// redirection URI, the authorization server SHOULD inform the resource
	// owner of the error and MUST NOT automatically redirect the user-agent to the
	// invalid redirection URI.
	var redirectURL *url.URL
	if u, ok := params["redirect_uri"]; ok {
		var err error
		redirectURL, err = url.Parse(u)
		if err != nil {
			// We are deliberately avoiding sending client original parameters,
			// so the authorization process is forced to start all over again.
			render.HTML(w, render.Options{
				Status: http.StatusOK,
				Data: AuthzData{
					Errors: []AuthzError{
						ErrRedirectURLInvalid,
					},
				},
				Template: cfg.authzForm,
			})
			return nil
		}
	} else {
		redirectURL = cinfo.RedirectURL
	}

	if redirectURL.Scheme != "https" {
		render.HTML(w, render.Options{
			Status: http.StatusOK,
			Data: AuthzData{
				Errors: []AuthzError{
					ErrRedirectURLInvalid,
				},
			},
			Template: cfg.authzForm,
		})
		return nil
	}

	// The authorization server MUST verify that the redirection URI to which
	// it will redirect the authorization code or access token matches a redirection URI registered
	// by the client as described in Section 3.1.2.
	if redirectURL.String() != cinfo.RedirectURL.String() {
		render.HTML(w, render.Options{
			Status: http.StatusOK,
			Data: AuthzData{
				Errors: []AuthzError{
					ErrRedirectURLMismatch,
				},
			},
			Template: cfg.authzForm,
		})
		return nil
	}

	// An opaque value used by the client to maintain state between the request
	// and callback.  The authorization server includes this value when redirecting
	// the user-agent back to the client.  The parameter SHOULD be used for preventing
	// cross-site request forgery as described in Section 10.12.
	state := params["state"]
	if state == "" {
		EncodeErrInURI(redirectURL.Query(), ErrStateRequired(state))
		http.Redirect(w, req, redirectURL.String(), http.StatusFound)
		return nil
	}

	// response_type
	// Value MUST be set to "code" or "token" for implicit authorizations.
	grantType := params["response_type"]
	if grantType != "code" && grantType != "token" {
		EncodeErrInURI(redirectURL.Query(), ErrUnsupportedResponseType(state))
		http.Redirect(w, req, redirectURL.String(), http.StatusFound)
		return nil
	}

	// The scope of the access request as described by Section 3.3.
	scope := params["scope"]
	if scope == "" {
		EncodeErrInURI(redirectURL.Query(), ErrScopeRequired(state))
		http.Redirect(w, req, redirectURL.String(), http.StatusFound)
		return nil
	}

	scopes, err := cfg.provider.ScopesInfo(scope)
	if err != nil {
		EncodeErrInURI(redirectURL.Query(), ErrServerError(state, err))
		http.Redirect(w, req, redirectURL.String(), http.StatusFound)
		return nil
	}

	return &AuthzData{
		Client:    cinfo,
		Scopes:    scopes,
		GrantType: grantType,
		State:     state,
	}
}

// ImplicitGrant implements http://tools.ietf.org/html/rfc6749#section-4.2
func implicitGrant(w http.ResponseWriter, req *http.Request, cfg *config, authzData *AuthzData) {
	u := authzData.Client.RedirectURL

	token, err := cfg.provider.GenToken(types.AccessToken, authzData.Scopes, authzData.Client)
	if err != nil {
		EncodeErrInURI(u.Query(), ErrServerError(authzData.State, err))
		http.Redirect(w, req, u.String(), http.StatusFound)
		return
	}

	query := url.Values{}
	query.Set("access_token", token.Value)
	query.Set("token_type", token.Type)
	query.Set("expires_in", strconv.FormatFloat(token.ExpiresIn.Seconds(), 'f', -1, 64))
	query.Set("scope", StringifyScopes(token.Scope))
	query.Set("state", authzData.State)

	u.Fragment = "#" + query.Encode()
	http.Redirect(w, req, u.String(), http.StatusFound)
}

// RevokeGrant invalidates all tokens issued with the given grant authorization code.
func RevokeGrant(w http.ResponseWriter, req *http.Request, cfg *config, _ http.Handler) {
	//TODO(c4milo)
}
