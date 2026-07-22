package serviceauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	HeaderName = "X-Dbrain-Service-Auth"
	version    = "v1"
)

const maxClockSkew = 2 * time.Minute

type ReplayVerifier struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

type verifiedHeader struct {
	nonce     string
	expiresAt time.Time
}

// SignHeader returns a short-lived HMAC header for local CLI-to-service probes.
func SignHeader(method string, requestPath string, secret string, now time.Time) (string, error) {
	method = canonicalMethod(method)
	requestPath = strings.TrimSpace(requestPath)
	secret = strings.TrimSpace(secret)
	if method == "" {
		return "", fmt.Errorf("method is required")
	}
	if requestPath == "" {
		return "", fmt.Errorf("request path is required")
	}
	if secret == "" {
		return "", fmt.Errorf("secret is required")
	}
	var nonceBytes [16]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return "", fmt.Errorf("generate service auth nonce: %w", err)
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes[:])
	timestamp := now.Unix()
	sig := sign(method, requestPath, timestamp, nonce, secret)
	return strings.Join([]string{
		version,
		strconv.FormatInt(timestamp, 10),
		nonce,
		base64.RawURLEncoding.EncodeToString(sig),
	}, ":"), nil
}

func VerifyHeader(method string, requestPath string, secret string, headerValue string, now time.Time) bool {
	_, ok := verifyHeader(method, requestPath, secret, headerValue, now)
	return ok
}

func (v *ReplayVerifier) VerifyAndConsume(method string, requestPath string, secret string, headerValue string, now time.Time) bool {
	verified, ok := verifyHeader(method, requestPath, secret, headerValue, now)
	if !ok || v == nil {
		return false
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.seen == nil {
		v.seen = make(map[string]time.Time)
	}
	for nonce, expiresAt := range v.seen {
		if now.After(expiresAt) {
			delete(v.seen, nonce)
		}
	}
	if _, exists := v.seen[verified.nonce]; exists {
		return false
	}
	v.seen[verified.nonce] = verified.expiresAt
	return true
}

func verifyHeader(method string, requestPath string, secret string, headerValue string, now time.Time) (verifiedHeader, bool) {
	method = canonicalMethod(method)
	requestPath = strings.TrimSpace(requestPath)
	secret = strings.TrimSpace(secret)
	headerValue = strings.TrimSpace(headerValue)
	if method == "" || requestPath == "" || secret == "" || headerValue == "" {
		return verifiedHeader{}, false
	}
	parts := strings.Split(headerValue, ":")
	if len(parts) != 4 || parts[0] != version {
		return verifiedHeader{}, false
	}
	timestamp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return verifiedHeader{}, false
	}
	requestTime := time.Unix(timestamp, 0)
	if now.Sub(requestTime) > maxClockSkew || requestTime.Sub(now) > maxClockSkew {
		return verifiedHeader{}, false
	}
	nonce := parts[2]
	if nonce == "" {
		return verifiedHeader{}, false
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return verifiedHeader{}, false
	}
	want := sign(method, requestPath, timestamp, nonce, secret)
	if !hmac.Equal(got, want) {
		return verifiedHeader{}, false
	}
	return verifiedHeader{nonce: nonce, expiresAt: requestTime.Add(maxClockSkew)}, true
}

func sign(method string, requestPath string, timestamp int64, nonce string, secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(method))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(requestPath))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(nonce))
	return mac.Sum(nil)
}

func canonicalMethod(method string) string {
	return strings.ToUpper(strings.TrimSpace(method))
}
