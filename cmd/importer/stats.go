// Copyright 2018 PingCAP, Inc.
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

package main

import (
	"encoding/json"
	"math/rand"
	"os"
	"time"

	"github.com/pingcap/errors"
	"github.com/pingcap/log"
	"github.com/pingcap/tidb/pkg/meta/model"
	stats "github.com/pingcap/tidb/pkg/statistics"
	"github.com/pingcap/tidb/pkg/statistics/handle/storage"
	"github.com/pingcap/tidb/pkg/statistics/util"
	"github.com/pingcap/tidb/pkg/types"
	"go.uber.org/zap"
)

func loadStats(tblInfo *model.TableInfo, path string) (*stats.Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Trace(err)
	}
	jsTable := &util.JSONTable{}
	err = json.Unmarshal(data, jsTable)
	if err != nil {
		return nil, errors.Trace(err)
	}
	return storage.TableStatsFromJSON(tblInfo, tblInfo.ID, jsTable)
}

type histogram struct {
	stats.Histogram

	index  *model.IndexInfo
	avgLen int
}

// When the randCnt falls in the middle of bucket, we return the idx of lower bound which is an even number.
// When the randCnt falls in the end of bucket, we return the upper bound which is odd.
func (h *histogram) getRandomBoundIdx() int {
	cnt := h.Buckets[len(h.Buckets)-1].Count
	randCnt := randInt64(0, cnt)
	for i, bkt := range h.Buckets {
		if bkt.Count >= randCnt {
			if bkt.Count-bkt.Repeat > randCnt {
				return 2 * i
			}
			return 2*i + 1
		}
	}
	return 0
}

func (h *histogram) randInt() int64 {
	idx := h.getRandomBoundIdx()
	if idx%2 == 0 {
		lower := h.Bounds.GetRow(idx).GetInt64(0)
		upper := h.Bounds.GetRow(idx + 1).GetInt64(0)
		return randInt64(lower, upper)
	}
	return h.Bounds.GetRow(idx).GetInt64(0)
}

// #nosec G404
func getValidPrefix(lower, upper string) string {
	for i := range lower {
		if i >= len(upper) {
			log.Fatal("lower is larger than upper", zap.String("lower", lower), zap.String("upper", upper))
		}
		if lower[i] != upper[i] {
			randCh := uint8(rand.Intn(int(upper[i]-lower[i]))) + lower[i]
			newBytes := make([]byte, i, i+1)
			copy(newBytes, lower[:i])
			newBytes = append(newBytes, randCh)
			return string(newBytes)
		}
	}
	return lower
}

func (h *histogram) getAvgLen(maxLen int) int {
	l := h.Bounds.NumRows()
	totalLen := 0
	for i := range l {
		totalLen += len(h.Bounds.GetRow(i).GetString(0))
	}
	avg := min(totalLen/l, maxLen)
	if avg == 0 {
		avg = 1
	}
	return avg
}

func (h *histogram) randString() string {
	idx := h.getRandomBoundIdx()
	if idx%2 == 0 {
		lower := h.Bounds.GetRow(idx).GetString(0)
		upper := h.Bounds.GetRow(idx + 1).GetString(0)
		prefix := getValidPrefix(lower, upper)
		restLen := h.avgLen - len(prefix)
		if restLen > 0 {
			prefix = prefix + randString(restLen)
		}
		return prefix
	}
	return h.Bounds.GetRow(idx).GetString(0)
}

// randDate randoms a bucket and random a date between upper and lower bound.
func (h *histogram) randDate(unit string, mysqlFmt string, dateFmt string) string {
	idx := h.getRandomBoundIdx()
	if idx%2 == 0 {
		lower := h.Bounds.GetRow(idx).GetTime(0)
		upper := h.Bounds.GetRow(idx + 1).GetTime(0)
		diff := types.TimestampDiff(unit, lower, upper)
		if diff == 0 {
			str, err := lower.DateFormat(mysqlFmt)
			if err != nil {
				log.Fatal(err.Error())
			}
			return str
		}
		delta := randInt(0, int(diff)-1)
		l, err := lower.GoTime(time.Local)
		if err != nil {
			log.Fatal(err.Error())
		}
		l = l.AddDate(0, 0, delta)
		return l.Format(dateFmt)
	}
	str, err := h.Bounds.GetRow(idx).GetTime(0).DateFormat(mysqlFmt)
	if err != nil {
		log.Fatal(err.Error())
	}
	return str
}
