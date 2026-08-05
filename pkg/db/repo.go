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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

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
	UpdateOperator(id string, operator lib.Operator, auth string) (err error)
	DeleteOperator(id string, auth string) (err error)
	DeleteOperators(ids []string, auth string) (err error)
	All(userId string, admin bool, args map[string][]string, auth string) (response lib.OperatorResponse, err error)
	FindOperator(id string, auth string) (flow lib.Operator, err error)
}

type MongoRepo struct {
	perm permV2Client.Client
	coll *mongo.Collection
}

func NewMongoRepo(perm permV2Client.Client, coll *mongo.Collection) (*MongoRepo, error) {
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
		return nil, fmt.Errorf("set permissions topic: %w", err)
	}
	return &MongoRepo{perm: perm, coll: coll}, nil
}

func (r *MongoRepo) ValidateOperatorPermissions() (err error) {
	util.Logger.Debug("validate operator permissions")
	operators, err := r.allOperatorOwners()
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
	for _, operator := range operators {
		permissions := permV2Client.ResourcePermissions{
			UserPermissions:  map[string]permV2Client.PermissionsMap{},
			GroupPermissions: map[string]permV2Client.PermissionsMap{},
			RolePermissions:  map[string]permV2Model.PermissionsMap{},
		}
		operatorId := operator.Id.Hex()
		dbIds = append(dbIds, operatorId)
		resource, ok := permResourceMap[operatorId]
		if ok {
			if ownerHasFullPermissions(resource, operator.UserId) {
				// Already correct, and this runs for every operator on every
				// start — writing it back would be a wasted round trip.
				continue
			}
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
	operator.DateCreated = time.Now()
	operator.DateUpdated = time.Now()
	version := int64(1)
	operator.Version = &version
	permissions := permV2Client.ResourcePermissions{
		GroupPermissions: map[string]permV2Client.PermissionsMap{},
		UserPermissions:  map[string]permV2Client.PermissionsMap{},
		RolePermissions:  map[string]permV2Model.PermissionsMap{},
	}
	SetDefaultPermissions(operator, permissions)
	if operator.Id != nil {
		operator.Id = nil
	}
	result, err := r.coll.InsertOne(context.TODO(), operator)
	if err != nil {
		return err
	}

	id := result.InsertedID.(bson.ObjectID).Hex()
	_, err, _ = r.perm.SetPermission(permV2Client.InternalAdminToken, PermV2InstanceTopic, id, permissions)
	return
}

func (r *MongoRepo) DeleteOperator(id string, auth string) (err error) {
	ok, err, _ := r.perm.CheckPermission(auth, PermV2InstanceTopic, id, permV2Client.Administrate)
	if err != nil {
		return err
	}
	if !ok {
		return lib.ErrMissingRights
	}

	objID, err := objectID(id)
	if err != nil {
		return
	}
	req := bson.M{"_id": objID}
	res := r.coll.FindOneAndDelete(context.TODO(), req)
	if res.Err() != nil {
		return notFound(res.Err())
	}
	err, _ = r.perm.RemoveResource(permV2Client.InternalAdminToken, PermV2InstanceTopic, id)
	return
}

func (r *MongoRepo) DeleteOperators(ids []string, auth string) (err error) {
	okArr, err, _ := r.perm.CheckMultiplePermissions(auth, PermV2InstanceTopic, ids, permV2Client.Administrate)
	if err != nil {
		return err
	}
	for id, ok := range okArr {
		if !ok {
			return fmt.Errorf("%w: id %s", lib.ErrMissingRights, id)
		}
	}
	var objID bson.ObjectID
	for _, id := range ids {
		objID, err = objectID(id)
		if err != nil {
			return
		}
		req := bson.M{"_id": objID}
		res := r.coll.FindOneAndDelete(context.TODO(), req)
		if res.Err() != nil {
			return notFound(res.Err())
		}
		err, _ = r.perm.RemoveResource(permV2Client.InternalAdminToken, PermV2InstanceTopic, id)
		if err != nil {
			return
		}
	}
	return
}

func (r *MongoRepo) UpdateOperator(id string, operator lib.Operator, auth string) (err error) {
	ok, err, _ := r.perm.CheckPermission(auth, PermV2InstanceTopic, id, permV2Client.Write)
	if err != nil {
		return err
	}
	if !ok {
		return lib.ErrMissingRights
	}

	objId, err := objectID(id)
	if err != nil {
		return
	}
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
		"dateUpdated":    time.Now(),
	},
		"$inc": bson.M{"version": 1},
	})
	if res.Err() != nil {
		return notFound(res.Err())
	}
	return
}

