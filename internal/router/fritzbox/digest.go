// Package fritzbox reads DSL line statistics and the event log from AVM FRITZ!Box routers via TR-064.
package fritzbox

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

type challenge struct {
	realm, nonce, qop, opaque, algorithm string
}

// parseChallenge reads a WWW-Authenticate: Digest header.
func parseChallenge(header string) (challenge, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(header), "Digest ")
	if !ok {
		return challenge{}, false
	}
	var ch challenge
	for _, part := range splitParams(rest) {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"`)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "realm":
			ch.realm = val
		case "nonce":
			ch.nonce = val
		case "qop":
			ch.qop = val
		case "opaque":
			ch.opaque = val
		case "algorithm":
			ch.algorithm = val
		}
	}
	return ch, ch.nonce != ""
}

// splitParams splits on commas outside quotes.
func splitParams(s string) []string {
	var parts []string
	var b strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			b.WriteRune(r)
		case r == ',' && !quoted:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// digestResponse computes the RFC 2617 response value (MD5, with or without qop=auth).
func digestResponse(ch challenge, user, pass, method, uri string, nc int, cnonce string) string {
	ha1 := md5hex(user + ":" + ch.realm + ":" + pass)
	ha2 := md5hex(method + ":" + uri)
	if hasAuthQop(ch.qop) {
		return md5hex(fmt.Sprintf("%s:%s:%08x:%s:auth:%s", ha1, ch.nonce, nc, cnonce, ha2))
	}
	return md5hex(ha1 + ":" + ch.nonce + ":" + ha2)
}

func hasAuthQop(qop string) bool {
	for _, q := range strings.Split(qop, ",") {
		if strings.TrimSpace(q) == "auth" {
			return true
		}
	}
	return false
}

func authorization(ch challenge, user, pass, method, uri string, nc int, cnonce string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=MD5, response="%s"`,
		user, ch.realm, ch.nonce, uri, digestResponse(ch, user, pass, method, uri, nc, cnonce))
	if hasAuthQop(ch.qop) {
		fmt.Fprintf(&b, `, qop=auth, nc=%08x, cnonce="%s"`, nc, cnonce)
	}
	if ch.opaque != "" {
		fmt.Fprintf(&b, `, opaque="%s"`, ch.opaque)
	}
	return b.String()
}

func newCnonce() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
