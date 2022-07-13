package main

import (
	"fmt"
	"net"

	"github.com/Kana-v1-exchange/dashboard/config"
	"github.com/Kana-v1-exchange/dashboard/handlers"
	proto "github.com/Kana-v1-exchange/enviroment/protos/serverHandler"
	"github.com/joho/godotenv"
	"google.golang.org/grpc"
)

const port = ":9000"

func main() {
	err := godotenv.Load("./envs/.env")
	if err != nil {
		panic(err)
	}

	listener, err := net.Listen("tcp", port)

	s := grpc.NewServer()

	serverHandler := handlers.NewServerHandler(
		config.GetPostgresConfig().Connect(),
		config.GetRedisConfig().Connect(),
		config.GetRmqConfig().Connect(),
	)

	proto.RegisterDashboardServiceServer(s, serverHandler)

	fmt.Println("start listening on port " + port)

	s.Serve(listener)
}
