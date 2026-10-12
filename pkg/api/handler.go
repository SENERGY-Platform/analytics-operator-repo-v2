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
	"os"
	"strconv"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/service"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	"github.com/gin-gonic/gin"
)

// getAll godoc
// @Summary Get operators
// @Description	Gets all operators
// @Tags Operator
// @Produce json
// @Param limit query int false "Maximum number of operators to return, at most 1000; 0 means no limit" default(1000)
// @Param offset query int false "Number of operators to skip"
// @Param sort query string false "Sort as field[:asc|desc]; only name is sortable" example(name:desc)
// @Param search query string false "Case-sensitive substring match on name; treated literally"
// @Success	200 {object} lib.OperatorResponse
// @Failure	400 {string} str
// @Failure	500 {string} str
// @Router /operator [get]
func getAll(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodGet, "/operator", func(gc *gin.Context) {
		args := gc.Request.URL.Query()
		operators, err := srv.GetOperators(gc.Request.Context(), gc.GetString(UserIdKey), args, gc.GetHeader("Authorization"))
		if err != nil {
			logError(gc.Request.Context(), "error getting operators", err)
			_ = gc.Error(safeError(err))
			return
		}
		gc.JSON(http.StatusOK, operators)
	}
}

// getOperator godoc
// @Summary Get operator
// @Description	Gets a single operator
// @Tags Operator
// @Produce json
// @Param id path string true "Operator ID"
// @Success	200 {object} lib.Operator
// @Failure	400 {string} str
// @Failure	403 {string} str
// @Failure	404 {string} str
// @Failure	500 {string} str
// @Router /operator/{id} [get]
func getOperator(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodGet, "/operator/:id", func(gc *gin.Context) {
		resp, err := srv.GetOperator(gc.Request.Context(), gc.Param("id"), gc.GetHeader("Authorization"))
		if err != nil {
			logError(gc.Request.Context(), "error getting operator", err)
			_ = gc.Error(safeError(err))
			return
		}
		gc.JSON(http.StatusOK, resp)
	}
}

// putOperator godoc
// @Summary Create operator
// @Description	Stores an operator. The versions field of the body is ignored; versions are stored through /operator/{id}/versions/{version}.
// @Tags Operator
// @Param operator body lib.Operator true "Create operator"
// @Accept json
// @Success	201
// @Failure	400 {string} str
// @Failure	500 {string} str
// @Router /operator/ [put]
func putOperator(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodPut, "/operator/", func(gc *gin.Context) {
		var request lib.Operator
		if err := gc.ShouldBindJSON(&request); err != nil {
			util.Logger.WarnContext(gc.Request.Context(), "error creating operator", "error", err)
			_ = gc.Error(fmt.Errorf("%w: malformed request body", lib.ErrInvalidInput))
			return
		}
		err := srv.CreateOperator(gc.Request.Context(), request, gc.GetString(UserIdKey))
		if err != nil {
			logError(gc.Request.Context(), "error creating operator", err)
			_ = gc.Error(safeError(err))
			return
		}
		gc.Status(http.StatusCreated)
	}
}

// postOperator godoc
// @Summary Update operator
// @Description	Validates and updates an operator. For an operator with versions, image, inputs, outputs and config_values must equal those of the newest version or be empty; they are always taken from the newest version, and a different value is refused with 400. The versions field of the body is ignored. Answers 409 when the operator was changed concurrently; the request can be repeated.
// @Tags Operator
// @Accept json
// @Param id path string true "Operator ID"
// @Param operator body lib.Operator true "Update operator"
// @Success	200
// @Failure	400 {string} str
// @Failure	403 {string} str
// @Failure	404 {string} str
// @Failure	409 {string} str
// @Failure	500 {string} str
// @Router /operator/{id} [post]
func postOperator(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodPost, "/operator/:id/", postOperatorHandler(srv)
}

// postOperatorAlias registers the path documented for postOperator. The
// trailing-slash variant stays registered because released clients call it.
func postOperatorAlias(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodPost, "/operator/:id", postOperatorHandler(srv)
}

