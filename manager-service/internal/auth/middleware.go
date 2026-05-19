package auth

import (
	"log"
	"net/http"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/httperror"
)

// ServiceKeyMiddleware creates an HTTP middleware that validates the X-Service-Key header.
// If validation fails, returns 401 Unauthorized.
func ServiceKeyMiddleware(validator *ServiceKeyValidator, headerName string) func(http.Handler) http.Handler {
	if headerName == "" {
		headerName = "X-Service-Key"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(headerName)
			if key == "" {
				httperror.Write(w, r, http.StatusUnauthorized, "service_key_missing", "service key is required")
				log.Printf("Auth: rejected request (path=%s, reason=missing key)", r.URL.Path)
				return
			}
			if !validator.Validate(key) {
				httperror.Write(w, r, http.StatusUnauthorized, "service_key_invalid", "service key is invalid")
				log.Printf("Auth: rejected request (path=%s, reason=invalid key)", r.URL.Path)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
