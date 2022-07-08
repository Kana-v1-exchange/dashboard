package handlers

import (
	"context"

	proto "github.com/Kana-v1-exchange/dashboard/protos/handlers/serverHandler"
	postgres "github.com/Kana-v1-exchange/enviroment/postgres"
	redis "github.com/Kana-v1-exchange/enviroment/redis"
	rmq "github.com/Kana-v1-exchange/enviroment/rmq"
)

type ServerHandler struct {
	postgresHandler postgres.PostgresHandler
	redisHandler    redis.RedisHandler
	rmqHandler      rmq.RmqHandler

	proto.UnimplementedDashboardServiceServer
}

func (sh *ServerHandler) SignIn(context.Context, *proto.User) (*proto.DefaultStringMsg, error) {
	
}
