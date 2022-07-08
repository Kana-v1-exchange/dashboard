package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	proto "github.com/Kana-v1-exchange/dashboard/protos/handlers/serverHandler"
	postgres "github.com/Kana-v1-exchange/enviroment/postgres"
	redis "github.com/Kana-v1-exchange/enviroment/redis"
	rmq "github.com/Kana-v1-exchange/enviroment/rmq"
	"golang.org/x/crypto/bcrypt"
)

type ServerHandler struct {
	postgresHandler postgres.PostgresHandler
	redisHandler    redis.RedisHandler
	rmqHandler      rmq.RmqHandler

	proto.UnimplementedDashboardServiceServer
}

const tokenTime = 120

func (sh *ServerHandler) SignUp(ctx context.Context, user *proto.User) (*proto.DefaultStringMsg, error) {
	msg := make(chan string)
	errCh := make(chan error)
	go func(msg chan string, errCh chan error) {
		_, _, err := sh.postgresHandler.GetUserData(user.Email)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				errCh <- err
				return
			}
		} else {
			errCh <- fmt.Errorf("user with such email already exists")
			return
		}

		pass, err := bcrypt.GenerateFromPassword([]byte(user.Password), 0)
		if err != nil {
			errCh <- fmt.Errorf("cannot generate hashcode from the password(%v); err: %v", user.Password, err)
			return
		}

		err = sh.postgresHandler.AddUser(user.Email, string(pass))
		if err != nil {
			errCh <- err
			return
		}

		msg <- "user has been signed in"

	}(msg, errCh)

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context has been canceled")
	case message := <-msg:
		return &proto.DefaultStringMsg{Message: message}, nil
	case err := <-errCh:
		return nil, fmt.Errorf("cannot sign in; err: %v", err)
	}
}
