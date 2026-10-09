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
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/flowrepo"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
)

type Service struct {
	dbRepo db.OperatorRepository
	flows  FlowUsage
}

// FlowUsage asks which flows use an operator, with the caller's token.
type FlowUsage interface {
	Usage(ctx context.Context, operatorId string, auth string) (lib.OperatorUsage, error)
}

// DeleteOptions carries what a delete needs besides the ids. Force deletes operators that flows
// still use; the caller has to have checked that the user may do so.
type DeleteOptions struct {
	Force  bool
	UserId string
}

// New builds the service together with the permissions-v2 client it needs. The
// client is constructed here rather than handed in, because constructing it can
// fail: the caller used to discard that error and carry on with a nil client.
func New(ctx context.Context, permissionsUrl string, flowRepoUrl string, database db.MongoDB) (*Service, error) {
	if flowRepoUrl == "" {
		// Without it every delete would end in a 502; better to fail at startup.
		return nil, fmt.Errorf("no flow repository address configured")
	}
	perm, err := newPermissionsClient(ctx, permissionsUrl)
	if err != nil {
		return nil, fmt.Errorf("permissions client: %w", err)
	}
	dbRepo, err := db.NewMongoRepo(ctx, perm, database.OperatorCollection())
	if err != nil {
		return nil, err
	}
	if err = dbRepo.ValidateOperatorPermissions(ctx); err != nil {
		return nil, err
	}
	return &Service{dbRepo: dbRepo, flows: flowrepo.New(flowRepoUrl)}, nil
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

func (s *Service) CreateOperator(ctx context.Context, operator lib.Operator, userId string) (err error) {
	operator.UserId = userId
	return s.dbRepo.InsertOperator(ctx, operator)
}

func (s *Service) UpdateOperator(ctx context.Context, id string, operator lib.Operator, auth string) (err error) {
	return s.dbRepo.UpdateOperator(ctx, id, operator, auth)
}

func (s *Service) DeleteOperator(ctx context.Context, id string, auth string, opts DeleteOptions) (err error) {
	used, err := s.usedBy(ctx, []string{id}, auth, opts)
	if err != nil {
		return err
	}
	if err = s.dbRepo.DeleteOperator(ctx, id, auth); err != nil {
		return err
	}
	logForced(ctx, used, opts)
	return nil
}

// DeleteOperators refuses the whole batch when flows use any of the operators, unless opts.Force
// is set.
func (s *Service) DeleteOperators(ctx context.Context, ids []string, auth string, opts DeleteOptions) (err error) {
	used, err := s.usedBy(ctx, ids, auth, opts)
	if err != nil {
		return err
	}
	if err = s.dbRepo.DeleteOperators(ctx, ids, auth); err != nil {
		return err
	}
	logForced(ctx, used, opts)
	return nil
}

// usedBy asks the flow repository for every id before anything is deleted, so a refusal deletes
// nothing. It returns the operators a forced delete goes ahead with, and an *lib.InUseError when
// there are some and the delete is not forced. Check and delete are not atomic: a flow saved in
// between is not seen.
func (s *Service) usedBy(ctx context.Context, ids []string, auth string, opts DeleteOptions) ([]lib.UsedOperator, error) {
	used := make([]lib.UsedOperator, 0)
	for _, id := range ids {
		usage, err := s.flows.Usage(ctx, id, auth)
		if err != nil {
			// The detail may hold internals and the handler logs the sentinel at error level.
			util.Logger.WarnContext(ctx, "could not ask the flow repository for the usage of an operator", "operator_id", id, "error", err)
			return nil, lib.ErrFlowRepoUnavailable
		}
		if usage.Flows > 0 {
			used = append(used, lib.UsedOperator{Id: id, OperatorUsage: usage})
		}
	}
	if len(used) > 0 && !opts.Force {
		return nil, &lib.InUseError{Operators: used}
	}
	return used, nil
}

func logForced(ctx context.Context, used []lib.UsedOperator, opts DeleteOptions) {
	for _, u := range used {
		util.Logger.WarnContext(ctx, "deleted an operator that flows still use",
			"operator_id", u.Id, "user_id", opts.UserId, "flows", u.Flows)
	}
}

func (s *Service) GetOperators(ctx context.Context, userId string, args map[string][]string, auth string) (response lib.OperatorResponse, err error) {
	return s.dbRepo.All(ctx, userId, false, args, auth)
}

func (s *Service) GetOperator(ctx context.Context, id string, auth string) (response lib.Operator, err error) {
	return s.dbRepo.FindOperator(ctx, id, auth)
}
