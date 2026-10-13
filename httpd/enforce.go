package httpd

import (
	"fmt"

	"webtyp.com/model"
	"webtyp.com/router"
)

func (s *Server) validateRBAC() error {
	for _, r := range s.router.routes {
		if r.info.MFA && r.info.Access == model.AccessPublic {
			return fmt.Errorf("route %s %s %s", r.info.Method, r.info.Path, router.ErrMsgPublicRequiresMFA)
		}

		// AccessGuarded without Resource
		if r.info.Access == model.AccessGuarded && r.info.Resource == "" {
			return fmt.Errorf("route %s %s is guarded but has no resource defined", r.info.Method, r.info.Path)
		}

		// AccessGuarded without Authorize configured
		if r.info.Access == model.AccessGuarded && s.config.Authorize == nil {
			return fmt.Errorf("route %s %s requires resource %q but no Authorize is configured", r.info.Method, r.info.Path, r.info.Resource)
		}

		// Not AccessGuarded but has a Resource
		if r.info.Access != model.AccessGuarded && r.info.Resource != "" {
			return fmt.Errorf("route %s %s has a resource defined but is not guarded (access: %v)", r.info.Method, r.info.Path, r.info.Access)
		}
	}
	return nil
}