func postOperatorHandler(srv service.Service) gin.HandlerFunc {
	return func(gc *gin.Context) {
		var request lib.Operator
		if err := gc.ShouldBindJSON(&request); err != nil {
			util.Logger.WarnContext(gc.Request.Context(), "error updating operator", "error", err)
			_ = gc.Error(fmt.Errorf("%w: malformed request body", lib.ErrInvalidInput))
			return
		}
		err := srv.UpdateOperator(gc.Request.Context(), gc.Param("id"), request, gc.GetHeader("Authorization"))
		if err != nil {
			logError(gc.Request.Context(), "error updating operator", err)
			_ = gc.Error(safeError(err))
			return
		}
		gc.Status(http.StatusOK)
	}
}

// putOperatorVersion godoc
// @Summary Put operator version
// @Description	Adds a version to an operator or replaces the version with the same label; deprecating a version is the same call with deprecated set. The label is vMAJOR.MINOR.PATCH, the image is required and may belong to one version of the operator only. The top-level image, inputs, outputs and config_values of the operator follow the highest version that is not deprecated, or the highest overall when all are. The first version turns an operator without versions into one with versions. The label in the path wins; a different non-empty version in the body is refused with 400. Answers 201 when the label is new and 200 when it replaced a version, and 409 when the operator was changed concurrently; the request can be repeated.
// @Tags Operator
// @Accept json
// @Param id path string true "Operator ID"
// @Param version path string true "Version label, vMAJOR.MINOR.PATCH" example(v1.2.0)
// @Param operatorVersion body lib.OperatorVersion true "Operator version"
// @Success	200
// @Success	201
// @Failure	400 {string} str
// @Failure	403 {string} str
// @Failure	404 {string} str
// @Failure	409 {string} str
// @Failure	500 {string} str
// @Router /operator/{id}/versions/{version} [put]
func putOperatorVersion(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodPut, "/operator/:id/versions/:version", putOperatorVersionHandler(srv.PutOperatorVersion)
}

// versionPutter is the part of the service the version handler calls.
type versionPutter func(ctx context.Context, id string, version lib.OperatorVersion, auth string) (created bool, err error)

func putOperatorVersionHandler(put versionPutter) gin.HandlerFunc {
	return func(gc *gin.Context) {
		var request lib.OperatorVersion
		if err := gc.ShouldBindJSON(&request); err != nil {
			util.Logger.WarnContext(gc.Request.Context(), "error putting operator version", "error", err)
			_ = gc.Error(fmt.Errorf("%w: malformed request body", lib.ErrInvalidInput))
			return
		}
		label := gc.Param("version")
		if request.Version != "" && request.Version != label {
			err := fmt.Errorf("%w: version in the body differs from the one in the path", lib.ErrInvalidInput)
			logError(gc.Request.Context(), "error putting operator version", err)
			_ = gc.Error(err)
			return
		}
		request.Version = label
		created, err := put(gc.Request.Context(), gc.Param("id"), request, gc.GetHeader("Authorization"))
		if err != nil {
			logError(gc.Request.Context(), "error putting operator version", err)
			_ = gc.Error(safeError(err))
			return
		}
		if created {
			gc.Status(http.StatusCreated)
			return
		}
		gc.Status(http.StatusOK)
	}
}

// deleteOperator godoc
// @Summary Delete operator
// @Description	Deletes an operator. Refused with 409 while flows use it, unless an administrator passes force. Answers 502 when the flow repository cannot be asked; nothing is deleted then.
// @Tags Operator
// @Produce json
// @Param id path string true "Operator ID"
// @Param force query bool false "Administrators only: delete although flows still use the operator" default(false)
// @Success	204
// @Failure	400 {string} str
// @Failure	403 {string} str
// @Failure	404 {string} str
// @Failure	409 {object} lib.InUseResponse
// @Failure	500 {string} str
// @Failure	502 {string} str
// @Router /operator/{id} [delete]
func deleteOperator(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodDelete, "/operator/:id/", deleteOperatorHandler(srv)
}

// deleteOperatorAlias registers the path documented for deleteOperator. The
// trailing-slash variant stays registered because released clients call it.
func deleteOperatorAlias(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodDelete, "/operator/:id", deleteOperatorHandler(srv)
}

