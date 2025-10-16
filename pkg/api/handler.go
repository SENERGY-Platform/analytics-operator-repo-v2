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
	"os"

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
// @Success	200 {object} lib.OperatorResponse
// @Failure	500 {string} str
// @Router /operator [get]
func getAll(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodGet, "/operator", func(gc *gin.Context) {
		args := gc.Request.URL.Query()
		flows, err := srv.GetOperators(gc.GetString(UserIdKey), args, gc.GetHeader("Authorization"))
		if err != nil {
			util.Logger.Error("error getting operators", "error", err)
			_ = gc.Error(errors.New(MessageSomethingWrong))
			return
		}
		gc.JSON(http.StatusOK, flows)
	}
}

// getOperator godoc
// @Summary Get operator
// @Description	Gets a single operator
// @Tags Operator
// @Produce json
// @Param id path string true "Operator ID"
// @Success	200 {object} lib.Operator
// @Failure	500 {string} str
// @Router /operator/{id} [get]
func getOperator(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodGet, "/operator/:id", func(gc *gin.Context) {
		resp, err := srv.GetOperator(gc.Param("id"), gc.GetString(UserIdKey), gc.GetHeader("Authorization"))
		if err != nil {
			util.Logger.Error("error getting operator", "error", err)
			_ = gc.Error(errors.New(MessageSomethingWrong))
			return
		}
		gc.JSON(http.StatusOK, resp)
	}
}

// postOperator godoc
// @Summary Update operator
// @Description	Validates and updates an operator
// @Tags Operator
// @Accept json
// @Param id path string true "Operator ID"
// @Param operator body lib.Operator true "Update operator"
// @Success	200
// @Failure	500 {string} str
// @Router /operator/{id} [post]
func postOperator(srv service.Service) (string, string, gin.HandlerFunc) {
	return http.MethodPost, "/operator/:id/", func(gc *gin.Context) {
		var request lib.Operator
		if err := gc.ShouldBindJSON(&request); err != nil {
			util.Logger.Error("error updating operator", "error", err)
			_ = gc.Error(errors.New(MessageSomethingWrong))
			return
		}
		err := srv.UpdateOperator(gc.Param("id"), request, gc.GetString(UserIdKey), gc.GetHeader("Authorization"))
		if err != nil {
			util.Logger.Error("error updating operator", "error", err)
			_ = gc.Error(errors.New(MessageSomethingWrong))
			return
		}
		gc.Status(http.StatusOK)
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
