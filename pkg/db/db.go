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
	"time"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MongoDB struct {
	url    string
	client *mongo.Client
}

func New(url string) (*MongoDB, error) {
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://" + url))
	if err != nil {
		return nil, err
	}
	return &MongoDB{
		url:    url,
		client: client,
	}, nil
}

func (db *MongoDB) Disconnect(ctx context.Context) {
	timeout, _ := getTimeoutContext(ctx)
	if err := db.client.Disconnect(timeout); err != nil {
		panic(err)
	}
}

func (db *MongoDB) OperatorCollection() *mongo.Collection {
	return db.client.Database("db").Collection("operators")
}

func SetDefaultPermissions(instance lib.Operator, permissions permV2Client.ResourcePermissions) {
	permissions.UserPermissions[instance.UserId] = permV2Client.PermissionsMap{
		Read:         true,
		Write:        true,
		Execute:      true,
		Administrate: true,
	}
}

func getTimeoutContext(basectx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(basectx, 10*time.Second)
}
