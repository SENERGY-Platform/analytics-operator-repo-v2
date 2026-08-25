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

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/api"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/config"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/db"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/service"
	"github.com/SENERGY-Platform/analytics-operator-repo-v2/pkg/util"
	"github.com/SENERGY-Platform/go-service-base/srv-info-hdl"
	"github.com/SENERGY-Platform/go-service-base/struct-logger/attributes"
	sb_util "github.com/SENERGY-Platform/go-service-base/util"
)

var version = "{version}"

func main() {
	srvInfoHdl := srv_info_hdl.New("analytics-operator-repo-v2", version)

	// Written from the two server goroutines below as well as the startup path,
	// so it cannot be a plain int.
	var ec atomic.Int32
	defer func() {
		os.Exit(int(ec.Load()))
	}()

	config.ParseFlags()

	cfg, err := config.New(config.Flags.ConfPath)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		ec.Store(1)
		return
	}

	util.InitStructLogger(cfg.Logger.Level)

	util.Logger.Info(srvInfoHdl.Name(), "version", srvInfoHdl.Version())
	util.Logger.Info("config: " + sb_util.ToJsonStr(cfg))

	ctx, cf := context.WithCancel(context.Background())

	database, err := db.New(cfg.MongoUrl, cfg.MongoDatabase)
	if err != nil {
		util.Logger.Error("error on db init", "error", err)
		ec.Store(1)
		return
	}
	util.Logger.Debug("connected to database")
	defer database.Disconnect()

	srv, err := service.New(ctx, cfg.PermissionsV2Url, *database)
	if err != nil {
		util.Logger.Error("error on new service", "error", err)
		ec.Store(1)
		return
	}

	httpHandler, err := api.New(*srv, cfg.URLPrefix)
	if err != nil {
		util.Logger.Error("error on new httpHandler", "error", err)
		ec.Store(1)
		return
	}

	bindAddress := ":" + strconv.FormatInt(int64(cfg.ServerPort), 10)

	if cfg.Debug {
		bindAddress = "127.0.0.1:" + strconv.FormatInt(int64(cfg.ServerPort), 10)
	}

	// Without these an idle or deliberately slow client holds a connection open
	// indefinitely. cfg.HttpTimeout has always existed for this and was unused.
	httpServer := &http.Server{
		Addr:              bindAddress,
		Handler:           httpHandler,
		ReadHeaderTimeout: cfg.HttpTimeout,
		ReadTimeout:       cfg.HttpTimeout,
		WriteTimeout:      cfg.HttpTimeout,
		IdleTimeout:       cfg.HttpTimeout,
	}

	go func() {
		util.Wait(ctx, util.Logger, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
		cf()
	}()

	wg := &sync.WaitGroup{}

	wg.Add(1)

	go func() {
		defer wg.Done()
		util.Logger.Info("starting http server")
		if err = httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			util.Logger.Error("starting server failed", attributes.ErrorKey, err)
			ec.Store(1)
		}
		cf()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		util.Logger.Info("stopping http server")
		ctxWt, cf2 := context.WithTimeout(context.Background(), time.Second*5)
		defer cf2()
		if err := httpServer.Shutdown(ctxWt); err != nil {
			util.Logger.Error("stopping server failed", attributes.ErrorKey, err)
			ec.Store(1)
		} else {
			util.Logger.Info("http server stopped")
		}
	}()

	wg.Wait()
}
