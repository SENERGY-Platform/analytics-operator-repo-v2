/*
 * Copyright 2025 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/service"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	"github.com/SENERGY-Platform/go-service-base/struct-logger/attributes"

	gin_mw "github.com/SENERGY-Platform/gin-middleware"
	"github.com/SENERGY-Platform/gin-middleware/otelx"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
)

// New godoc
// @title Analytics-Operator-Repo-V2 API
// @version {version}
// @description For the administration of analytics operators.
// @license.name Apache-2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html
// @BasePath /
func New(ctx context.Context, srv service.Service, urlPrefix string, otelEndpoint string) (*gin.Engine, error) {
	// Idempotent: InitOpenTelemetry has already run this in main, and the setup
	// behind it happens once per process. The handler comes back either way.
	otelHandler, err := otelx.GinOpenTelemetry(ctx, ServiceName, otelEndpoint)
	if err != nil {
		return nil, fmt.Errorf("set up OpenTelemetry: %w", err)
	}
	gin.SetMode(gin.ReleaseMode)
	httpHandler := gin.New()
	httpHandler.RedirectTrailingSlash = false
	httpHandler.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "DELETE", "OPTIONS", "PUT"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))
	var middleware []gin.HandlerFunc
	middleware = append(
		middleware,
		// First in the chain: it lifts the trace context and the baggage off the
		// request into the request context, which the access log, the handlers and
		// the permission calls below all read from.
		otelHandler,
		// Directly after it, and it has to stay there: see DiscardBaggageErrors.
		DiscardBaggageErrors(),
		gin_mw.StructLoggerHandlerWithDefaultGenerators(
			util.Logger.With(attributes.LogRecordTypeKey, attributes.HttpAccessLogRecordTypeVal),
			attributes.Provider,
			[]string{HealthCheckPath},
			nil,
		),
	)
	middleware = append(middleware,
		requestid.New(requestid.WithCustomHeaderStrKey(HeaderRequestID)),
		gin_mw.ErrorHandler(statusCode, ", "),
		gin_mw.StructRecoveryHandler(util.Logger, gin_mw.DefaultRecoveryFunc),
	)
	httpHandler.Use(middleware...)
	httpHandler.UseRawPath = true
	httpHandlerWithPrefix := httpHandler.Group(urlPrefix)
	setRoutes, err := routes.Set(srv, httpHandlerWithPrefix)
	if err != nil {
		return nil, err
	}
	for _, route := range setRoutes {
		util.Logger.Debug("http route", attributes.MethodKey, route[0], attributes.PathKey, route[1])
	}
	httpHandlerWithPrefix.Use(AuthMiddleware())
	setRoutes, err = routesAuth.Set(srv, httpHandlerWithPrefix)
	if err != nil {
		return nil, err
	}
	for _, route := range setRoutes {
		util.Logger.Debug("http route", attributes.MethodKey, route[0], attributes.PathKey, route[1])
	}
	return httpHandler, nil
}

// InitOpenTelemetry sets up the tracer provider and the propagator for the
// process. It belongs before anything that captures the global provider or makes
// an outgoing call — a mongo monitor built earlier would hold the no-op provider
// for good, and a startup call made earlier carries neither traceparent nor
// baggage. The setup runs once per process and the first caller owns its error:
// a later call returns nil without having initialised anything.
func InitOpenTelemetry(ctx context.Context, otelEndpoint string) error {
	if _, err := otelx.GinOpenTelemetry(ctx, ServiceName, otelEndpoint); err != nil {
		return fmt.Errorf("set up OpenTelemetry: %w", err)
	}
	return nil
}

// DiscardBaggageErrors takes the errors the OpenTelemetry handler reported off the
// request and logs them instead.
//
// otelx reports a baggage value it cannot carry — one holding a space, a comma or
// a non-ASCII character — with gin's c.Error. ErrorHandler then turns anything in
// c.Errors into a response: it forces a 500 where the status was below 400 and
// appends the error text to the body. A user whose Keycloak username is a display
// name would get a 500 on every DELETE and a corrupted JSON body on every GET, for
// a log annotation that failed.
//
// This handler has to sit immediately after the OpenTelemetry handler. otelx adds
// those errors before it calls c.Next(), so at this point nothing else can have
// added one, which is what makes clearing the slice safe. Moved further down, it
// would discard a handler's own error.
func DiscardBaggageErrors() gin.HandlerFunc {
	return func(gc *gin.Context) {
		if len(gc.Errors) > 0 {
			for _, reported := range gc.Errors {
				util.Logger.WarnContext(gc.Request.Context(),
					"could not put a value into the request baggage", "error", reported.Err)
			}
			gc.Errors = nil
		}
		gc.Next()
	}
}

// statusCode maps the sentinels from lib onto HTTP statuses. Anything it does
// not recognise stays a 500, which is what ErrorHandler defaults to.
func statusCode(err error) int {
	switch {
	case errors.Is(err, lib.ErrInvalidInput):
		return http.StatusBadRequest
	case errors.Is(err, lib.ErrMissingRights):
		return http.StatusForbidden
	case errors.Is(err, lib.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// logError picks the level from the status the error maps to. A 4xx is the
// caller's own mistake and nothing this service can act on; logging it at ERROR
// buries the entries that mean the service itself is broken.
func logError(ctx context.Context, msg string, err error) {
	if statusCode(err) < http.StatusInternalServerError {
		util.Logger.WarnContext(ctx, msg, "error", err)
		return
	}
	util.Logger.ErrorContext(ctx, msg, "error", err)
}

// safeError decides what the caller gets to read. ErrorHandler writes the error
// text into the response body, so only the sentinels we built ourselves may pass
// through; everything else could carry database or permission internals.
func safeError(err error) error {
	if errors.Is(err, lib.ErrInvalidInput) || errors.Is(err, lib.ErrMissingRights) || errors.Is(err, lib.ErrNotFound) {
		return err
	}
	return errors.New(MessageSomethingWrong)
}

func AuthMiddleware() gin.HandlerFunc {
	return func(gc *gin.Context) {
		userId, err := getUserId(gc)
		if err != nil {
			util.Logger.WarnContext(gc.Request.Context(), "could not get user id", "error", err)
			gc.String(http.StatusUnauthorized, MessageUnauthorized)
			gc.Abort()
			return
		}
		gc.Set(UserIdKey, userId)
		gc.Next()
	}
}

func getUserId(c *gin.Context) (userId string, err error) {
	forUser := c.Query("for_user")
	if forUser != "" {
		roles := strings.Split(c.GetHeader("X-User-Roles"), ", ")
		if slices.Contains[[]string](roles, "admin") {
			return forUser, nil
		}
	}

	userId = c.GetHeader("X-UserId")
	if userId == "" {
		if c.GetHeader(HeaderAuthorization) != "" {
			var claims jwt.Token
			claims, err = jwt.Parse(c.GetHeader(HeaderAuthorization))
			if err != nil {
				return
			}
			userId = claims.Sub
		} else {
			err = errors.New("missing authorization and x-userid header")
		}
	}
	return
}
