package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Kana-v1-exchange/dashboard/helpers"
	postgres "github.com/Kana-v1-exchange/enviroment/postgres"
	proto "github.com/Kana-v1-exchange/enviroment/protos/serverHandler"
	redis "github.com/Kana-v1-exchange/enviroment/redis"
	rmq "github.com/Kana-v1-exchange/enviroment/rmq"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/metadata"
)

type ServerHandler struct {
	postgresHandler postgres.PostgresHandler
	redisHandler    redis.RedisHandler
	rmqHandler      rmq.RmqHandler

	transaction *sync.Mutex

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

		msg <- "user has been signed up"
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

func (sh *ServerHandler) SignIn(ctx context.Context, user *proto.User) (*proto.DefaultStringMsg, error) {
	msg := make(chan string)
	errCh := make(chan error)

	go func(msg chan string, errCh chan error) {
		userID, dbPass, err := sh.postgresHandler.GetUserData(user.Email)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				errCh <- errors.New("invalid email")
				return
			}
			errCh <- err
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(dbPass), []byte(user.Password)); err != nil {
			errCh <- errors.New("invalid password")
			return
		}

		expiresAt := time.Now().Add(time.Minute * tokenTime)
		_, err = sh.redisHandler.GetOrUpdateUserToken(userID, &expiresAt)
		if err != nil {
			errCh <- err
			return
		}

		msg <- "succesfully signed in"
	}(msg, errCh)

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context has been canceled")
	case message := <-msg:
		return &proto.DefaultStringMsg{Message: message}, nil
	case err := <-errCh:
		return nil, err
	}
}

func (sh *ServerHandler) GetAllCurrencies(ctx context.Context, _ *proto.EmptyMsg) (*proto.GetCurrenciesResponse, error) {
	response := make(chan *proto.GetCurrenciesResponse)
	errCh := make(chan error)

	go func(response chan *proto.GetCurrenciesResponse, errCh chan error) {
		currencies, err := sh.postgresHandler.GetCurrencies()
		if err != nil {
			errCh <- err
			return
		}

		curValues := make([]*proto.CurrencyValue, 0, len(currencies))

		for currency, value := range currencies {
			curValues = append(curValues, &proto.CurrencyValue{
				Value:    float32(value),
				Currency: currency,
			})
		}

		response <- &proto.GetCurrenciesResponse{CurrencyValue: curValues}
	}(response, errCh)

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context has been canceled")
	case msg := <-response:
		return msg, nil
	case err := <-errCh:
		return nil, err
	}
}

func (sh *ServerHandler) BuyCurrency(ctx context.Context, sellInfo *proto.SellOperation) (*proto.DefaultStringMsg, error) {
	msg := make(chan *proto.DefaultStringMsg)
	errCh := make(chan error)

	go func(msg chan *proto.DefaultStringMsg, errCh chan error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			userID, err := strconv.ParseUint(md.Get("userID")[0], 10, 64)
			if err != nil {
				errCh <- fmt.Errorf("%wcannot get userID from the context metadata; err: %v", helpers.ErrInternal, err)
				return
			}

			if err := sh.isUserAlive(userID); err != nil {
				if errors.Is(err, helpers.ErrInternal) {
					errCh <- fmt.Errorf("%winternal err: %v", helpers.ErrInternal, err.Error())
				} else {
					errCh <- err
				}

				return
			}

			sh.transaction.Lock()
			defer sh.transaction.Unlock()

			sellerID, err := sh.postgresHandler.FindSeller(sellInfo.CurrencyValue.Currency, float64(sellInfo.CurrencyValue.Value))
			if err != nil {
				errCh <- err
				return
			}

			currencyVal, err := sh.postgresHandler.GetCurrencyValue(sellInfo.CurrencyValue.Currency)
			if err != nil {
				errCh <- err
				return
			}

			usdToTransact := currencyVal * float64(sellInfo.CurrencyValue.Value)

			// send USD to a seller
			err = sh.postgresHandler.SendCurrency(uint64(sellInfo.UserID), sellerID, "USD", usdToTransact)
			if err != nil {
				errCh <- fmt.Errorf("%w%v", helpers.ErrInternal, err)
			}

			// send currency to a buyer
			err = sh.postgresHandler.SendCurrency(sellerID, uint64(sellInfo.UserID), sellInfo.CurrencyValue.Currency, float64(sellInfo.CurrencyValue.Value))
			if err != nil {
				// rollback if error
				sh.postgresHandler.SendCurrency(sellerID, uint64(sellInfo.UserID), "USD", usdToTransact)

				errCh <- fmt.Errorf("%w%v", helpers.ErrInternal, err)
				return
			}

			msg <- &proto.DefaultStringMsg{Message: fmt.Sprintf("%v %v has been bought", sellInfo.CurrencyValue.Value, sellInfo.CurrencyValue.Currency)}
		} else {
			errCh <- fmt.Errorf("%wrequest doesn't contain metadata", helpers.ErrInternal)
		}
	}(msg, errCh)

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context has been canceled")
	case message := <-msg:
		err := sh.redisHandler.Increment(sellInfo.CurrencyValue.Currency + redis.RedisCurrencyOperationsSuffix)
		if err != nil {
			return nil, fmt.Errorf("%wcannot increment the number of the operations with the currency %v; err: %v", helpers.ErrInternal, sellInfo.CurrencyValue.Currency, err)
		}

		return message, nil
	case err := <-errCh:
		return nil, err
	}
}

