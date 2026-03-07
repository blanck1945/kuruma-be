package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"flota/internal/config"
	"flota/internal/core"
)

func Auth(cfg config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Permite preflight CORS sin exigir credenciales.
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			path := r.URL.Path
			if strings.HasPrefix(path, "/v1/internal/") {
				token := bearerToken(r.Header.Get("Authorization"))
				if token == "" {
					writeAuthError(w, r, core.ErrUnauthorized, "missing bearer token")
					return
				}
				claims, err := parseHS256JWT(token, cfg.InternalJWTSecret)
				if err != nil {
					writeAuthError(w, r, core.ErrUnauthorized, "invalid jwt")
					return
				}
				scope, _ := claims["scope"].(string)
				if !strings.Contains(scope, "fines:read") {
					writeAuthError(w, r, core.ErrForbidden, "missing fines:read scope")
					return
				}
				client, _ := claims["sub"].(string)
				ctx := SetAuth(r.Context(), "internal", client)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			if strings.HasPrefix(path, "/v1/external/") {
				apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
				if apiKey == "" {
					writeAuthError(w, r, core.ErrUnauthorized, "missing api key")
					return
				}

				for client, secret := range cfg.ExternalAPIKeys {
					if secret == apiKey {
						ctx := SetAuth(r.Context(), "external", client)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
				writeAuthError(w, r, core.ErrForbidden, "invalid api key")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ParseHS256JWT validates and decodes an HS256 JWT. Exported for use in handlers.
func ParseHS256JWT(token, secret string) (map[string]any, error) {
	return parseHS256JWT(token, secret)
}

// GenerateHS256JWT creates a signed HS256 JWT with the given claims.
func GenerateHS256JWT(claims map[string]any, secret string) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	unsigned := header + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return unsigned + "." + sig, nil
}

func parseHS256JWT(token, secret string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, core.DomainError{Code: core.ErrUnauthorized, Message: "invalid token format"}
	}
	unsigned := parts[0] + "." + parts[1]

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, core.DomainError{Code: core.ErrUnauthorized, Message: "invalid signature"}
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, core.DomainError{Code: core.ErrUnauthorized, Message: "invalid payload"}
	}
	claims := map[string]any{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, core.DomainError{Code: core.ErrUnauthorized, Message: "invalid claims"}
	}
	return claims, nil
}

func bearerToken(authHeader string) string {
	authHeader = strings.TrimSpace(authHeader)
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