func (r *MongoRepo) All(userId string, admin bool, args map[string][]string, auth string) (response lib.OperatorResponse, err error) {
	opt := options.Find()
	limit := int64(MaxLimit)
	for arg, value := range args {
		if arg == "sort" {
			field, direction, _ := strings.Cut(value[0], ":")
			order := int64(1)
			if direction == "desc" {
				order = -1
			}

			sortFields := []string{"name"}
			if slices.Contains(sortFields, field) {
				opt.SetSort(bson.M{field: order})
			}
		}
		if arg == "limit" {
			limit, err = parseQueryInt(arg, value[0])
			if err != nil {
				return lib.OperatorResponse{}, err
			}
			if limit > MaxLimit {
				return lib.OperatorResponse{}, fmt.Errorf("%w: limit exceeds maximum of %d", lib.ErrInvalidInput, MaxLimit)
			}
		}
		if arg == "offset" {
			skip, skipErr := parseQueryInt(arg, value[0])
			if skipErr != nil {
				return lib.OperatorResponse{}, skipErr
			}
			opt.SetSkip(skip)
		}
	}
	opt.SetLimit(limit)

	var req = bson.M{}
	ids := []bson.ObjectID{}
	var stringIds []string
	if !admin {
		stringIds, err, _ = r.perm.ListAccessibleResourceIds(auth, PermV2InstanceTopic, permV2Client.ListOptions{}, permV2Client.Read)
		if err != nil {
			return
		}
		for _, id := range stringIds {
			objID, err := objectID(id)
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
				// QuoteMeta keeps the caller's input a literal substring: an
				// unescaped value would let them run arbitrary regex on the server.
				"name": bson.M{"$regex": regexp.QuoteMeta(val[0])},
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

	response.Total, err = r.coll.CountDocuments(context.TODO(), req)
	if err != nil {
		util.Logger.Error("error on CountDocuments", "error", err)
		return
	}
	response.Operators = make([]lib.Operator, 0)
	err = cur.All(context.TODO(), &response.Operators)
	if err != nil {
		return lib.OperatorResponse{}, err
	}
	return
}

func (r *MongoRepo) FindOperator(id string, auth string) (operator lib.Operator, err error) {
	objID, err := objectID(id)
	if err != nil {
		return
	}
	ok, err, _ := r.perm.CheckPermission(auth, PermV2InstanceTopic, id, permV2Client.Read)
	if err != nil {
		return operator, err
	}
	if !ok {
		return operator, lib.ErrMissingRights
	}
	err = r.coll.FindOne(context.TODO(), bson.M{"_id": objID}).Decode(&operator)
	if err != nil {
		return operator, notFound(err)
	}
	return
}

// ownerHasFullPermissions reports whether the owner already holds everything
// SetDefaultPermissions would grant, so reconciliation can skip the write.
func ownerHasFullPermissions(resource permV2Client.Resource, userId string) bool {
	p, ok := resource.ResourcePermissions.UserPermissions[userId]
	return ok && p.Read && p.Write && p.Execute && p.Administrate
}

// allOperatorOwners returns the id and owner of every operator. Reconciliation
// deletes permissions for ids it does not see, so it must see all of them — it
// therefore bypasses All and the caller-facing MaxLimit cap by construction.
// Only the two fields SetDefaultPermissions needs are read.
func (r *MongoRepo) allOperatorOwners() ([]lib.Operator, error) {
	opt := options.Find().SetProjection(bson.M{"_id": 1, "userId": 1})
	cur, err := r.coll.Find(context.TODO(), bson.M{}, opt)
	if err != nil {
		return nil, err
	}
	operators := []lib.Operator{}
	if err = cur.All(context.TODO(), &operators); err != nil {
		return nil, err
	}
	return operators, nil
}

// objectID parses a caller-supplied id, reporting a malformed one as invalid
// input rather than letting the driver's message reach the client.
func objectID(id string) (bson.ObjectID, error) {
	objID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return objID, fmt.Errorf("%w: malformed operator id", lib.ErrInvalidInput)
	}
	return objID, nil
}

// notFound maps the driver's empty-result error onto the API sentinel and
// leaves every other error untouched.
func notFound(err error) error {
	if errors.Is(err, mongo.ErrNoDocuments) {
		return lib.ErrNotFound
	}
	return err
}

// parseQueryInt accepts only non-negative integers; anything else is the
// caller's mistake and must not silently fall back to a default.
func parseQueryInt(arg string, raw string) (int64, error) {
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", lib.ErrInvalidInput, arg)
	}
	return v, nil
}
