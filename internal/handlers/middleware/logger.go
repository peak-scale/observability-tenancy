package middleware

import (
	"time"

	"github.com/go-logr/logr"
	"github.com/valyala/fasthttp"
)

// LoggerMiddleware logs incoming requests.
func LoggerMiddleware(log logr.Logger) func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			start := time.Now()
			method := string(ctx.Method())
			url := string(ctx.RequestURI())
			remote := ctx.RemoteAddr().String()

			// Log Headers
			ctx.Request.Header.VisitAllInOrder(func(key, value []byte) {
				log.V(5).Info("request header",
					"key", string(key),
					"value", string(value),
				)
			})

			// Call the next handler in the chain
			next(ctx)

			// Log after request is handled
			log.V(5).Info("request",
				"method", method,
				"url", url,
				"remote", remote,
				"duration", time.Since(start).String(),
				"status", ctx.Response.StatusCode(),
			)
		}
	}
}