func deleteOperatorHandler(srv service.Service) gin.HandlerFunc {
	return func(gc *gin.Context) {
		opts, err := deleteOptions(gc)
		if err != nil {
			logError(gc.Request.Context(), "error deleting operator", err)
			_ = gc.Error(safeError(err))
			return
		}
		err = srv.DeleteOperator(gc.Request.Context(), gc.Param("id"), gc.GetHeader("Authorization"), opts)
		if err != nil {
			deleteFailed(gc, "error deleting operator", err, false)
			return
		}
		gc.Status(http.StatusNoContent)
	}
}

// deleteOptions reads ?force from the request. Force skips the check for flows that use the
// operator, so it is for administrators only.
func deleteOptions(gc *gin.Context) (service.DeleteOptions, error) {
	opts := service.DeleteOptions{UserId: gc.GetString(UserIdKey)}
	raw := gc.Query("force")
	if raw == "" {
		return opts, nil
	}
	force, err := strconv.ParseBool(raw)
	if err != nil {
		return opts, fmt.Errorf("%w: force must be true or false", lib.ErrInvalidInput)
	}
	if force && !isAdmin(gc) {
		return opts, fmt.Errorf("%w: force needs the admin role", lib.ErrMissingRights)
	}
	opts.Force = force
	return opts, nil
}

// deleteFailed answers a failed delete. A refusal because flows use the operators carries their
// usage in the body, which the error handler would reduce to text.
func deleteFailed(gc *gin.Context, msg string, err error, batch bool) {
	logError(gc.Request.Context(), msg, err)
	var inUse *lib.InUseError
	if !errors.As(err, &inUse) {
		_ = gc.Error(safeError(err))
		return
	}
	if batch {
		gc.JSON(http.StatusConflict, lib.InUseBatchResponse{Error: inUse.Error(), Operators: inUse.Operators})
		return
	}
	gc.JSON(http.StatusConflict, lib.InUseResponse{Error: inUse.Error(), OperatorUsage: inUse.Operators[0].OperatorUsage})
}

// deleteOperators godoc
// @Summary Delete multiple operators
// @Description	Deletes multiple operators, all or none. Refused with 409 naming each used operator while flows use any of them, unless an administrator passes force. Answers 502 when the flow repository cannot be asked; nothing is deleted then.
// @Tags Operator
// @Accept json
// @Produce json
// @Param request body []string true "ID list"
// @Param force query bool false "Administrators only: delete although flows still use operators of the list" default(false)
// @Success	204
// @Failure	400 {string} str
// @Failure	403 {string} str
// @Failure	404 {string} str
// @Failure	409 {object} lib.InUseBatchResponse
// @Failure	500 {string} str
// @Failure	502 {string} str
// @Router /operator [delete]
func deleteOperators(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodDelete, "/operator", func(gc *gin.Context) {
		var request []string
		if err := gc.ShouldBindJSON(&request); err != nil {
			util.Logger.WarnContext(gc.Request.Context(), "error deleting operators", "error", err)
			_ = gc.Error(fmt.Errorf("%w: malformed request body", lib.ErrInvalidInput))
			return
		}

		opts, err := deleteOptions(gc)
		if err != nil {
			logError(gc.Request.Context(), "error deleting operators", err)
			_ = gc.Error(safeError(err))
			return
		}
		err = srv.DeleteOperators(gc.Request.Context(), request, gc.GetHeader("Authorization"), opts)
		if err != nil {
			deleteFailed(gc, "error deleting operators", err, true)
			return
		}
		gc.Status(http.StatusNoContent)
	}
}

func getHealthCheckH(_ service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodGet, HealthCheckPath, func(c *gin.Context) {
		c.Status(http.StatusOK)
	}
}

func getSwaggerDocH(_ service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodGet, "/doc", func(gc *gin.Context) {
		if _, err := os.Stat("docs/swagger.json"); err != nil {
			_ = gc.Error(err)
			return
		}
		gc.Header("Content-Type", gin.MIMEJSON)
		gc.File("docs/swagger.json")
	}
}
