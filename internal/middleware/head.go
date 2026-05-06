package middleware

import "net/http"

// AllowHead routes HEAD requests through the matching GET handler. Per RFC
// 9110 the body is discarded; status and headers are preserved. This is what
// most users expect when they hit a route with `curl -I`.
func AllowHead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.Method = http.MethodGet
		next.ServeHTTP(headBodyDiscarder{ResponseWriter: w}, r2)
	})
}

type headBodyDiscarder struct{ http.ResponseWriter }

func (h headBodyDiscarder) Write(b []byte) (int, error) { return len(b), nil }
