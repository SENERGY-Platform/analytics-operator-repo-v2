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
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/service"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	"github.com/SENERGY-Platform/go-service-base/struct-logger/attributes"

	gin_mw "github.com/SENERGY-Platform/gin-middleware"
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
func New(srv service.Service, urlPrefix string) (*gin.Engine, error) {
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
			util.Logger.Error("could not get user id")
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
