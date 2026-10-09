/*
 * Copyright 2026 InfAI (CC SES)
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

// Package flowrepo asks the flow repository which flows use an operator.
package flowrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/lib"
)

const (
	requestTimeout = 10 * time.Second
	// The answer lists the flow names the caller may read; this only bounds a misbehaving server.
	maxBodyBytes = 4 << 20
)

type Client struct {
	url  string
	http *http.Client
}

func New(baseUrl string) *Client {
	return &Client{url: strings.TrimRight(baseUrl, "/"), http: &http.Client{Timeout: requestTimeout}}
}

// Usage returns the flow repository's answer for the operator, asked with the caller's own token.
// Any failure, including a non-200 status, is an error: an unknown answer is not "unused".
func (c *Client) Usage(ctx context.Context, operatorId string, auth string) (usage lib.OperatorUsage, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/operators/"+url.PathEscape(operatorId)+"/usage", nil)
	if err != nil {
		return usage, err
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return usage, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return usage, fmt.Errorf("flow repository answered %d", resp.StatusCode)
	}
	// A pointer, so that a 200 without a count is an error and not "zero flows".
	var answer struct {
		Flows    *int          `json:"flows"`
		Readable []lib.FlowRef `json:"readable"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&answer); err != nil {
		return usage, fmt.Errorf("decode flow repository answer: %w", err)
	}
	if answer.Flows == nil {
		return usage, errors.New("flow repository answer has no flow count")
	}
	usage.Flows = *answer.Flows
	usage.Readable = answer.Readable
	if usage.Readable == nil {
		usage.Readable = []lib.FlowRef{}
	}
	return usage, nil
}
