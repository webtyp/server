package httpd

import (
	"io"
	"net/http"
	"strings"
	"sync"

	"webtyp.com/json"
	"webtyp.com/model"
	"webtyp.com/router"
)

var (
	_ router.Context  = (*httpContext)(nil)
	_ router.Streamer = (*httpStreamer)(nil)
	_ router.Router   = (*httpRouter)(nil)
	_ router.Route    = (*httpRoute)(nil)
)

type httpContext struct {
	w      http.ResponseWriter
	r      *http.Request
	body   []byte
	once   sync.Once
	values map[string]string
	userID string
}

func (c *httpContext) Method() string {
	return c.r.Method
}

func (c *httpContext) Path() string {
	return c.r.URL.Path
}

// Param returns a path parameter the matched route declared with {name}.
//
// ServeMux extracts it: the route was registered as "GET /api/items/{id}",
// which the standard library has matched and populated since Go 1.22. This
// repo does not parse patterns — see docs/ARCHITECTURE.md.
func (c *httpContext) Param(name string) string {
	return c.r.PathValue(name)
}

func (c *httpContext) Body() []byte {
	c.once.Do(func() {
		if c.r.Body != nil {
			c.body, _ = io.ReadAll(c.r.Body)
		}
	})
	return c.body
}

func (c *httpContext) GetHeader(key string) string {
	return c.r.Header.Get(key)
}

func (c *httpContext) SetHeader(key, value string) {
	c.w.Header().Set(key, value)
}

func (c *httpContext) WriteStatus(code int) {
	c.w.WriteHeader(code)
}

func (c *httpContext) Write(b []byte) (int, error) {
	return c.w.Write(b)
}

func (c *httpContext) Decode(into model.Decodable) error {
	return json.Decode(c.Body(), into)
}

func (c *httpContext) Encode(v model.Encodable) error {
	var out []byte
	if err := json.Encode(v, &out); err != nil {
		return err
	}
	_, err := c.Write(out)
	return err
}

func (c *httpContext) SetValue(key, value string) {
	if c.values == nil {
		c.values = make(map[string]string)
	}
	c.values[key] = value
}

func (c *httpContext) Value(key string) string {
	if v, ok := c.values[key]; ok {
		return v
	}
	if key == router.ContextKeyRemoteAddr {
		return c.r.RemoteAddr
	}
	if v, ok := c.r.Context().Value(key).(string); ok {
		return v
	}
	return ""
}

func (c *httpContext) SetUserID(id string) {
	c.userID = id
}

func (c *httpContext) UserID() string {
	return c.userID
}

func (c *httpContext) SetCookie(cookie router.Cookie) {
	hc := &http.Cookie{
		Name:     cookie.Name,
		Value:    cookie.Value,
		Path:     cookie.Path,
		Domain:   cookie.Domain,
		MaxAge:   cookie.MaxAge,
		Secure:   cookie.Secure,
		HttpOnly: cookie.HttpOnly,
	}
	switch cookie.SameSite {
	case router.SameSiteLax:
		hc.SameSite = http.SameSiteLaxMode
	case router.SameSiteStrict:
		hc.SameSite = http.SameSiteStrictMode
	case router.SameSiteNone:
		hc.SameSite = http.SameSiteNoneMode
	default:
		hc.SameSite = http.SameSiteDefaultMode
	}
	http.SetCookie(c.w, hc)
}

func (c *httpContext) Cookie(name string) (router.Cookie, bool) {
	hc, err := c.r.Cookie(name)
	if err != nil {
		return router.Cookie{}, false
	}
	sameSite := router.SameSiteDefault
	switch hc.SameSite {
	case http.SameSiteLaxMode:
		sameSite = router.SameSiteLax
	case http.SameSiteStrictMode:
		sameSite = router.SameSiteStrict
	case http.SameSiteNoneMode:
		sameSite = router.SameSiteNone
	}
	return router.Cookie{
		Name:     hc.Name,
		Value:    hc.Value,
		Path:     hc.Path,
		Domain:   hc.Domain,
		MaxAge:   hc.MaxAge,
		Secure:   hc.Secure,
		HttpOnly: hc.HttpOnly,
		SameSite: sameSite,
	}, true
}

