package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	"github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/config"
)

type Enforcer struct {
	*casbin.Enforcer
}

type ctx int

const EnforcerContext ctx = 60

// var enforcer *casbin.Enforcer

func New() *Enforcer {
	roles := make(map[string][]string)
	roles["anonymous"] = []string{
		"default", "login",
	}
	roles["user"] = []string{
		"api", "default",
	}

	modelString := `
    [request_definition]
    r = sub, obj, act

    [policy_definition]
    p = sub, obj, act, eft

    [role_definition]
    g = _, _

    [policy_effect]
    # must have an allow and no deny statements that match
    e = some(where (p.eft == allow)) && !some(where (p.eft == deny))

    [matchers]
    m = g(r.sub, p.sub) && globMatch(r.obj, p.obj) && (p.act == '*' || regexMatch(r.act, p.act))
  `

	policyString := `
    # p, role, path, method, action
    # p = policy
    # not logged in = anonymous
    p, default, /, GET, allow
    p, default, /ca, GET, allow
    p, default, /favicon.ico, GET, allow

    # error pages
    p, default, /403, GET, allow
    p, default, /404, GET, allow

    p, login, /login, GET, allow
    p, login, /login/oauth, GET, allow

    # Client
    p, default, /api/v1/ca, GET, allow
    p, default, /api/v1/certificate, GET, allow
    p, default, /api/v1/signreq, POST, allow

    # Browser GUI
    p, user, /approve/*, GET, allow
  `

	modelFromString, err := model.NewModelFromString(modelString)
	if err != nil {
		slog.Error("failed to load model", slog.Any("error", err))
	}
	enforcer, err := casbin.NewEnforcer(modelFromString)
	if err != nil {
		slog.Error("failed to enforcer model", slog.Any("error", err))
	}
	loadPolicy(enforcer, policyString, roles)

	return &Enforcer{enforcer}
}

func (e *Enforcer) CheckAccessHandler(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		user := middleware.GetUserName(r)

		method := r.Method
		path := r.URL.Path

		canAccess, _ := e.Enforce(user, path, method)
		if canAccess {
			next.ServeHTTP(w, r)
		} else {
			middleware.GetSession(r).Put(r.Context(), "prelogin", r.URL.Path)
			// slog.Error("CASBIN", slog.String("username", user),
			// 	slog.String("method", method), slog.String("path", path))
			http.Redirect(w, r, "/login", http.StatusFound)
		}
	}

	return http.HandlerFunc(fn)

}

func loadPolicy(enforcer *casbin.Enforcer, p string, roles config.RoleInfo) {
	m := enforcer.GetModel()

	strs := strings.Split(p, "\n")
	for _, line := range strs {
		l := strings.TrimSpace(line)
		if line == "" {
			continue
		}
		_ = persist.LoadPolicyLine(l, m)
	}

	for source, v := range roles {
		_, _ = enforcer.AddRolesForUser(source, v)
	}
}

func (e *Enforcer) GetRoles(r *http.Request) []string {
	roles, _ := e.GetRolesForUser(middleware.GetUserName(r))

	return roles
}

func (e *Enforcer) AddRoles(r *http.Request, user string, roles []string) {
	_, _ = e.AddRolesForUser(user, roles)
}

func (e *Enforcer) RegisterHandler(next http.Handler) http.Handler {
	// m.LoadAndSave(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = context.WithValue(ctx, EnforcerContext, e)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func GetEnforcerFromContext(r *http.Request) *Enforcer {
	return r.Context().Value(EnforcerContext).(*Enforcer)
}
