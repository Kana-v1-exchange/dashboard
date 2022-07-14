package mocks

import (
	"errors"

	"github.com/jackc/pgx/v4"
)

type user struct {
	email    string
	password string
	money    map[string]float64
}

type PostgresHandlerMock struct {
	users      map[uint64]user
	currencies map[string]float64
}

func NewPostgresHandlerMock() *PostgresHandlerMock {
	return &PostgresHandlerMock{
		users: map[uint64]user{
			1: {
				email:    "seller",
				password: "$2a$10$B4jIiCs429IUhk9g8cwsQuv8vQCNtTrpWGdaM7/ZNkk23keHI8HVG",
				money:    map[string]float64{"USD": 1000, "AUD": 1000},
			},
			2: {
				email: "buyer",
				password: "$2a$10$P9clVRx8N/xttuRPTL4sq.emGf.1QXC5dlCAmVECTdQcjUtpL924G	",
				money: map[string]float64{"USD": 1000, "AUD": 1000},
			},
		},
		currencies: map[string]float64{"USD": 1, "AUD": 2},
	}
}

func (phm *PostgresHandlerMock) GetCurrencies() (map[string]float64, error) {
	return phm.currencies, nil
}

func (phm *PostgresHandlerMock) GetUsersNum() (int, error) {
	return len(phm.users), nil
}

func (phm *PostgresHandlerMock) UpdateCurrency(currency string, value float64) error {
	return nil
}

func (phm *PostgresHandlerMock) GetCurrencyAmount(currency string) (float64, error) {
	sum := float64(0)
	for _, user := range phm.users {
		sum += user.money[currency]
	}

	return sum, nil
}

func (phm *PostgresHandlerMock) GetCurrencyValue(currency string) (float64, error) {
	return phm.currencies[currency], nil
}

func (phm *PostgresHandlerMock) UpdateCurrencyAmount(userID uint64, currency string, value float64) error {
	phm.users[userID].money[currency] = value

	return nil
}

func (phm *PostgresHandlerMock) AddUser(email, password string) error {
	id := uint64(0)
	for key := range phm.users {
		if key > id {
			id = key
		}
	}

	id++

	phm.users[id] = user{
		email:    email,
		password: password,
		money:    map[string]float64{"USD": 1000, "AUD": 1000},
	}

	return nil
}

func (phm *PostgresHandlerMock) GetUserData(email string) (uint64, string, error) {
	for id, user := range phm.users {
		if user.email == email {
			return id, user.password, nil
		}
	}

	return 0, "", pgx.ErrNoRows
}

func (phm *PostgresHandlerMock) GetUserMoney(userID uint64, currency string) (float64, error) {
	return phm.users[userID].money[currency], nil
}

func (phm *PostgresHandlerMock) SendCurrency(from, to uint64, currency string, value float64) error {
	phm.users[to].money[currency] += value
	phm.users[from].money[currency] -= value

	return nil
}

func (phm *PostgresHandlerMock) FindSeller(currency string, value float64) (uint64, error) {
	for id, user := range phm.users {
		if user.money[currency] >= value {
			return id, nil
		}
	}

	return 0, errors.New("user with such value of the currency does not exist")
}
