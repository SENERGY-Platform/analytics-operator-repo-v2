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
)

type Config struct {
	Debug            bool          `json:"debug" env_var:"DEBUG"`
	ServerPort       int           `json:"server_port" env_var:"SERVER_PORT"`
	Logger           LoggerConfig  `json:"logger" env_var:"LOGGER_CONFIG"`
	MongoUrl         string        `json:"mongo_url" env_var:"MONGO_URL"`
	HttpTimeout      time.Duration `json:"http_timeout" env_var:"HTTP_TIMEOUT"`
	PermissionsV2Url string        `json:"permissions_v2_url" env_var:"PERMISSIONS_V2_URL"`
	URLPrefix        string        `json:"url_prefix" env_var:"URL_PREFIX"`
}

type LoggerConfig struct {
	Level string `json:"level" env_var:"LOGGER_LEVEL"`
}

func New(path string) (*Config, error) {
	cfg := Config{
		ServerPort:       8000,
		MongoUrl:         "localhost:27017",
		Debug:            false,
		Logger:           LoggerConfig{Level: "info"},
		HttpTimeout:      30 * time.Second,
		PermissionsV2Url: "http://permv2.permissions:8080",
		URLPrefix:        "",
	}
	err := sb_config_hdl.Load(&cfg, nil, envTypeParser, nil, path)
	return &cfg, err
}
