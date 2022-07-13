package mocks

import amqp "github.com/rabbitmq/amqp091-go"

type RmqMock struct {
	msgs chan amqp.Delivery
}

func NewRmqMock() *RmqMock {
	return &RmqMock{
		msgs: make(chan amqp.Delivery),
	}
}

func (rm *RmqMock) Write(msg string) error {
	rm.msgs <- amqp.Delivery{Body: []byte(msg)}

	return nil
}

func (rm *RmqMock) Read() (<-chan amqp.Delivery, error) {
	return rm.msgs, nil
}