type httpStreamer struct {
	*httpContext
}

func (s *httpStreamer) Flush() {
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

type httpRouter struct {
	mux               *http.ServeMux
	mu                sync.RWMutex
	middlewares       []router.Middleware
	globalMiddlewares []router.Middleware
	routes            []*httpRoute

	// Authorizer info to be used by enforced RBAC
	authn      router.Middleware
	authorizer model.Authorizer
}

type httpRoute struct {
	info       router.RouteInfo
	h          router.HandlerFunc
	sh         router.StreamFunc
	mwAppliedH router.HandlerFunc
}

func (r *httpRoute) Requires(resource model.Resource, action model.Action) router.Route {
	r.info.Resource = resource
	r.info.Action = action
	r.info.Access = model.AccessGuarded
	return r
}

func (r *httpRoute) Authenticated() router.Route {
	r.info.Access = model.AccessAuthenticated
	return r
}

func (r *httpRoute) Public() router.Route {
	r.info.Access = model.AccessPublic
	return r
}

func (r *httpRoute) Accepts(args model.Fielder) router.Route {
	r.info.Args = args
	return r
}

func NewRouter(mux *http.ServeMux) router.Router {
	return &httpRouter{
		mux: mux,
	}
}

func (r *httpRouter) Use(m ...router.Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.middlewares = append(r.middlewares, m...)
}

func (r *httpRouter) useGlobal(m ...router.Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.globalMiddlewares = append(r.globalMiddlewares, m...)
}

func (r *httpRouter) setAuthorizer(authn router.Middleware, authorizer model.Authorizer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authn = authn
	r.authorizer = authorizer
}

func (r *httpRouter) Handle(method, path string, h router.HandlerFunc) router.Route {
	// Pattern validation: this server delegates pattern matching to net/http.ServeMux,
	// but uses router.ValidatePattern at registration so patterns rejected by edge runtimes
	// (e.g. {name...} trailing wildcards) are rejected early here as well.
	if err := router.ValidatePattern(path); err != nil {
		panic(err.Error())
	}

	route := &httpRoute{info: router.RouteInfo{Method: method, Path: path}, h: h}
	r.mu.Lock()
	r.routes = append(r.routes, route)
	r.mu.Unlock()

	r.register(route)
	return route
}

// pattern builds the ServeMux pattern for a route. The method MUST be part of it: registering
// by path alone makes two routes on the same path the SAME pattern, and ServeMux panics on the
// duplicate. That is not a corner case — it is what the router contract offers on every path
// (Get/Post/Options), and it panicked here while working fine on the edge implementation.
//
// ServeMux matches "GET /path" natively since Go 1.22, so it also emits the 405 itself. An
// empty method means "any method" and stays a bare path pattern; the contract allows it.
func pattern(info router.RouteInfo) string {
	if info.Method == "" {
		return info.Path
	}
	return info.Method + " " + info.Path
}

func (r *httpRouter) register(route *httpRoute) {
	r.mux.HandleFunc(pattern(route.info), func(w http.ResponseWriter, req *http.Request) {
		hctx := &httpContext{w: w, r: req}
		r.mu.RLock()
		if route.mwAppliedH == nil {
			r.mu.RUnlock()
			r.mu.Lock()
			if route.mwAppliedH == nil {
				hfunc := route.h
				if hfunc == nil && route.sh != nil {
					hfunc = func(ctx router.Context) {
						if s, ok := ctx.(*httpContext); ok {
							route.sh(&httpStreamer{httpContext: s})
						}
					}
				}
				route.mwAppliedH = r.applySecurityAndMiddleware(hfunc, route)
			}
			r.mu.Unlock()
			r.mu.RLock()
		}
		handler := route.mwAppliedH
		r.mu.RUnlock()

		handler(hctx)
	})
}

func (r *httpRouter) reRegister(route *httpRoute) {
	// Reset pre-applied middleware on re-registration (new mux)
	route.mwAppliedH = nil
	if route.h != nil {
		r.register(route)
	} else if route.sh != nil {
		r.registerStream(route)
	}
}

func (r *httpRouter) Get(path string, h router.HandlerFunc) router.Route {
	return r.Handle(http.MethodGet, path, h)
}

func (r *httpRouter) Post(path string, h router.HandlerFunc) router.Route {
	return r.Handle(http.MethodPost, path, h)
}

func (r *httpRouter) Put(path string, h router.HandlerFunc) router.Route {
	return r.Handle(http.MethodPut, path, h)
}

func (r *httpRouter) Delete(path string, h router.HandlerFunc) router.Route {
	return r.Handle(http.MethodDelete, path, h)
}

func (r *httpRouter) Options(path string, h router.HandlerFunc) router.Route {
	return r.Handle(http.MethodOptions, path, h)
}

func (r *httpRouter) Stream(path string, h router.StreamFunc) router.Route {
	if err := router.ValidatePattern(path); err != nil {
		panic(err.Error())
	}

	route := &httpRoute{info: router.RouteInfo{Method: http.MethodGet, Path: path}, sh: h}
	r.mu.Lock()
	r.routes = append(r.routes, route)
	r.mu.Unlock()

	r.registerStream(route)
	return route
}

func (r *httpRouter) PublicAsset(path string, h router.HandlerFunc) {
	if err := router.ValidatePattern(path); err != nil {
		panic(err.Error())
	}

	route := &httpRoute{
		info: router.RouteInfo{
			Method: http.MethodGet,
			Path:   path,
			Access: model.AccessPublic,
		},
		h: h,
	}
	r.mu.Lock()
	r.routes = append(r.routes, route)
	r.mu.Unlock()
	r.register(route)
}

func (r *httpRouter) PublicDir(prefix string, dir string) {
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if err := router.ValidatePattern(prefix); err != nil {
		panic(err.Error())
	}

	route := &httpRoute{
		info: router.RouteInfo{
			Method: http.MethodGet,
			Path:   prefix,
			Access: model.AccessPublic,
			Dir:    dir,
		},
	}
	r.mu.Lock()
	r.routes = append(r.routes, route)
	r.mu.Unlock()
	// PublicDir is NOT registered in the mux. It's handled as a fallback in wrapWithBatteries.
}

func (r *httpRouter) registerStream(route *httpRoute) {
	r.register(route)
}

func (r *httpRouter) applySecurityAndMiddleware(h router.HandlerFunc, route *httpRoute) router.HandlerFunc {
	// These are local copies because we are already inside a lock when calling this (from register)
	authn := r.authn
	authorizer := r.authorizer
	mws := r.middlewares
	gmws := r.globalMiddlewares

	wrapped := h
	if wrapped == nil {
		wrapped = func(ctx router.Context) {}
	}

	// 1. The consumer's middleware wraps the handler — BEHIND the gate (step 2). A rejected
	//    request must execute none of it: a Use that decodes the body or hits the DB would
	//    otherwise do that work for a caller who is about to get a 403. The gate is a gate.
	//
	//    The library's own batteries (log, gzip) are NOT here — they are globalMiddlewares,
	//    applied in step 3, outside the gate, so a rejection is still logged.
	for i := 0; i < len(mws); i++ {
		wrapped = mws[i](wrapped)
	}

	// 2. Apply RBAC around them.
	next := wrapped
	wrapped = func(ctx router.Context) {
		switch route.info.Access {
		case model.AccessPublic:
			next(ctx)

		case model.AccessAuthenticated:
			if ctx.UserID() == "" {
				ctx.WriteStatus(http.StatusForbidden)
				ctx.Write([]byte("Forbidden\n"))
				return
			}
			next(ctx)

		default: // model.AccessGuarded — el zero value: identidad Y permiso
			if ctx.UserID() == "" {
				ctx.WriteStatus(http.StatusForbidden)
				ctx.Write([]byte("Forbidden\n"))
				return
			}
			// model.Allowed deniega si Authorize es nil: la ausencia de respuesta no es permiso.
			if !model.Allowed(authorizer, ctx.UserID(), route.info.Resource, route.info.Action) {
				ctx.WriteStatus(http.StatusForbidden)
				ctx.Write([]byte("Forbidden\n"))
				return
			}
			next(ctx)
		}
	}

	// 3. The library's batteries (log, gzip, no-cache) sit OUTSIDE the gate on purpose: a
	//    rejected request must still be logged and compressed. They are the seam for anything
	//    that legitimately needs to observe a 403.
	for i := 0; i < len(gmws); i++ {
		wrapped = gmws[i](wrapped)
	}

	// 4. Authn is the outermost: identity must exist BEFORE the gate decides with it.
	if authn != nil {
		wrapped = authn(wrapped)
	}

	return wrapped
}

func (r *httpRouter) Socket(path string, h router.SocketFunc) router.Route {
	if err := router.ValidatePattern(path); err != nil {
		panic(err.Error())
	}

	route := &httpRoute{info: router.RouteInfo{Method: http.MethodGet, Path: path}}
	r.mu.Lock()
	r.routes = append(r.routes, route)
	r.mu.Unlock()

	r.mux.HandleFunc(pattern(route.info), func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "WebSocket not implemented in native adapter", http.StatusNotImplemented)
	})
	return route
}

