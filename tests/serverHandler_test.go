package tests

import (
	"context"
	"testing"

	"github.com/Kana-v1-exchange/dashboard/handlers"
	"github.com/Kana-v1-exchange/dashboard/tests/mocks"
	proto "github.com/Kana-v1-exchange/enviroment/protos/serverHandler"
	"google.golang.org/grpc/metadata"
)

func TestSignUp(t *testing.T) {
	postgresHandler := mocks.NewPostgresHandlerMock()
	serverHandler := handlers.NewServerHandler(
		postgresHandler,
		mocks.NewRedisHandlerMock(),
		mocks.NewRmqMock(),
	)

	_, err := serverHandler.SignUp(context.Background(), &proto.User{Email: "testEmail", Password: "testPassword"})
	if err != nil {
		t.Fatal(err)
	}

	usersNum, err := postgresHandler.GetUsersNum()
	if err != nil {
		t.Fatal(err)
	}

	if usersNum != 3 {
		t.Fatalf("expected 3 user; actual: %v", usersNum)
	}
}

func TestSignUpSameEmail(t *testing.T) {
	postgresHandler := mocks.NewPostgresHandlerMock()
	serverHandler := handlers.NewServerHandler(
		postgresHandler,
		mocks.NewRedisHandlerMock(),
		mocks.NewRmqMock(),
	)

	_, err := serverHandler.SignUp(context.Background(), &proto.User{Email: "testEmail", Password: "testPassword"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = serverHandler.SignUp(context.Background(), &proto.User{Email: "testEmail", Password: "testPassword"})
	if err == nil {
		t.Fatal("user with the same email has been signed up")
	}
}

func TestSignIn(t *testing.T) {
	postgresHandler := mocks.NewPostgresHandlerMock()
	serverHandler := handlers.NewServerHandler(
		postgresHandler,
		mocks.NewRedisHandlerMock(),
		mocks.NewRmqMock(),
	)

	_, err := serverHandler.SignUp(context.Background(), &proto.User{Email: "testEmail", Password: "testPassword"})
	if err != nil {
		t.Fatal(err)
	}

	msg, err := serverHandler.SignIn(context.Background(), &proto.User{Email: "testEmail", Password: "testPassword"})

	if msg.Message == "" || err != nil {
		t.Fatal(err)
	}
}

func TestSignInInvalidPass(t *testing.T) {
	postgresHandler := mocks.NewPostgresHandlerMock()
	serverHandler := handlers.NewServerHandler(
		postgresHandler,
		mocks.NewRedisHandlerMock(),
		mocks.NewRmqMock(),
	)

	_, err := serverHandler.SignUp(context.Background(), &proto.User{Email: "testEmail", Password: "testPassword"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = serverHandler.SignIn(context.Background(), &proto.User{Email: "testEmail", Password: "invalidPassword"})

	if err == nil {
		t.Fatal("succesfully signed in with invalid password")
	}
}

func TestSignInInvalidEmail(t *testing.T) {
	postgresHandler := mocks.NewPostgresHandlerMock()
	serverHandler := handlers.NewServerHandler(
		postgresHandler,
		mocks.NewRedisHandlerMock(),
		mocks.NewRmqMock(),
	)

	_, err := serverHandler.SignUp(context.Background(), &proto.User{Email: "testEmail", Password: "testPassword"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = serverHandler.SignIn(context.Background(), &proto.User{Email: "invalidEmail", Password: "testPassword"})

	if err == nil {
		t.Fatal("succesfully signed in with invalid password")
	}
}

func TestGetAllCurrencies(t *testing.T) {
	postgresHandler := mocks.NewPostgresHandlerMock()
	serverHandler := handlers.NewServerHandler(
		postgresHandler,
		mocks.NewRedisHandlerMock(),
		mocks.NewRmqMock(),
	)

	currencies, err := serverHandler.GetAllCurrencies(context.Background(), &proto.EmptyMsg{})
	dbCurrencies, _ := postgresHandler.GetCurrencies()

	if len(currencies.CurrencyValue) != len(dbCurrencies) {
		t.Fatalf("invalid num of currencies; expected: %v, actual: %v", len(dbCurrencies), len(currencies.CurrencyValue))
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestBuyCurrency(t *testing.T) {
	sellerID := uint64(1)
	buyerID := uint64(2)
	currencyToBuy := "AUD"
	amountToBuy := float64(20)

	redisHandler := mocks.NewRedisHandlerMock()
	postgresHandler := mocks.NewPostgresHandlerMock()

	serverHandler := handlers.NewServerHandler(
		postgresHandler,
		redisHandler,
		mocks.NewRmqMock(),
	)

	_, err := serverHandler.SignIn(context.Background(), &proto.User{Email: "buyer", Password: "buyer"})
	if err != nil {
		t.Fatal(err)
	}

	md := metadata.New(map[string]string{"userID": "2"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	buyerCurrencyAmountBeforeDial, _ := postgresHandler.GetUserMoney(buyerID, currencyToBuy)
	sellerCurrencyAmountBeforeDial, _ := postgresHandler.GetUserMoney(sellerID, currencyToBuy)

	sellerUsdBeforeDial, _ := postgresHandler.GetUserMoney(sellerID, "USD")
	buyerUsdBeforeDial, _ := postgresHandler.GetUserMoney(buyerID, "USD")

	_, err = serverHandler.BuyCurrency(ctx, &proto.SellOperation{
		CurrencyValue: &proto.CurrencyValue{
			Value:    float32(amountToBuy),
			Currency: currencyToBuy,
		},
	})

	if err != nil {
		t.Fatal(err)
	}

	buyerCurrencyAmountAfterDial, _ := postgresHandler.GetUserMoney(buyerID, currencyToBuy)
	sellerCurrencyAmountAfterDial, _ := postgresHandler.GetUserMoney(sellerID, currencyToBuy)

	sellerUsdAfterDial, _ := postgresHandler.GetUserMoney(sellerID, "USD")
	buyerUsdAfterDial, _ := postgresHandler.GetUserMoney(buyerID, "USD")

	if buyerCurrencyAmountAfterDial != buyerCurrencyAmountBeforeDial+amountToBuy {
		t.Fatalf("invalid amount of %v after the Dial. Expected: %v, actual: %v",
			currencyToBuy,
			buyerCurrencyAmountBeforeDial+amountToBuy,
			buyerCurrencyAmountAfterDial)
	}

	if sellerCurrencyAmountAfterDial != sellerCurrencyAmountBeforeDial-amountToBuy {
		t.Fatalf("invalid amount of %v after the Dial. Expected: %v, actual: %v",
			currencyToBuy,
			sellerCurrencyAmountAfterDial-amountToBuy,
			sellerCurrencyAmountBeforeDial)
	}

	usdVal, _ := postgresHandler.GetCurrencyValue(currencyToBuy)

	usdDif := amountToBuy * usdVal

	if sellerUsdBeforeDial+usdDif != sellerUsdAfterDial {
		t.Fatalf("invalid amount of %v after the Dial. Expected: %v, actual: %v",
			"USD",
			sellerUsdBeforeDial+usdDif,
			sellerUsdAfterDial)
	}

	if buyerUsdBeforeDial-usdDif != buyerUsdAfterDial {
		t.Fatalf("invalid amount of %v after the Dial. Expected: %v, actual: %v",
			"USD",
			buyerUsdBeforeDial-usdDif,
			buyerUsdAfterDial)
	}
}
