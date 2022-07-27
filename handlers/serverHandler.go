package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Kana-v1-exchange/dashboard/helpers"
	postgres "github.com/Kana-v1-exchange/enviroment/postgres"
	proto "github.com/Kana-v1-exchange/enviroment/protos/serverHandler"
	redis "github.com/Kana-v1-exchange/enviroment/redis"
	rmq "github.com/Kana-v1-exchange/enviroment/rmq"

	"github.com/jackc/pgx/v4"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/metadata"
)

type ServerHandler struct {
	postgresHandler     postgres.PostgresHandler
	transactionExecutor postgres.TransactionExecutor
	redisHandler        redis.RedisHandler
	rmqHandler          rmq.RmqHandler

	proto.UnimplementedDashboardServiceServer
}

const tokenTime = 120

func NewServerHandler(p postgres.PostgresHandler, t postgres.TransactionExecutor, r redis.RedisHandler, rmq rmq.RmqHandler) *ServerHandler {
	return &ServerHandler{
		postgresHandler:     p,
		transactionExecutor: t,
		redisHandler:        r,
		rmqHandler:          rmq,
	}
}

func (sh *ServerHandler) SignUp(ctx context.Context, user *proto.User) (*proto.DefaultStringMsg, error) {
	msg := make(chan string)
	errCh := make(chan error)
	go func(msg chan string, errCh chan error) {
		_, _, err := sh.postgresHandler.GetUserData(user.Email)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
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
		return nil, fmt.Errorf("cannot sign up; err: %v", err)
	}
}

func (sh *ServerHandler) SignIn(ctx context.Context, user *proto.User) (*proto.DefaultStringMsg, error) {
	msg := make(chan string)
	errCh := make(chan error)

	go func(msg chan string, errCh chan error) {
		userID, dbPass, err := sh.postgresHandler.GetUserData(user.Email)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
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

		msg <- fmt.Sprintf("%v", userID)
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
	if sellInfo.Currency == "USD" {
		return nil, fmt.Errorf("cannot buy USD")
	}

	msg := make(chan *proto.DefaultStringMsg)
	errCh := make(chan error)
	transfered := make(chan float64, 1)

	go func(msg chan *proto.DefaultStringMsg, transfered chan<- float64, errCh chan error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			buyerID, err := strconv.ParseUint(md.Get("userID")[0], 10, 64)
			if err != nil {
				errCh <- fmt.Errorf("%wcannot get userID from the context metadata; err: %v", helpers.ErrInternal, err)
				return
			}

			if err := sh.isUserAlive(buyerID); err != nil {
				if errors.Is(err, helpers.ErrInternal) {
					errCh <- fmt.Errorf("%winternal err: %v", helpers.ErrInternal, err.Error())
				} else {
					errCh <- err
				}

				return
			}

			err = sh.transactionExecutor.LockMoney()
			if err != nil {
				errCh <- err
				return
			}

			sellers, err := sh.postgresHandler.FindSellers(sh.transactionExecutor, sellInfo.Currency, float64(sellInfo.Amount), float64(sellInfo.FloorPrice), float64(sellInfo.CeilPrice))
			if err != nil {
				errCh <- err
				return
			}

			amountToPay := float64(0)
			currencySum := float64(0)

			for _, seller := range sellers {
				err = sh.postgresHandler.GetMoneyFromSellingPool(
					sh.transactionExecutor,
					seller.Currency,
					buyerID,
					float64(sellInfo.Amount*(1-helpers.Fee)),
					float64(sellInfo.FloorPrice),
					float64(sellInfo.CeilPrice),
				)

				if err != nil {
					sh.transactionExecutor.Rollback()
					errCh <- fmt.Errorf("%vcannot buy currency. err: %w", helpers.ErrInternal, err)
					return
				}

				err = sh.postgresHandler.SendMoney(sh.transactionExecutor, buyerID, seller.UserID, sellInfo.Currency, seller.Price*seller.Amount)
				if err != nil {
					sh.transactionExecutor.Rollback()
					errCh <- fmt.Errorf("%vcannot send money to the seller. err: %w", helpers.ErrInternal, err)
					return
				}

				currencySum += seller.Amount
				amountToPay += seller.Price * seller.Amount
			}

			if currencySum < float64(sellInfo.Amount) {
				sh.transactionExecutor.Rollback()
				errCh <- fmt.Errorf("sellers do not have enough %v with such price. Need %v, have %v", sellInfo.Currency, sellInfo.Amount, currencySum)
				return
			}
			availableUSD, err := sh.postgresHandler.GetUserMoney(buyerID, "USD")
			if err != nil {
				errCh <- fmt.Errorf("%w%v", helpers.ErrInternal, err)
				return
			}

			if availableUSD < amountToPay {
				sh.transactionExecutor.Rollback()
				errCh <- fmt.Errorf("need %v USD. Has %v", amountToPay, availableUSD)
				return
			}

			err = sh.transactionExecutor.Commit()
			if err != nil {
				errCh <- fmt.Errorf("%w%v", helpers.ErrInternal, err)
				return
			}

			msg <- &proto.DefaultStringMsg{Message: fmt.Sprintf("%v %v has been bought", sellInfo.Amount, sellInfo.Currency)}
		} else {
			errCh <- fmt.Errorf("%wrequest doesn't contain metadata", helpers.ErrInternal)
		}
	}(msg, transfered, errCh)

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context has been canceled")
	case message := <-msg:
		go func() {
			err := sh.redisHandler.Increment(sellInfo.Currency + redis.RedisCurrencyOperationsSuffix)
			if err != nil {
				fmt.Println(fmt.Errorf("%wcannot increment the number of the operations with the currency %v; err: %v", helpers.ErrInternal, sellInfo.Currency, err))
				return
			}

			pricesStr, err := json.Marshal(<-transfered)
			if err != nil {
				panic(err)
			}

			err = sh.redisHandler.AddToList(sellInfo.Currency+redis.RedisCurrencyPriceSuffix, string(pricesStr))
			if err != nil {
				panic(err)
			}
		}()

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
			userIDs := md.Get("userID")
			if len(userIDs) == 0 {
				errCh <- fmt.Errorf("%wrequest doesn't contain metadata", helpers.ErrInternal)
				return
			}

			userID, err := strconv.ParseUint(userIDs[0], 10, 64)
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

			alreadyHas, err := sh.postgresHandler.GetUserMoney(userID, sellInfo.Currency)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					alreadyHas = 0
				} else {
					errCh <- err
					return
				}
			}

			if alreadyHas < float64(sellInfo.Amount) {
				errCh <- fmt.Errorf("trying to sell(%v %v) more than has(%v %v)", sellInfo.Amount, sellInfo.Currency, alreadyHas, sellInfo.Currency)
				return
			}

			err = sh.transactionExecutor.LockMoney()
			if err != nil {
				errCh <- err
				return
			}

			err = sh.postgresHandler.AddMoneyToSellingPool(sh.transactionExecutor, sellInfo.Currency, userID, float64(sellInfo.Amount), float64(sellInfo.FloorPrice))
			if err != nil {
				sh.transactionExecutor.Rollback()
				errCh <- fmt.Errorf("%vcannot seil currency; err: %w", helpers.ErrInternal, err)
				return
			}

			err = sh.transactionExecutor.Commit()
			if err != nil {
				errCh <- err
				return
			}

		} else {
			errCh <- fmt.Errorf("%wrequest doesn't contain metadata", helpers.ErrInternal)
			return
		}

		msg <- &proto.DefaultStringMsg{Message: fmt.Sprintf("%v %v selling now", sellInfo.Amount, sellInfo.Currency)}
	}(msg, errCh)

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context has been canceled")
	case message := <-msg:
		err := sh.redisHandler.Increment(sellInfo.Currency + redis.RedisCurrencyOperationsSuffix)
		if err != nil {
			return nil, fmt.Errorf("%wcannot increment the number of the operations with the currency %v; err: %v", helpers.ErrInternal, sellInfo.Currency, err)
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
			return fmt.Errorf("%wcannot parse rmq message (%v); err: %v", helpers.ErrInternal, string(message.Body), err)
		}

		if parsedMessage.Currency == currency.Message {
			stream.Send(&proto.DefaultFloatMsg{Value: parsedMessage.Value})
		}
	}
}

