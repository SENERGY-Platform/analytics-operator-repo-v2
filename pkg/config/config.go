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

package config

import (
	"time"

	sb_config_hdl "github.com/SENERGY-Platform/go-service-base/config-hdl"
	sb_config_types "github.com/SENERGY-Platform/go-service-base/config-hdl/types"
)

type Config struct {
	Debug            bool                   `json:"debug" env_var:"DEBUG"`
	ServerPort       int                    `json:"server_port" env_var:"SERVER_PORT"`
	Logger           LoggerConfig           `json:"logger" env_var:"LOGGER_CONFIG"`
	MongoUrl         string                 `json:"mongo_url" env_var:"MONGO_URL"`
	MongoUser        string                 `json:"mongo_user" env_var:"MONGO_USER"`
	MongoPassword    sb_config_types.Secret `json:"mongo_password" env_var:"MONGO_PASSWORD"`
	MongoAuthSource  string                 `json:"mongo_auth_source" env_var:"MONGO_AUTH_SOURCE"`
	MongoDatabase    string                 `json:"mongo_database" env_var:"MONGO_DATABASE"`
	HttpTimeout      time.Duration          `json:"http_timeout" env_var:"HTTP_TIMEOUT"`
	PermissionsV2Url string                 `json:"permissions_v2_url" env_var:"PERMISSIONS_V2_URL"`
	URLPrefix        string                 `json:"url_prefix" env_var:"URL_PREFIX"`
	// FlowRepoUrl is asked before an operator is deleted, to see whether flows use it.
	FlowRepoUrl string `json:"flow_repo_url" env_var:"FLOW_REPO_URL"`
	// OtelEndpoint is the OTLP collector traces are exported to. Empty means the
	// in-cluster Jaeger the otelx default names, which is what every deployment
	// uses; it is set explicitly only to export somewhere else.
	OtelEndpoint string `json:"otel_endpoint" env_var:"OTEL_ENDPOINT"`
}

type LoggerConfig struct {
	Level string `json:"level" env_var:"LOGGER_LEVEL"`
}

func New(path string) (*Config, error) {
	cfg := Config{
		ServerPort:       8000,
		MongoUrl:         "mongodb://localhost:27017",
		MongoUser:        "",
		MongoPassword:    "",
		MongoAuthSource:  "admin",
		MongoDatabase:    "analytics_operator_repo",
		Debug:            false,
		Logger:           LoggerConfig{Level: "info"},
		HttpTimeout:      30 * time.Second,
		PermissionsV2Url: "http://permv2.permissions:8080",
		URLPrefix:        "",
		FlowRepoUrl:      "http://api.analytics-flow-repo:5000",
		OtelEndpoint:     "",
	}
	err := sb_config_hdl.Load(&cfg, nil, envTypeParser, nil, path)
	return &cfg, err
}
