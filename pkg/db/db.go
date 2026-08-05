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
	"fmt"
	"time"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	"github.com/SENERGY-Platform/go-service-base/struct-logger/attributes"
	permV2Client "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MongoDB struct {
	url      string
	database string
	client   *mongo.Client
}

func New(url string, database string) (*MongoDB, error) {
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://" + url))
	if err != nil {
		return nil, err
	}
	db := &MongoDB{
		url:      url,
		database: database,
		client:   client,
	}
	// Connect is lazy, so without a ping an unreachable database only surfaces
	// on the first request — after the service has reported itself as started.
	timeout, cf := getTimeoutContext(context.Background())
	defer cf()
	if err = client.Ping(timeout, nil); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if err = db.ensureIndexes(timeout); err != nil {
		return nil, fmt.Errorf("create indexes: %w", err)
	}
	return db, nil
}

// ensureIndexes covers the two access patterns All relies on: sorting by name
// and the owner term of the accessible-resources filter.
func (db *MongoDB) ensureIndexes(ctx context.Context) error {
	_, err := db.OperatorCollection().Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "name", Value: 1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}}},
	})
	return err
}

// Disconnect uses its own deadline instead of inheriting one: it runs during
// shutdown, when the callers' context is already cancelled.
func (db *MongoDB) Disconnect() {
	timeout, cf := getTimeoutContext(context.Background())
	defer cf()
	if err := db.client.Disconnect(timeout); err != nil {
		util.Logger.Error("disconnecting from database failed", attributes.ErrorKey, err)
	}
}

func (db *MongoDB) OperatorCollection() *mongo.Collection {
	return db.client.Database(db.database).Collection("operators")
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
