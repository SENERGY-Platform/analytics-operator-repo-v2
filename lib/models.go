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

package lib

import "go.mongodb.org/mongo-driver/v2/bson"

type OperatorResponse struct {
	Operators []Operator `json:"operators"`
	Total     int64      `json:"totalCount"`
}

type Operator struct {
	Id             bson.ObjectID `bson:"_id" json:"_id"`
	Name           string        `json:"name"`
	Image          string        `json:"image"`
	Description    string        `json:"description"`
	DeploymentType string        `json:"deploymentType"`
	Cost           *int64        `json:"cost"`
	UserId         string        `json:"userId"`
	Pub            bool          `json:"pub"`
	Config         []Value       `json:"config_values"`
	Inputs         []Value       `json:"inputs"`
	Outputs        []Value       `json:"outputs"`
}

type Value struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
