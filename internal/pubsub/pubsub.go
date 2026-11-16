// Package pubsub — PubSub System from CRUSH/OPENCODE
package pubsub

type PubSub struct {
	subscribers map[string][]chan interface{}
}

func NewPubSub() *PubSub {
	return &PubSub{subscribers: make(map[string][]chan interface{})}
}

func (p *PubSub) Subscribe(topic string) chan interface{} {
	ch := make(chan interface{})
	p.subscribers[topic] = append(p.subscribers[topic], ch)
	return ch
}

func (p *PubSub) Publish(topic string, data interface{}) {
	for _, ch := range p.subscribers[topic] {
		ch <- data
	}
}
