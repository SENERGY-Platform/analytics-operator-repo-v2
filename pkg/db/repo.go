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

package db

import (
	"fmt"
	"maps"
	"slices"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	permV2Model "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

type OperatorRepository interface {
	InsertOperator(operator lib.Operator) (err error)
	UpdateOperator(id string, operator lib.Operator, userId string, auth string) (err error)
	DeleteOperator(id string, userId string, admin bool, auth string) (err error)
	All(userId string, admin bool, args map[string][]string, auth string) (response lib.OperatorResponse, err error)
	FindOperator(id string, userId string, auth string) (flow lib.Operator, err error)
}

type MongoRepo struct {
	perm permV2Client.Client
}

func NewMongoRepo(perm permV2Client.Client) *MongoRepo {
	_, err, _ := perm.SetTopic(permV2Client.InternalAdminToken, permV2Client.Topic{
		Id: PermV2InstanceTopic,
		DefaultPermissions: permV2Client.ResourcePermissions{
			RolePermissions: map[string]permV2Model.PermissionsMap{
				"admin": {
					Read:         true,
					Write:        true,
					Execute:      true,
					Administrate: true,
				},
			},
		},
	})
	if err != nil {
		return nil
	}
	return &MongoRepo{perm: perm}
}

func (r *MongoRepo) ValidateOperatorPermissions() (err error) {
	util.Logger.Debug("validate operator permissions")
	resp, err := r.All("", true, map[string][]string{}, "")
	if err != nil {
		return
	}
	permResources, err, _ := r.perm.ListResourcesWithAdminPermission(permV2Client.InternalAdminToken, PermV2InstanceTopic, permV2Client.ListOptions{})
	if err != nil {
		return
	}
	permResourceMap := map[string]permV2Client.Resource{}
	for _, permResource := range permResources {
		permResourceMap[permResource.Id] = permResource
	}

	dbIds := []string{}
	for _, operator := range resp.Operators {
		permissions := permV2Client.ResourcePermissions{
			UserPermissions:  map[string]permV2Client.PermissionsMap{},
			GroupPermissions: map[string]permV2Client.PermissionsMap{},
			RolePermissions:  map[string]permV2Model.PermissionsMap{},
		}
		operatorId := operator.Id
		dbIds = append(dbIds, operatorId)
		resource, ok := permResourceMap[operatorId]
		if ok {
			permissions.UserPermissions = resource.ResourcePermissions.UserPermissions
			permissions.GroupPermissions = resource.GroupPermissions
			permissions.RolePermissions = resource.ResourcePermissions.RolePermissions
		}
		SetDefaultPermissions(operator, permissions)

		_, err, _ = r.perm.SetPermission(permV2Client.InternalAdminToken, PermV2InstanceTopic, operatorId, permissions)
		if err != nil {
			return
		}
	}
	permResourceIds := maps.Keys(permResourceMap)

	for permResouceId := range permResourceIds {
		if !slices.Contains(dbIds, permResouceId) {
			err, _ = r.perm.RemoveResource(permV2Client.InternalAdminToken, PermV2InstanceTopic, permResouceId)
			if err != nil {
				return
			}
			util.Logger.Debug(fmt.Sprintf("%s exists only in permissions-v2, now deleted", permResouceId))
		}
	}
	return
}

func (r *MongoRepo) InsertOperator(operator lib.Operator) (err error) {
	return
}

func (r *MongoRepo) UpdateOperator(id string, operator lib.Operator, userId string, auth string) (err error) {
	return
}

func (r *MongoRepo) DeleteOperator(id string, userId string, admin bool, auth string) (err error) {
	return
}

func (r *MongoRepo) All(userId string, admin bool, args map[string][]string, auth string) (response lib.OperatorResponse, err error) {
	return
}

func (r *MongoRepo) FindOperator(id string, userId string, auth string) (flow lib.Operator, err error) {
	return
}
