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
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	permV2Model "github.com/SENERGY-Platform/permissions-v2/pkg/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
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
	coll *mongo.Collection
}

func NewMongoRepo(perm permV2Client.Client, coll *mongo.Collection) *MongoRepo {
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
	return &MongoRepo{perm: perm, coll: coll}
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
		operatorId := operator.Id.Hex()
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

func (r *MongoRepo) DeleteOperator(id string, userId string, admin bool, auth string) (err error) {
	return
}

func (r *MongoRepo) UpdateOperator(id string, operator lib.Operator, userId string, auth string) (err error) {
	ok, err, _ := r.perm.CheckPermission(auth, PermV2InstanceTopic, id, permV2Client.Write)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New(MessageMissingRights)
	}

	objId, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return
	}
	operator.Id = objId
	res := r.coll.FindOneAndUpdate(context.TODO(), bson.M{"_id": objId}, bson.M{"$set": bson.M{
		"name":           operator.Name,
		"description":    operator.Description,
		"image":          operator.Image,
		"cost":           operator.Cost,
		"deploymentType": operator.DeploymentType,
		"pub":            operator.Pub,
		"inputs":         operator.Inputs,
		"outputs":        operator.Outputs,
		"config_values":  operator.Config,
	}})
	if res.Err() != nil {
		return res.Err()
	}
	return
}

func (r *MongoRepo) All(userId string, admin bool, args map[string][]string, auth string) (response lib.OperatorResponse, err error) {
	opt := options.Find()
	for arg, value := range args {
		if arg == "limit" {
			limit, _ := strconv.ParseInt(value[0], 10, 64)
			opt.SetLimit(limit)
		}
		if arg == "offset" {
			skip, _ := strconv.ParseInt(value[0], 10, 64)
			opt.SetSkip(skip)
		}
		if arg == "order" {
			ord := strings.Split(value[0], ":")
			order := 1
			if ord[1] == "desc" {
				order = -1
			}
			opt.SetSort(bson.M{ord[0]: int64(order)})
		}
	}

	var req = bson.M{}
	ids := []bson.ObjectID{}
	var stringIds []string
	if !admin {
		stringIds, err, _ = r.perm.ListAccessibleResourceIds(auth, PermV2InstanceTopic, permV2Client.ListOptions{}, permV2Client.Read)
		if err != nil {
			return
		}
		for _, id := range stringIds {
			objID, err := bson.ObjectIDFromHex(id)
			if err != nil {
				return lib.OperatorResponse{}, err
			}
			ids = append(ids, objID)
		}
		req = bson.M{
			"$or": []interface{}{
				bson.M{"_id": bson.M{"$in": ids}},
				bson.M{"userId": userId},
			}}
		if val, ok := args["search"]; ok {
			req = bson.M{
				"name": bson.M{"$regex": val[0]},
				"$or": []interface{}{
					bson.M{"_id": bson.M{"$in": ids}},
					bson.M{"userId": userId},
				}}
		}
	}
	cur, err := r.coll.Find(context.TODO(), req, opt)
	if err != nil {
		util.Logger.Error("error on query", "error", err)
		return
	}

	req = bson.M{}
	if !admin {
		req = bson.M{
			"$or": []interface{}{
				bson.M{"_id": bson.M{"$in": ids}},
				bson.M{"userId": userId},
			}}
		if val, ok := args["search"]; ok {
			req = bson.M{
				"name": bson.M{"$regex": val[0]},
				"$or": []interface{}{
					bson.M{"_id": bson.M{"$in": ids}},
					bson.M{"userId": userId},
				}}
		}
	}

	response.Total, err = r.coll.CountDocuments(context.TODO(), req)
	if err != nil {
		util.Logger.Error("error on CountDocuments", "error", err)
		return
	}
	response.Operators = make([]lib.Operator, 0)
	for cur.Next(context.TODO()) {
		// create a value into which the single document can be decoded
		var elem lib.Operator
		err = cur.Decode(&elem)
		if err != nil {
			return
		}
		response.Operators = append(response.Operators, elem)
	}
	return
}

func (r *MongoRepo) FindOperator(id string, userId string, auth string) (operator lib.Operator, err error) {
	objID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return
	}
	ok, err, _ := r.perm.CheckPermission(auth, PermV2InstanceTopic, id, permV2Client.Read)
	if err != nil {
		return operator, err
	}
	if !ok {
		return operator, errors.New(MessageMissingRights)
	}
	err = r.coll.FindOne(context.TODO(), bson.M{"_id": objID}).Decode(&operator)
	if err != nil {
		return
	}
	return
}
