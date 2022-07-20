package mocks

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	redis "github.com/Kana-v1-exchange/enviroment/redis"
)

type RedisHandlerMock struct {
	storage map[string]string
}

func NewRedisHandlerMock() *RedisHandlerMock {
	return &RedisHandlerMock{storage: make(map[string]string)}
}

func (rhm *RedisHandlerMock) Set(key, value string) error {
	rhm.storage[key] = value
	return nil
}

func (rhm *RedisHandlerMock) Get(key string) (string, error) {
	return rhm.storage[key], nil
}

func (rhm *RedisHandlerMock) Remove(keys ...string) error {
	for _, key := range keys {
		delete(rhm.storage, key)
	}

	return nil
}

func (rhm *RedisHandlerMock) Increment(keys ...string) error {
	for _, key := range keys {
		value, err := strconv.ParseFloat(rhm.storage[key], 64)
		if err != nil {
			rhm.storage[key] = "1"
			return nil
		}

		value++

		rhm.storage[key] = fmt.Sprint(value)
	}

	return nil
}

func (rhm *RedisHandlerMock) AddOperation(currency string, price float64) error {
	operations := make([]float64, 0)

	err := json.Unmarshal([]byte(rhm.storage[currency+redis.RedisCurrencyOperationsSuffix]), &operations)

	if err != nil {
		panic(err.Error())
	}

	operations = append(operations, price)

	operationsStr, err := json.Marshal(operations)
	if err != nil {
		panic(err.Error())
	}

	rhm.storage[currency+redis.RedisCurrencyOperationsSuffix] = string(operationsStr)

	return nil
}

func (rhm *RedisHandlerMock) GetOrUpdateUserToken(userID uint64, expiresAt *time.Time) (time.Time, error) {
	var t time.Time
	var err error

	if userTime, ok := rhm.storage[fmt.Sprint(userID)+redis.UserTokenSuffix]; ok {
		t, err = time.Parse(time.RFC3339, userTime)
		if err != nil {
			panic(err.Error())
		}
	}

	if expiresAt != nil {
		rhm.storage[fmt.Sprint(userID)+redis.UserTokenSuffix] = expiresAt.Format(time.RFC3339)
	}

	return t, err
}

func (rhm *RedisHandlerMock) AddToList(key string, values ...string) error {
	rhm.storage[key] = strings.Join(values, ";")
	return nil
}

func (rhm *RedisHandlerMock) GetList(key string) ([]string, error) {
	str, ok := rhm.storage[key]
	if !ok {
		return nil, errors.New("cannot find value by key")
	}

	return strings.Split(str, ";"), nil
}
