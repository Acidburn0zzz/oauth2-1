// Package oauth2 implements an OAuth 2.0 authorization server with support
// for token revokation.
//
// For details about the specs implemented please refer to
// * http://tools.ietf.org/html/rfc6749
// * http://tools.ietf.org/html/rfc6750
// * https://tools.ietf.org/html/rfc7009
package oauth2

import (
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/context"
)

// Client defines client information required by oauth2 to:
//   * Show an authorization form to a resource owner
//   * Validate that the provided request_uri parameter matches the one previously
//     registered for the client.
type Client struct {
	ID            string
	Name          string
	Desc          string
	ProfileImgURL string
	HomepageURL   string
	RedirectURL   string
}

// Scope defines a type for manipulating OAuth2 scopes.
type Scope struct {
	ID   string
	Desc string
}

// Defines a type for the two defined token types in OAuth2.
type TokenType string

const (
	AccessToken  TokenType = "access"
	RefreshToken TokenType = "refresh"
)

type Provider interface {
	// ClientInfo returns 3rd-party client information
	ClientInfo(clientID string) (info Client, err error)

	// GenAuthzCode issues and stores an authorization grant code, in a persistent storage.
	GenAuthzCode(clientID, scopes []Scope) (code string, err error)

	// RevokeAuthzCode expires the grant code as well as all access and refresh tokens generated with it.
	RevokeAuthzCode(code string) error

	// ScopesInfo parses the list of scopes requested by the client and
	// returns its descriptions for the resource owner to fully understand
	// what he is authorizing the client to access to. An error is returned
	// if the scopes list does not comply with http://tools.ietf.org/html/rfc6749#section-3.3
	//
	// Unrecognized or non-existent scopes are ignored.
	ScopesInfo(scopes string) ([]Scope, error)

	// GenToken generates and stores token.
	GenToken(tokenType TokenType, scopes []Scope) (token string, err error)

	// RevokeToken expires a specific token.
	RevokeToken(token string) error

	// RefreshToken refreshes an access token.
	RefreshToken(refreshToken, scopes []Scope) (accessToken string, err error)

	// AuthzForm returns the HTML authorization form.
	AuthzForm() string

	// LoginURL returns the login URL for the resource owner to authenticate if there is
	// not a valid session. The authentication system should send back the user
	// to the referer URL in order to complete the OAuth2 authorization process.
	LoginURL(refererURL string) (url string)

	// CheckSession checks whether or not the resource owner has a valid session
	// with the system. If not, it redirects the user to the login URL.
	CheckSession() (invalid bool)
}

// http://commandcenter.blogspot.com/2014/01/self-referential-functions-and-design.html
type option func(*config)

type config struct {
	authzEndpoint  string
	tokenEndpoint  string
	revokeEndpoint string
	ctx            context.Context
	stsMaxAge      time.Duration
	authzForm      *template.Template
	provider       Provider
}

// TokenEndpoint allows setting token endpoint. Defaults to "/oauth2/tokens".
//
// The token endpoint is used by the client to obtain an access token by
// presenting its authorization grant or refresh token.  The token
// endpoint is used with every authorization grant except for the
// implicit grant type (since an access token is issued directly).
//
// Since requests to the token endpoint result in the transmission of
// clear-text credentials (in the HTTP request and response), the
// authorization server MUST require the use of TLS as described in
// Section 1.6 when sending requests to the token endpoint.
//
// -- http://tools.ietf.org/html/rfc6749#section-3.2
func SetTokenEndpoint(endpoint string) option {
	return func(c *config) {
		c.tokenEndpoint = endpoint
	}
}

// AuthzEndpoint allows setting authorization endpoint. Defaults to "/oauth2/authzs"
//
// The authorization endpoint is used to interact with the resource owner and
// obtain an authorization grant.
//
// Since requests to the authorization endpoint result in user authentication
// and the transmission of clear-text credentials (in the HTTP response), the
// authorization server MUST require the use of TLS as described in Section 1.6
// when sending requests to the authorization endpoint.
//
// -- http://tools.ietf.org/html/rfc6749#section-3.1.1
func SetAuthzEndpoint(endpoint string) option {
	return func(c *config) {
		c.authzEndpoint = endpoint
	}
}

// SetRevokeEndpoint allows setting a custom token revoke URI. Defaults to "/oauth2/revoke"
func SetRevokeEndpoint(endpoint string) option {
	return func(c *config) {
		c.revokeEndpoint = endpoint
	}
}

// SetSTSMaxAge sets Strict Transport Security maximum age. Defaults to 1yr
func SetSTSMaxAge(maxAge time.Duration) option {
	return func(c *config) {
		c.stsMaxAge = maxAge
	}
}

// SetAuthzForm sets authorization form to show to the resource owner
func SetAuthzForm(form string) option {
	return func(c *config) {
		t := template.New("authzform")
		tpl, err := t.Parse(form)
		if err != nil {
			log.Fatalln("Error parsing authorization form: %v", err)
		}
		c.authzForm = tpl
	}
}

// Handler handles OAuth2 requests.
func Handler(next http.Handler, opts ...option) http.Handler {
	// Default configuration options.
	cfg := &config{
		tokenEndpoint:  "/oauth2/tokens",
		authzEndpoint:  "/oauth2/authzs",
		revokeEndpoint: "/oauth2/revoke",
	}

	cfg.stsMaxAge = time.Duration(31536000) * time.Second // 1yr

	// Applies user's configuration.
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.authzForm == nil {
		log.Fatalln("Authorization form is required")
	}

	if cfg.provider == nil {
		log.Fatalln("An implementation of the oauth2.Provider interface is expected")
	}

	// Keeps a registry of path function handlers for OAuth2 requests.
	registry := map[string]map[string]func(http.ResponseWriter, *http.Request, *config, http.Handler){
		cfg.authzEndpoint: AuthzHandlers,
		cfg.tokenEndpoint: TokenHandlers,
		// TODO(c4milo): URL handlers for revoking tokens and grants
	}

	// Locates and runs specific OAuth2 handler for request's method
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for p, handlers := range registry {
			if strings.HasPrefix(req.URL.Path, p) {
				if handlerFn, ok := handlers[req.Method]; ok {
					handlerFn(w, req, cfg, next)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
				w.Write([]byte("Method Not Allowed"))
				return
			}
		}
	})
}
