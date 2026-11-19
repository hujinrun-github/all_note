package japaneseintegration

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderConnection = "X-FlowSpace-Connection"
	HeaderKeyID      = "X-FlowSpace-Key-Id"
	HeaderTimestamp  = "X-FlowSpace-Timestamp"
	HeaderNonce      = "X-FlowSpace-Nonce"
	HeaderSignature  = "X-FlowSpace-Signature"
)

func canonicalTarget(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	prefix := "/api/integrations/japanese/"
	at := strings.Index(u.Path, prefix)
	if at < 0 {
		return ""
	}
	path := u.EscapedPath()
	if u.RawPath != "" {
		path = u.Path
	}
	// Deployment prefixes are transport concerns; sign only the API contract.
	at = strings.Index(path, prefix)
	if at < 0 {
		return ""
	}
	path = path[at:]
	if query := u.Query().Encode(); query != "" {
		path += "?" + query
	}
	return path
}
func signature(method, target, connection, keyID, timestamp, nonce, secret string, body []byte) string {
	sum := sha256.Sum256(body)
	text := strings.Join([]string{strings.ToUpper(method), canonicalTarget(target), connection, keyID, timestamp, nonce, hex.EncodeToString(sum[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(text))
	return hex.EncodeToString(mac.Sum(nil))
}
func Sign(headers http.Header, method, target, connection, keyID, secret string, body []byte, now time.Time, nonce string) {
	ts := strconv.FormatInt(now.Unix(), 10)
	headers.Set(HeaderConnection, connection)
	headers.Set(HeaderKeyID, keyID)
	headers.Set(HeaderTimestamp, ts)
	headers.Set(HeaderNonce, nonce)
	headers.Set(HeaderSignature, signature(method, target, connection, keyID, ts, nonce, secret, body))
}
func VerifySignature(headers http.Header, method, target, secret string, body []byte, now time.Time) bool {
	timestamp := headers.Get(HeaderTimestamp)
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	delta := now.Unix() - unix
	if delta < -300 || delta > 300 {
		return false
	}
	nonce, err := hex.DecodeString(headers.Get(HeaderNonce))
	if err != nil || len(nonce) < 16 || len(nonce) > 64 {
		return false
	}
	if canonicalTarget(target) == "" || headers.Get(HeaderConnection) == "" || headers.Get(HeaderKeyID) == "" {
		return false
	}
	got, err := hex.DecodeString(headers.Get(HeaderSignature))
	if err != nil {
		return false
	}
	expected, _ := hex.DecodeString(signature(method, target, headers.Get(HeaderConnection), headers.Get(HeaderKeyID), timestamp, headers.Get(HeaderNonce), secret, body))
	return hmac.Equal(got, expected)
}
