package api

import (
	"crypto/sha256"
	"crypto/subtle"
)

// Auth is tariffd's optional bearer-token gate. It holds the SHA-256 digest of
// every configured token and answers, in constant time, whether a presented
// token is one of them.
//
// There is no actor and no role here, unlike a data store's auth: tariffd
// stores nothing and attributes nothing, so a token is a plain admission
// check, not an identity. A nil *Auth means no tokens were configured and the
// compute endpoints are open (see config.Config.Tokens).
type Auth struct {
	// digests holds SHA-256 of each token. Comparing fixed-size digests rather
	// than the raw tokens gives every comparison the same length whatever the
	// presented token's length, so the constant-time compare is constant-time
	// in practice and not only in name.
	digests [][sha256.Size]byte
}

// NewAuth builds an Auth from a non-empty token set. It returns nil for an
// empty set, which the middleware reads as "open": callers decide whether to
// configure tokens at all.
func NewAuth(tokens []string) *Auth {
	if len(tokens) == 0 {
		return nil
	}
	a := &Auth{digests: make([][sha256.Size]byte, 0, len(tokens))}
	for _, t := range tokens {
		a.digests = append(a.digests, sha256.Sum256([]byte(t)))
	}
	return a
}

// Valid reports whether token matches a configured token. The scan never exits
// early and every comparison is crypto/subtle's constant-time compare over
// fixed-size digests, so response timing does not narrow a token down byte by
// byte. The token set is small and static; scanning all of it per request is
// the price of that property and it is cheap.
func (a *Auth) Valid(token string) bool {
	h := sha256.Sum256([]byte(token))
	var ok int
	for i := range a.digests {
		ok |= subtle.ConstantTimeCompare(h[:], a.digests[i][:])
	}
	return ok == 1
}
