package mqttclient

import (
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"math/rand"
	"sync"
	"time"
)

type mockClient struct {
	sync.Mutex
	connected bool
	exit      chan bool

	subs   map[string][]mqtt.MessageHandler
	broker IMessageBroker
}

type mockMessage struct {
	id       uint16
	topic    string
	qos      byte
	retained bool
	payload  interface{}
}

var (
	//_ mqtt.Client  = newMockClient(nil)
	_ mqtt.Message = newMockMessage("mock", 0, false, nil)
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func newMockClient(p IMessageBroker) (mqtt.Client, error) {
	return &mockClient{
		subs:   make(map[string][]mqtt.MessageHandler),
		broker: p,
	}, nil
}

func newMockMessage(topic string, qos byte, retained bool, payload interface{}) mqtt.Message {
	return &mockMessage{
		id:       uint16(rand.Int()),
		topic:    topic,
		qos:      qos,
		retained: retained,
		payload:  payload,
	}
}

func (m *mockMessage) Ack() {
	return
}

func (m *mockMessage) Duplicate() bool {
	return false
}

func (m *mockMessage) Qos() byte {
	return m.qos
}

func (m *mockMessage) Retained() bool {
	return m.retained
}

func (m *mockMessage) Topic() string {
	return m.topic
}

func (m *mockMessage) MessageID() uint16 {
	return m.id
}

func (m *mockMessage) Payload() []byte {
	return m.payload.([]byte)
}

func (m *mockClient) AddRoute(topic string, h mqtt.MessageHandler) {
	return
}

func (m *mockClient) IsConnected() bool {
	m.Lock()
	defer m.Unlock()
	return m.connected
}

func (m *mockClient) IsConnectionOpen() bool {
	m.Lock()
	defer m.Unlock()
	return m.connected
}

func (m *mockClient) Connect() mqtt.Token {
	m.Lock()
	defer m.Unlock()

	if m.connected {
		return nil
	}

	m.connected = true
	m.exit = make(chan bool)

	tk := newToken()
	tk.err = nil
	tk.release()
	return tk
}

func (m *mockClient) Disconnect(uint) {
	m.Lock()
	defer m.Unlock()

	if !m.connected {
		return
	}

	m.connected = false

	select {
	case <-m.exit:
		return
	default:
		close(m.exit)
	}
}

func (m *mockClient) Publish(topic string, qos byte, retained bool, payload interface{}) mqtt.Token {
	m.Lock()
	defer m.Unlock()

	if !m.connected {
		return nil
	}

	msg := newMockMessage(topic, qos, retained, payload)

	for _, sub := range m.subs[topic] {
		sub(m, msg)
	}

	tk := newToken()
	tk.err = nil
	tk.release()
	return tk
}

func (m *mockClient) Subscribe(topic string, qos byte, h mqtt.MessageHandler) mqtt.Token {
	m.Lock()
	defer m.Unlock()

	if !m.connected {
		return nil
	}

	m.subs[topic] = append(m.subs[topic], h)

	tk := newToken()
	tk.err = nil
	tk.release()
	return tk
}

func (m *mockClient) SubscribeMultiple(topics map[string]byte, h mqtt.MessageHandler) mqtt.Token {
	m.Lock()
	defer m.Unlock()

	if !m.connected {
		return nil
	}

	for topic := range topics {
		m.subs[topic] = append(m.subs[topic], h)
	}

	return &mqtt.SubscribeToken{}
}

func (m *mockClient) Unsubscribe(topics ...string) mqtt.Token {
	m.Lock()
	defer m.Unlock()

	if !m.connected {
		return nil
	}

	for _, topic := range topics {
		delete(m.subs, topic)
	}

	return &mqtt.UnsubscribeToken{}
}

func (m *mockClient) OptionsReader() mqtt.ClientOptionsReader {
	return mqtt.ClientOptionsReader{}
}

type mockToken struct {
	err  error
	done chan struct{}
}

func newToken() *mockToken {
	return &mockToken{
		done: make(chan struct{}),
	}
}

func (t *mockToken) release() {
	close(t.done)
}

func (t *mockToken) Wait() bool {
	<-t.done
	return true
}

func (t *mockToken) WaitTimeout(d time.Duration) bool {
	select {
	case <-t.done:
		return true
	case <-time.After(d):
		return false
	}
}

func (t *mockToken) Done() <-chan struct{} {
	return t.done
}

func (t *mockToken) Error() error {
	return t.err
}
