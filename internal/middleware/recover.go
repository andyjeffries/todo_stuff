package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover catches panics in downstream handlers and hands them off to the
// supplied renderFn (typically Handlers.InternalError) so the user sees the
// same friendly 500 page as any other server-side failure. Replaces chi's
// middleware.Recoverer, which renders a plain-text "Internal Server Error".
//
// We log the panic + stack trace at error level. http.ErrAbortHandler is
// the special "abort, don't recover" sentinel that the stdlib uses for
// hijacked / streamed connections — re-panic so the runtime tears the
// connection down cleanly.
func Recover(renderFn http.HandlerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				slog.Error("panic in handler",
					"panic", rec,
					"path", r.URL.Path,
					"method", r.Method,
					"stack", string(debug.Stack()))
				renderFn(w, r)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
