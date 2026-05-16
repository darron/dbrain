package serviceauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderName = "X-Dbrain-Service-Auth"
	version    = "v1"
)

const maxClockSkew = 2 * time.Minute

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
	method = canonicalMethod(method)
	requestPath = strings.TrimSpace(requestPath)
	secret = strings.TrimSpace(secret)
	headerValue = strings.TrimSpace(headerValue)
	if method == "" || requestPath == "" || secret == "" || headerValue == "" {
		return false
	}
	parts := strings.Split(headerValue, ":")
	if len(parts) != 4 || parts[0] != version {
		return false
	}
	timestamp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	requestTime := time.Unix(timestamp, 0)
	if now.Sub(requestTime) > maxClockSkew || requestTime.Sub(now) > maxClockSkew {
		return false
	}
	nonce := parts[2]
	if nonce == "" {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want := sign(method, requestPath, timestamp, nonce, secret)
	return hmac.Equal(got, want)
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
