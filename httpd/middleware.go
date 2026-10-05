package httpd

import (
	"compress/gzip"
	"net/http"
	"strings"
	"time"

	"webtyp.com/router"
)

// lazyGzipWriter decides on the FIRST write whether to compress.
//
// It cannot decide earlier: the handler runs after the middleware, and a handler
// may encode the body itself (webtyp/client gzips the wasm binary and declares
// Content-Encoding). Compressing on top of that ships gzip(gzip(body)); the browser
// decompresses one layer, finds gzip magic (1f 8b 08 00) where the wasm magic word
// should be, and the app never starts. Encoding an already-encoded body is always a
// bug, so the transport refuses to do it — whatever the handler is.
type lazyGzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
}

func (w *lazyGzipWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	if w.Header().Get("Content-Encoding") != "" {
		return // the handler already encoded the body: pass it through untouched
	}
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		return // SSE must not be gzipped: causes buffering, delays, and HTTP/2 stream errors
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.gz = gzip.NewWriter(w.ResponseWriter)
}

func (w *lazyGzipWriter) WriteHeader(code int) {
	w.decide()
	w.ResponseWriter.WriteHeader(code)
}

func (w *lazyGzipWriter) Write(b []byte) (int, error) {
	w.decide()
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *lazyGzipWriter) Close() {
	if w.gz != nil {
		w.gz.Close()
	}
}

func (w *lazyGzipWriter) Flush() {
	if w.gz != nil {
		w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *lazyGzipWriter) SetWriteDeadline(deadline time.Time) error {
	return http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
}

func (w *lazyGzipWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Gzip middleware
func Gzip(next router.HandlerFunc) router.HandlerFunc {
	return func(ctx router.Context) {
		if !strings.Contains(ctx.GetHeader("Accept-Encoding"), "gzip") {
			next(ctx)
			return
		}

		// Access underlying http.ResponseWriter if possible
		hctx, ok := ctx.(*httpContext)
		if !ok {
			next(ctx)
			return
		}

		originalW := hctx.w
		lw := &lazyGzipWriter{ResponseWriter: originalW}
		hctx.w = lw
		defer func() {
			lw.Close()
			hctx.w = originalW
		}()

		next(ctx)
	}
}

// NoCache middleware
func NoCache(next router.HandlerFunc) router.HandlerFunc {
	return func(ctx router.Context) {
		ctx.SetHeader(headerCacheControl, "no-store, no-cache, must-revalidate, max-age=0")
		ctx.SetHeader("Pragma", "no-cache")
		ctx.SetHeader("Expires", "0")
		next(ctx)
	}
}

const headerCacheControl = "Cache-Control"
