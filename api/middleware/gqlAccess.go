package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"at.ourproject/vfeeg-backend/logging"
	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// GQLAccessOperation is a gqlgen operation middleware (handler.Server.AroundOperations):
// it puts the GraphQL operation name into the access line, since every
// GraphQL call is the one route /query.
func GQLAccessOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	if graphql.HasOperationContext(ctx) {
		if op := graphql.GetOperationContext(ctx).Operation; op != nil {
			logging.SetOperation(ctx, op.Name)
		}
	}
	return next(ctx)
}

// GQLRecover is the gqlgen recover function (handler.Server.SetRecoverFunc):
// a panic in a resolver is logged as one line through the logging module
// instead of gqlgen's plain-text stack on stderr. The client gets the same
// generic error as before.
func GQLRecover(ctx context.Context, err any) error {
	slog.Default().LogAttrs(ctx, slog.LevelError, "GraphQL resolver panicked",
		slog.String("event.action", "graphql.panic"),
		slog.String("error.type", fmt.Sprintf("%T", err)),
		slog.String("error.message", fmt.Sprint(err)),
		slog.String("error.stack_trace", string(debug.Stack())),
	)
	return gqlerror.Errorf("internal system error")
}
