package httpd

import (
	"net/http"
	"time"

	"webtyp.com/model"
	"webtyp.com/router"
)

const (
	DefaultPort      = "8080"
	DefaultPublicDir = "public"
	HealthPath       = "/health"
)

type Config struct {
	Port         string
	InternetPort string // the internet door (e.g. behind Cloudflare Tunnel); "" = no internet door
	PublicDir    string // "" = no servir estáticos
	Gzip         bool
	NoCache      bool
	Health       bool
	Logger       func(...any)

	Authn          router.Middleware
	Authorize      model.Authorizer
	RoutesEndpoint bool

	// Policy lets the introspection endpoint report WHICH roles hold the
	// permission each route requires. Optional: nil leaves those columns
	// marked unknown, which is not the same as "nobody" — a permission no
	// role holds turns a correctly-declared route into a permanent 403, and
	// that finding must never be confused with "the app did not say".
	//
	// It is separate from Authorize because the two answer opposite
	// questions: Authorize answers "may this user do this?", Policy answers
	// "who may do this?". A closure cannot answer the second.
	Policy model.PolicyDescriber

	TLS TLSConfig
}

type Server struct {
	config                Config
	mux                   *http.ServeMux
	router                *httpRouter
	routesEndpointMounted bool
	localCert             *localCert
	baseHandler           http.Handler
	baseErr               error
}

func applyDefaults(c Config) Config {
	if c.Port == "" {
		c.Port = DefaultPort
	}
	return c
}

func New(c Config) *Server {
	c = applyDefaults(c)
	mux := http.NewServeMux()
	r := NewRouter(mux).(*httpRouter)

	s := &Server{
		config: c,
		mux:    mux,
		router: r,
	}
	s.localCert = &localCert{logf: s.log}

	if c.TLS.mode() == tlsLocal {
		r.PublicAsset(CAPath, serveLocalCA)
	}

	return s
}

func (s *Server) Router() router.Router {
	return s.router
}

func (s *Server) Mount(m ...router.APIModule) *Server {
	for _, module := range m {
		module.MountAPI(s.router)
	}
	return s
}

func (s *Server) Handler() (http.Handler, error) {
	b, err := s.base()
	if err != nil {
		return nil, err
	}
	return markNetwork(b, router.NetworkLocal), nil
}

// InternetHandler returns the handler for the internet door; the same routes as
// Handler, marked as coming from the internet.
func (s *Server) InternetHandler() (http.Handler, error) {
	b, err := s.base()
	if err != nil {
		return nil, err
	}
	return markNetwork(b, router.NetworkInternet), nil
}

func (s *Server) base() (http.Handler, error) {
	if s.baseHandler != nil || s.baseErr != nil {
		return s.baseHandler, s.baseErr
	}

	s.registerRoutesEndpoint()

	s.mux = http.NewServeMux()
	s.router.mux = s.mux

	if s.config.Health {
		s.mux.HandleFunc(pattern(router.RouteInfo{Method: http.MethodGet, Path: HealthPath}), func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})
	}

	// Re-register all existing routes in the new mux
	s.router.mu.RLock()
	routes := make([]*httpRoute, len(s.router.routes))
	copy(routes, s.router.routes)
	s.router.mu.RUnlock()

	for _, route := range routes {
		s.router.reRegister(route)
	}

	if err := s.validateRBAC(); err != nil {
		s.baseErr = err
		return nil, err
	}

	// Set authorizer for the router to use in closures
	s.router.setAuthorizer(s.config.Authn, s.config.Authorize)

	if err := s.validateTLS(); err != nil {
		s.baseErr = err
		return nil, err
	}

	var handler http.Handler = s.mux

	if s.config.PublicDir != "" {
		s.router.PublicDir("/", s.config.PublicDir)
	}

	// Apply fallback for PublicDir and global batteries
	handler = s.wrapWithBatteries(handler)
	handler = s.wrapWithGlobalBatteries(handler)

	s.baseHandler = handler
	return handler, nil
}
func (s *Server) ListenAndServe() error {
	local, err := s.Handler()
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:         ":" + s.config.Port,
		Handler:      local,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if s.config.Logger != nil {
		s.config.Logger("Starting server on port", s.config.Port)
	}

	if s.config.InternetPort == "" {
		return s.listenAndServe(srv)
	}

	internet, err := s.InternetHandler()
	if err != nil {
		return err
	}

	internetSrv := &http.Server{
		Addr:         ":" + s.config.InternetPort,
		Handler:      internet,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if s.config.Logger != nil {
		s.config.Logger("Starting internet door on port", s.config.InternetPort)
	}

	errs := make(chan error, 2)
	go func() { errs <- s.listenAndServe(srv) }()
	go func() { errs <- s.listenAndServe(internetSrv) }()
	return <-errs
}
func (s *Server) log(args ...any) {
	if s.config.Logger != nil {
		s.config.Logger(args...)
	}
}