func (r *httpRouter) Mount(prefix string, fn func(router.Router)) {
	if !strings.HasPrefix(prefix, "/") || (len(prefix) > 1 && strings.HasSuffix(prefix, "/")) {
		panic(router.ErrMsgMountPrefix)
	}
	fn(&prefixRouter{parent: r, prefix: prefix})
}

type prefixRouter struct {
	parent *httpRouter
	prefix string
}

func (pr *prefixRouter) subPath(p string) string {
	if p == "/" {
		return pr.prefix
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return pr.prefix + p
}

func (pr *prefixRouter) Get(path string, h router.HandlerFunc) router.Route {
	return pr.parent.Get(pr.subPath(path), h)
}

func (pr *prefixRouter) Post(path string, h router.HandlerFunc) router.Route {
	return pr.parent.Post(pr.subPath(path), h)
}

func (pr *prefixRouter) Put(path string, h router.HandlerFunc) router.Route {
	return pr.parent.Put(pr.subPath(path), h)
}

func (pr *prefixRouter) Delete(path string, h router.HandlerFunc) router.Route {
	return pr.parent.Delete(pr.subPath(path), h)
}

func (pr *prefixRouter) Options(path string, h router.HandlerFunc) router.Route {
	return pr.parent.Options(pr.subPath(path), h)
}

func (pr *prefixRouter) Handle(method, path string, h router.HandlerFunc) router.Route {
	return pr.parent.Handle(method, pr.subPath(path), h)
}

func (pr *prefixRouter) Stream(path string, h router.StreamFunc) router.Route {
	return pr.parent.Stream(pr.subPath(path), h)
}

func (pr *prefixRouter) Socket(path string, h router.SocketFunc) router.Route {
	return pr.parent.Socket(pr.subPath(path), h)
}

func (pr *prefixRouter) PublicAsset(path string, h router.HandlerFunc) {
	pr.parent.PublicAsset(pr.subPath(path), h)
}

func (pr *prefixRouter) PublicDir(prefix string, dir string) {
	pr.parent.PublicDir(pr.subPath(prefix), dir)
}

func (pr *prefixRouter) Mount(prefix string, fn func(router.Router)) {
	if !strings.HasPrefix(prefix, "/") || (len(prefix) > 1 && strings.HasSuffix(prefix, "/")) {
		panic(router.ErrMsgMountPrefix)
	}
	fn(&prefixRouter{parent: pr.parent, prefix: pr.subPath(prefix)})
}

func (pr *prefixRouter) Use(m ...router.Middleware) {
	pr.parent.Use(m...)
}

func (pr *prefixRouter) Routes() []router.RouteInfo {
	return pr.parent.Routes()
}

func (r *httpRouter) Routes() []router.RouteInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]router.RouteInfo, len(r.routes))
	for i, route := range r.routes {
		result[i] = route.info
	}
	return result
}