func (sh *ServerHandler) SellCurrency(ctx context.Context, sellInfo *proto.SellOperation) (*proto.DefaultStringMsg, error) {
	msg := make(chan *proto.DefaultStringMsg)
	errCh := make(chan error)

	go func(msg chan *proto.DefaultStringMsg, errCh chan error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			userID, err := strconv.ParseUint(md.Get("userID")[0], 10, 64)
			if err != nil {
				errCh <- fmt.Errorf("%wcannot get userID from the context metadata; err: %v", helpers.ErrInternal, err)
				return
			}

			if err := sh.isUserAlive(userID); err != nil {
				if errors.Is(err, helpers.ErrInternal) {
					errCh <- fmt.Errorf("%winternal err: %v", helpers.ErrInternal, err.Error())
				} else {
					errCh <- err
				}

				return
			}

			currencyValue, err := sh.postgresHandler.GetCurrencyAmount(sellInfo.CurrencyValue.Currency)
			if err != nil {
				errCh <- fmt.Errorf("%w%v", helpers.ErrInternal, err)
				return
			}

			usdToSell := currencyValue * float64(sellInfo.CurrencyValue.Value)
			availableUSD, err := sh.postgresHandler.GetUserMoney(uint64(sellInfo.UserID), sellInfo.CurrencyValue.Currency)
			if err != nil {
				errCh <- fmt.Errorf("%w%v", helpers.ErrInternal, err)
				return
			}

			if availableUSD < usdToSell {
				errCh <- fmt.Errorf("user has %v USD, but wants to sell %v", availableUSD, usdToSell)
				return
			}

			sh.transaction.Lock()
			defer sh.transaction.Unlock()

			err = sh.postgresHandler.UpdateCurrencyAmount(uint64(sellInfo.UserID), "USD", availableUSD-usdToSell)
			if err != nil {
				errCh <- fmt.Errorf("%w%v", helpers.ErrInternal, err)
			}

			err = sh.postgresHandler.UpdateCurrencyAmount(uint64(sellInfo.UserID), sellInfo.CurrencyValue.Currency, float64(sellInfo.CurrencyValue.Value))
			if err != nil {
				fullErr := fmt.Errorf("%w%v", helpers.ErrInternal, err)
				err = sh.postgresHandler.UpdateCurrencyAmount(uint64(sellInfo.UserID), "USD", availableUSD)
				if err != nil {
					fullErr = fmt.Errorf("%v. Also cannot rollback a transaction", fullErr)
				}

				errCh <- fullErr
			}
		} else {
			errCh <- fmt.Errorf("%wrequest doesn't contain metadata", helpers.ErrInternal)
		}
	}(msg, errCh)

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context has been canceled")
	case message := <-msg:
		err := sh.redisHandler.Increment(sellInfo.CurrencyValue.Currency + redis.RedisCurrencyOperationsSuffix)
		if err != nil {
			return nil, fmt.Errorf("%wcannot increment the number of the operations with the currency %v; err: %v", helpers.ErrInternal, sellInfo.CurrencyValue.Currency, err)
		}

		return message, nil
	case err := <-errCh:
		return nil, err
	}
}

func (sh *ServerHandler) GetCurrencyValue(currency *proto.DefaultStringMsg, stream proto.DashboardService_GetCurrencyValueServer) error {
	msgs, err := sh.rmqHandler.Read()
	if err != nil {
		return fmt.Errorf("%cannot read from the rmq queue; err: %v", helpers.ErrInternal, err)
	}

	for {
		message := <-msgs
		parsedMessage := new(proto.CurrencyValue)
		err := json.Unmarshal(message.Body, &parsedMessage)
		if err != nil {
			return fmt.Errorf("%wcannot parse rmq message (%s); err: %v", helpers.ErrInternal, message.Body, err)
		}

		if parsedMessage.Currency == currency.Message {
			stream.Send(&proto.DefaultFloatMsg{Value: parsedMessage.Value})
		}
	}
}

func (sh *ServerHandler) isUserAlive(userID uint64) error {
	expiresAt, err := sh.redisHandler.GetOrUpdateUserToken(userID, nil)
	if err != nil {
		return fmt.Errorf("%w%v", helpers.ErrInternal, err)
	}

	if time.Now().After(expiresAt) {
		return errors.New("user's token has already been expired")
	}

	return nil
}
