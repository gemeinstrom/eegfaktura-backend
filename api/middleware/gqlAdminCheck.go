package middleware

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/sirupsen/logrus"
)

// F1, step 1 (log only): GQLProtect marks requests whose token has no
// EEG_ADMIN group; GQLAdminCheckLog writes one INFO line per GraphQL
// operation of such a request. Nothing is blocked; enforcing the role
// follows after the log period.

var gqlNonAdminCtxKey = &contextKey{"gql-non-admin"}

// gqlNonAdmin holds the token data that is logged. No user id (sub), no
// e-mail, no username, no token (no user tracking, 2026-10-29).
type gqlNonAdmin struct {
	azp         string
	accessClass string
	superuser   bool
}

func withGQLNonAdmin(ctx context.Context, claims *PlatformClaims, superuser bool) context.Context {
	return context.WithValue(ctx, gqlNonAdminCtxKey, &gqlNonAdmin{
		azp:         claims.Authorized,
		accessClass: accessClass(claims.AccessGroups),
		superuser:   superuser,
	})
}

// IsGQLAdmin reports whether the GraphQL request passed GQLProtect with an EEG_ADMIN token. For the resolvers that
// require the admin group like their REST counterpart (Protect), while other operations stay in log-only mode. A
// context that did not pass GQLProtect has no tenant: the resolvers refuse it before.
func IsGQLAdmin(ctx context.Context) bool {
	_, nonAdmin := ctx.Value(gqlNonAdminCtxKey).(*gqlNonAdmin)
	return !nonAdmin
}

// accessClass is the role class of the access groups: admin, user or none.
func accessClass(ag AccessGroups) string {
	switch {
	case ag.IsAdmin():
		return "admin"
	case ag.IsUser():
		return "user"
	}
	return "none"
}

// GQLAdminCheckLog is a gqlgen operation middleware (handler.Server.AroundOperations).
//
// TODO(F1 step 2): temporary log; remove this hook and the context marker when
// /query enforces the admin role.
func GQLAdminCheckLog(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	if m, ok := ctx.Value(gqlNonAdminCtxKey).(*gqlNonAdmin); ok {
		fields := logrus.Fields{
			"tenant":              ForContextTenant(ctx),
			"client.id":           m.azp,
			"access_groups.class": m.accessClass,
			"superuser":           m.superuser,
		}
		if graphql.HasOperationContext(ctx) {
			if op := graphql.GetOperationContext(ctx).Operation; op != nil {
				fields["operation_type"] = string(op.Operation)
				fields["graphql.operation.name"] = op.Name
			}
		}
		logrus.WithContext(ctx).WithFields(fields).Info("gql-admin-check: non-admin token")
	}
	return next(ctx)
}
