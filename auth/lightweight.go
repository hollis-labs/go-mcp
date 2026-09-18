package auth

import (
	"context"
	"crypto/subtle"
	"net/http"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

// StaticProvider is go-mcp's lightweight default Provider: it accepts a
// fixed set of bearer tokens and needs no external calls, no key exchange,
// and no configuration beyond the tokens themselves. It is what a
// local/stdio/trusted deployment uses when it wants "don't hand the token
// back in the clear" without standing up OAuth infrastructure.
//
// Tokens carry no expiration by construction (they are static shared
// secrets, not short-lived credentials), so StaticProvider's default
// Options set AllowMissingExpiration. Rotate a token by removing it from
// the map; StaticProvider itself does no revocation bookkeeping.
type StaticProvider struct {
	tokens map[string]sdkauth.TokenInfo
	opts   sdkauth.RequireBearerTokenOptions
}

// NewStaticProvider builds a StaticProvider that accepts exactly the given
// bearer tokens. Each token maps to a UserID, used only to identify the
// caller in [sdkauth.TokenInfo.UserID] for downstream logging/audit — pass
// "" for tokens where only possession matters, not identity.
func NewStaticProvider(tokens map[string]string) *StaticProvider {
	p := &StaticProvider{
		tokens: make(map[string]sdkauth.TokenInfo, len(tokens)),
		opts:   sdkauth.RequireBearerTokenOptions{AllowMissingExpiration: true},
	}
	for token, userID := range tokens {
		p.tokens[token] = sdkauth.TokenInfo{UserID: userID}
	}
	return p
}

// WithScopes sets the scopes StaticProvider's accepted tokens are treated
// as carrying, and the scopes [HTTPMiddleware] will require. Every accepted
// token gets the same scope set — StaticProvider has no per-token scoping.
func (p *StaticProvider) WithScopes(scopes ...string) *StaticProvider {
	for token, info := range p.tokens {
		info.Scopes = scopes
		p.tokens[token] = info
	}
	p.opts.Scopes = scopes
	return p
}

// Verifier implements [Provider].
func (p *StaticProvider) Verifier() sdkauth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		for known, info := range p.tokens {
			if subtle.ConstantTimeCompare([]byte(token), []byte(known)) == 1 {
				result := info
				return &result, nil
			}
		}
		return nil, sdkauth.ErrInvalidToken
	}
}

// Options implements [Provider].
func (p *StaticProvider) Options() *sdkauth.RequireBearerTokenOptions {
	opts := p.opts
	return &opts
}
