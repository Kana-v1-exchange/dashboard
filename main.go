package main

import (
	"net/http"

	"github.com/Kana-v1-exchange/dashboard/config"
	"github.com/Kana-v1-exchange/dashboard/handlers"
	proto "github.com/Kana-v1-exchange/enviroment/protos/serverHandler"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/joho/godotenv"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
)

const port = ":11111"

func main() {
	err := godotenv.Load("./envs/.env")
	if err != nil {
		panic(err)
	}

	grpcServer := grpc.NewServer()
	grpcWebServer := grpcweb.WrapServer(grpcServer)

	grpcHandler := handlers.NewServerHandler(
		config.GetPostgresConfig().Connect(),
		config.GetRedisConfig().Connect(),
		config.GetRmqConfig().Connect(),
	)

	proto.RegisterDashboardServiceServer(grpcServer, grpcHandler)

	serverHandler := h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-User-Agent, X-Grpc-Web")
		w.Header().Set("grpc-status", "")
		w.Header().Set("grpc-message", "")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
		}

		grpcWebServer.ServeHTTP(w, r)
	}), new(http2.Server))

	err = http.ListenAndServe(port, serverHandler)
	if err != nil {
		panic(err)
	}
}
