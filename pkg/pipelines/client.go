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

// Package pipelines asks the pipeline registry which pipelines run an operator.
package pipelines

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
	// The answer lists the caller's pipelines; this only bounds a misbehaving server.
	maxBodyBytes = 4 << 20
)

type Client struct {
	url  string
	http *http.Client
}

func New(baseUrl string) *Client {
	return &Client{url: strings.TrimRight(baseUrl, "/"), http: &http.Client{Timeout: requestTimeout}}
}

// Usage returns the pipeline registry's answer for the operator, asked with the caller's own
// token. A non-empty image narrows it to the pipelines running that image; the registry refuses
// an empty one, so it is left out then. Any failure, including a non-200 status, is an error: an
// unknown answer is not "unused".
func (c *Client) Usage(ctx context.Context, operatorId string, image string, auth string) (usage lib.PipelineUsage, err error) {
	target := c.url + "/pipeline/statistics/operatorusage/" + url.PathEscape(operatorId)
	if image != "" {
		target += "?" + url.Values{"image": {image}}.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
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
		return usage, fmt.Errorf("pipeline registry answered %d", resp.StatusCode)
	}
	// A pointer, so that a 200 without a count is an error and not "zero pipelines".
	var answer struct {
		Pipelines *int              `json:"pipelines"`
		Readable  []lib.PipelineRef `json:"readable"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&answer); err != nil {
		return usage, fmt.Errorf("decode pipeline registry answer: %w", err)
	}
	if answer.Pipelines == nil {
		return usage, errors.New("pipeline registry answer has no pipeline count")
	}
	if *answer.Pipelines < 0 {
		return usage, fmt.Errorf("pipeline registry answered a negative count %d", *answer.Pipelines)
	}
	usage.Pipelines = *answer.Pipelines
	usage.ReadablePipelines = answer.Readable
	if usage.ReadablePipelines == nil {
		usage.ReadablePipelines = []lib.PipelineRef{}
	}
	return usage, nil
}
