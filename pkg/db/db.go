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
	"time"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	sb_config_types "github.com/SENERGY-Platform/go-service-base/config-hdl/types"
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

// Config is what New needs to reach the database. URL is a full connection
// string including the scheme; an empty User means the client does not authenticate.
type Config struct {
	URL        string
	User       string
	Password   sb_config_types.Secret
	AuthSource string
	Database   string
}

// validate refuses values the config loader lets through: a variable that is set
// but empty overwrites its default with "".
func (cfg Config) validate() error {
	if cfg.Database == "" {
		return errors.New("MONGO_DATABASE is empty")
	}
	if cfg.User != "" && cfg.Password.Value() == "" {
		return errors.New("MONGO_USER is set but MONGO_PASSWORD is empty")
	}
	return nil
}

// clientOptions passes the URL to the driver unchanged. A set User replaces any
// credentials the URL carries, because SetAuth after ApplyURI overrides them.
func clientOptions(cfg Config) *options.ClientOptions {
	opts := options.Client().
		ApplyURI(cfg.URL).
		SetMonitor(newCommandMonitor())
	if cfg.User != "" {
		opts.SetAuth(options.Credential{
			Username:   cfg.User,
			Password:   cfg.Password.Value(),
			AuthSource: cfg.AuthSource,
		})
	}
	return opts
}

func New(cfg Config) (*MongoDB, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	client, err := mongo.Connect(clientOptions(cfg))
	if err != nil {
		return nil, err
	}
	return open(client, cfg.URL, cfg.Database)
}

// open takes ownership of client and disconnects it on every failure, so a
// failed start leaves no monitoring goroutines or pooled connections behind.
func open(client *mongo.Client, url, database string) (_ *MongoDB, err error) {
	defer func() {
		if err != nil {
			ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
			defer cf()
			if dErr := client.Disconnect(ctx); dErr != nil {
				util.Logger.Error("disconnecting after a failed start failed", attributes.ErrorKey, dErr)
			}
		}
	}()
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
	if err = db.checkAccess(timeout); err != nil {
		return nil, fmt.Errorf("mongo startup check failed: %w", err)
	}
	if err = db.ensureIndexes(timeout); err != nil {
		return nil, fmt.Errorf("create indexes: %w", err)
	}
	return db, nil
}

// checkAccess fails without valid authentication on a server that enforces it,
// which ping does not: MongoDB answers ping unauthenticated.
func (db *MongoDB) checkAccess(ctx context.Context) error {
	_, err := db.client.Database(db.database).ListCollectionNames(ctx, bson.D{},
		options.ListCollections().SetNameOnly(true).SetAuthorizedCollections(true))
	return err
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
