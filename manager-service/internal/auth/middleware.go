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
			if !validator.Validate(key) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				log.Printf("Auth: rejected request (path=%s, hasKey=%v)", r.URL.Path, key != "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
