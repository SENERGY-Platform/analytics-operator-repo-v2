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
	"context"
	"fmt"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/db"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
)

type Service struct {
	dbRepo db.OperatorRepository
}

// New builds the service together with the permissions-v2 client it needs. The
// client is constructed here rather than handed in, because constructing it can
// fail: the caller used to discard that error and carry on with a nil client.
func New(ctx context.Context, permissionsUrl string, database db.MongoDB) (*Service, error) {
	perm, err := newPermissionsClient(ctx, permissionsUrl)
	if err != nil {
		return nil, fmt.Errorf("permissions client: %w", err)
	}
	dbRepo, err := db.NewMongoRepo(perm, database.OperatorCollection())
	if err != nil {
		return nil, err
	}
	if err = dbRepo.ValidateOperatorPermissions(); err != nil {
		return nil, err
	}
	return &Service{dbRepo: dbRepo}, nil
}

// newPermissionsClient returns the client for the configured address. The literal
// "mock" selects the in-process implementation, which is what a local run and the
// tests use; anything else is treated as the address of a permissions-v2.
func newPermissionsClient(ctx context.Context, url string) (permV2Client.Client, error) {
	if url == "" {
		// Refused here rather than accepted: a client built for an empty address
		// fails on every request instead of at startup, where a misconfiguration
		// is still cheap to read.
		return nil, fmt.Errorf("no permissions-v2 address configured")
	}
	if url == "mock" {
		util.Logger.Debug("using mock permissions")
		return permV2Client.NewTestClient(ctx)
	}
	return permV2Client.New(url), nil
}

func (s *Service) CreateOperator(operator lib.Operator, userId string) (err error) {
	operator.UserId = userId
	return s.dbRepo.InsertOperator(operator)
}

func (s *Service) UpdateOperator(id string, operator lib.Operator, auth string) (err error) {
	return s.dbRepo.UpdateOperator(id, operator, auth)
}

func (s *Service) DeleteOperator(id string, auth string) (err error) {
	return s.dbRepo.DeleteOperator(id, auth)
}

func (s *Service) DeleteOperators(ids []string, auth string) (err error) {
	return s.dbRepo.DeleteOperators(ids, auth)
}

func (s *Service) GetOperators(userId string, args map[string][]string, auth string) (response lib.OperatorResponse, err error) {
	return s.dbRepo.All(userId, false, args, auth)
}

func (s *Service) GetOperator(id string, auth string) (response lib.Operator, err error) {
	return s.dbRepo.FindOperator(id, auth)
}
