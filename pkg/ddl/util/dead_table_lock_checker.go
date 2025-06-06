// Copyright 2020 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package util

import (
	"context"
	"strings"
	"time"

	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/ddl/logutil"
	infoschema "github.com/pingcap/tidb/pkg/infoschema/context"
	"github.com/pingcap/tidb/pkg/meta/model"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

const (
	defaultRetryCnt      = 5
	defaultRetryInterval = time.Millisecond * 200
	defaultTimeout       = time.Second
)

// DeadTableLockChecker uses to check dead table locks.
// If tidb-server panic or killed by others, the table locks hold by the killed tidb-server maybe doesn't released.
type DeadTableLockChecker struct {
	etcdCli *clientv3.Client
}

// NewDeadTableLockChecker creates new DeadLockChecker.
func NewDeadTableLockChecker(etcdCli *clientv3.Client) DeadTableLockChecker {
	return DeadTableLockChecker{
		etcdCli: etcdCli,
	}
}

func (d *DeadTableLockChecker) getAliveServers(ctx context.Context) (map[string]struct{}, error) {
	var err error
	var resp *clientv3.GetResponse
	allInfos := make(map[string]struct{})
	for range defaultRetryCnt {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		childCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
		resp, err = d.etcdCli.Get(childCtx, DDLAllSchemaVersions, clientv3.WithPrefix())
		cancel()
		if err != nil {
			logutil.DDLLogger().Info("clean dead table lock get alive servers failed.", zap.Error(err))
			time.Sleep(defaultRetryInterval)
			continue
		}
		for _, kv := range resp.Kvs {
			serverID := strings.TrimPrefix(string(kv.Key), DDLAllSchemaVersions+"/")
			allInfos[serverID] = struct{}{}
		}
		return allInfos, nil
	}
	return nil, errors.Trace(err)
}

// GetDeadLockedTables gets dead locked tables.
func (d *DeadTableLockChecker) GetDeadLockedTables(ctx context.Context, is infoschema.MetaOnlyInfoSchema) (map[model.SessionInfo][]model.TableLockTpInfo, error) {
	if d.etcdCli == nil {
		return nil, nil
	}
	aliveServers, err := d.getAliveServers(ctx)
	if err != nil {
		return nil, err
	}
	deadLockTables := make(map[model.SessionInfo][]model.TableLockTpInfo)

	tbls := is.ListTablesWithSpecialAttribute(func(t *model.TableInfo) bool {
		return t.Lock != nil
	})
	for _, db := range tbls {
		for _, tbl := range db.TableInfos {
			if tbl.Lock == nil {
				continue
			}
			for _, se := range tbl.Lock.Sessions {
				if _, ok := aliveServers[se.ServerID]; !ok {
					deadLockTables[se] = append(deadLockTables[se], model.TableLockTpInfo{
						SchemaID: tbl.DBID,
						TableID:  tbl.ID,
						Tp:       tbl.Lock.Tp,
					})
				}
			}
		}
	}
	return deadLockTables, nil
}
