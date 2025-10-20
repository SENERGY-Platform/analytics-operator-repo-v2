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
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/db"
	srv_info_hdl "github.com/SENERGY-Platform/go-service-base/srv-info-hdl"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
)

type Service struct {
	srvInfoHdl srv_info_hdl.Handler
	dbRepo     db.OperatorRepository
}

func New(srvInfoHdl srv_info_hdl.Handler, perm permV2Client.Client, database db.MongoDB) (*Service, error) {
	dbRepo := db.NewMongoRepo(perm, database.OperatorCollection())
	err := dbRepo.ValidateOperatorPermissions()
	return &Service{
		srvInfoHdl: srvInfoHdl,
		dbRepo:     dbRepo,
	}, err
}

func (s *Service) CreateOperator(operator lib.Operator, userId string) (err error) {
	operator.UserId = userId
	return s.dbRepo.InsertOperator(operator)
}

func (s *Service) UpdateOperator(id string, operator lib.Operator, userId string, auth string) (err error) {
	return s.dbRepo.UpdateOperator(id, operator, userId, auth)
}

func (s *Service) DeleteOperator(id string, userId string, auth string) (err error) {
	return s.dbRepo.DeleteOperator(id, userId, false, auth)
}

func (s *Service) DeleteOperators(ids []string, userId string, auth string) (err error) {
	return s.dbRepo.DeleteOperators(ids, userId, false, auth)
}

func (s *Service) GetOperators(userId string, args map[string][]string, auth string) (response lib.OperatorResponse, err error) {
	return s.dbRepo.All(userId, false, args, auth)
}

func (s *Service) GetOperator(id string, userId string, auth string) (response lib.Operator, err error) {
	return s.dbRepo.FindOperator(id, userId, auth)
}
