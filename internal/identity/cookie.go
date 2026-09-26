package identity

import (
	"net/http"
	"net/url"
	"strings"
)

// TokenFromRequest returns the token carried by the first present cookie of
// names (in order), percent-decoded ('+' stays a literal '+': cookie values
// are not form-encoded, and base64 tokens carry it) and with a leading "Bearer " stripped. "" when
// none is present.
func TokenFromRequest(r *http.Request, names []string) string {
	for _, n := range names {
		ck, err := r.Cookie(n)
		if err != nil || ck.Value == "" {
			continue
		}
		v := ck.Value
		if dec, err := url.PathUnescape(v); err == nil {
			v = dec
		}
		v = strings.TrimSpace(v)
		if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			v = strings.TrimSpace(v[7:])
		}
		if v != "" {
			return v
		}
	}
	return ""
}
