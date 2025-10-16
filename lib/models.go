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
	Name           string        `json:"name,omitempty" binding:"required"`
	Image          string        `json:"image,omitempty"`
	Description    string        `json:"description,omitempty"`
	DeploymentType string        `bson:"deploymentType" json:"deploymentType,omitempty"`
	Cost           *int64        `json:"cost,omitempty"`
	UserId         string        `bson:"userId" json:"userId,omitempty"`
	Pub            bool          `json:"pub,omitempty"`
	Config         []Value       `bson:"config_values" json:"config_values,omitempty"`
	Inputs         []Value       `json:"inputs,omitempty"`
	Outputs        []Value       `json:"outputs,omitempty"`
}

type Value struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
