package auth

import (
	"encoding/json"
	"log"
	"net/http"
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
			w.Header().Set("Content-Type", "application/json")
			if key == "" {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "SERVICE_KEY_MISSING"})
				log.Printf("Auth: rejected request (path=%s, reason=missing key)", r.URL.Path)
				return
			}
			if !validator.Validate(key) {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "SERVICE_KEY_INVALID"})
				log.Printf("Auth: rejected request (path=%s, reason=invalid key)", r.URL.Path)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
