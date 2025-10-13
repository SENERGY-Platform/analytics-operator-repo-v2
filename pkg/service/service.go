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

package service

import (
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/db"
	srv_info_hdl "github.com/SENERGY-Platform/go-service-base/srv-info-hdl"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
)

type Service struct {
	srvInfoHdl srv_info_hdl.Handler
	dbRepo     db.OperatorRepository
}

func New(srvInfoHdl srv_info_hdl.Handler, perm permV2Client.Client) (*Service, error) {
	dbRepo := db.NewMongoRepo(perm)
	err := dbRepo.ValidateOperatorPermissions()
	return &Service{
		srvInfoHdl: srvInfoHdl,
		dbRepo:     dbRepo,
	}, err
}
