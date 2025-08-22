package worker

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

type ResultsHandler struct {
	redisClient *redis.Client
}

const updateResultsLuaScript = `
local counterKeyPrefix = KEYS[1]
local amountKeyPrefix = KEYS[2]
local timestamp = tonumber(ARGV[1])
local amountValue = tonumber(ARGV[2])
local incrementVal = tonumber(ARGV[3])

local counterKey = counterKeyPrefix .. ":" .. timestamp
local amountKey = amountKeyPrefix .. ":" .. timestamp

local newAmount = redis.call('INCRBYFLOAT', amountKey, amountValue)
local newCount = redis.call('INCRBY', counterKey, incrementVal)

return {newAmount, newCount}
`

func NewResultsHandler(rc *redis.Client) *ResultsHandler {
	return &ResultsHandler{
		redisClient: rc,
	}
}

func (rh *ResultsHandler) updateResults(ctx context.Context, CounterKeyPrefix string, amountKeyPrefix string, timestamp int64, timeSeriesValue float64) error {
	if err := rh.redisClient.Eval(ctx, updateResultsLuaScript,
		[]string{CounterKeyPrefix, amountKeyPrefix},
		timestamp,
		timeSeriesValue,
		1, // incrementa 1 no contador
	).Err(); err != nil {
		return err
	}

	return nil
}

func (rh *ResultsHandler) ResultsCountByRangePipe(pipe redis.Pipeliner, ctx context.Context, counterKeyPrefix string, start, end int64) *redis.SliceCmd {
	keys := rh.getBucketKeysInRange(counterKeyPrefix, start, end)
	if len(keys) == 0 {
		return nil
	}

	return pipe.MGet(ctx, keys...)
}

// Calcula todas as chaves entre start e end
func (rh *ResultsHandler) getBucketKeysInRange(counterKeyPrefix string, start, end int64) []string {
	keys := []string{}

	if start == 0 && end == 0 {
		return rh.getAllBucketKeys(counterKeyPrefix)
	}

	if start != 0 && end == 0 {
		return rh.getBucketKeysWithStartOrEnd(
			counterKeyPrefix,
			start,
			startingAtComparison,
		)
	}

	if start == 0 {
		return rh.getBucketKeysWithStartOrEnd(
			counterKeyPrefix,
			end,
			endingAtComparison,
		)
	}

	for bucket := start; bucket <= end; bucket++ {
		keys = append(keys, fmt.Sprintf("%s:%d", counterKeyPrefix, bucket))
	}

	return keys
}

func startingAtComparison(keyTimestamp, start int64) bool {
	return keyTimestamp >= start
}

func endingAtComparison(keyTimestamp, end int64) bool {
	return keyTimestamp <= end
}

func (rh *ResultsHandler) getAllBucketKeys(counterKeyPrefix string) []string {
	var cursor uint64
	var keys []string
	for {
		var k []string
		var err error
		k, cursor, err = rh.redisClient.Scan(context.Background(), cursor, counterKeyPrefix+":*", 100).Result()
		if err != nil {
			log.Fatalf(err.Error())
		}

		keys = append(keys, k...)

		if cursor == 0 {
			break
		}
	}

	return keys
}

func (rh *ResultsHandler) getBucketKeysWithStartOrEnd(
	counterKeyPrefix string,
	limit int64,
	comparisonFunc func(timestamp, limit int64) bool,
) []string {
	var cursor uint64
	var keys []string
	var parts []string
	var keyTimestamp int64
	prefix := counterKeyPrefix + ":"

	for {
		var k []string
		var err error
		k, cursor, err = rh.redisClient.Scan(context.Background(), cursor, counterKeyPrefix+":*", 100).Result()
		if err != nil {
			log.Printf("Error scanning redis keys: %v", err)
			return []string{}
		}

		for _, key := range k {
			parts = strings.SplitAfter(key, prefix)
			if len(parts) == 2 {
				keyTimestamp, err = strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					log.Printf("Error scanning redis keys: %v", err)
					continue
				}

				if comparisonFunc(keyTimestamp, limit) {
					keys = append(keys, key)
				}
			}
		}

		if cursor == 0 {
			break
		}
	}

	return keys
}