func (sh *ServerHandler) GetUserMoney(ctx context.Context, _ *proto.EmptyMsg) (*proto.GetCurrenciesResponse, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("%wrequest doesn't contain metadata", helpers.ErrInternal)
	}
	userIDs := md.Get("userID")
	if len(userIDs) == 0 {
		return nil, fmt.Errorf("%wrequest contains %v userIDs instead of 1 metadata", helpers.ErrInternal, len(userIDs))
	}

	userID, err := strconv.ParseUint(userIDs[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%wcannot get userID from the context metadata; err: %v", helpers.ErrInternal, err)
	}

	if err := sh.isUserAlive(userID); err != nil {
		if errors.Is(err, helpers.ErrInternal) {
			return nil, fmt.Errorf("%winternal err: %v", helpers.ErrInternal, err.Error())
		} else {
			return nil, err
		}
	}

	currencies, err := sh.postgresHandler.GetCurrencies()
	if err != nil {
		return nil, fmt.Errorf("%winternal err: %v", helpers.ErrInternal, err.Error())
	}

	currencyValues := make([]*proto.CurrencyValue, 0, len(currencies))

	for currency := range currencies {
		amount, err := sh.postgresHandler.GetUserMoney(userID, currency)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%winternal err: %v", helpers.ErrInternal, err.Error())
		}

		currencyValues = append(currencyValues, &proto.CurrencyValue{Value: float32(amount), Currency: currency})
	}

	return &proto.GetCurrenciesResponse{CurrencyValue: currencyValues}, nil
}

func (sh *ServerHandler) GetUserHistory(context.Context, *proto.EmptyMsg) (*proto.GetUserHistoryResponse, error) {
	return nil, errors.New("not implemented")
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
