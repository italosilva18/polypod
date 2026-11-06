package humanloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ProcessFunc executes one inbound message through the complete agent loop.
type ProcessFunc func(ctx context.Context, msg Message) (string, error)

// DeliverFunc sends one outbound message through its remote relay or notifier.
type DeliverFunc func(ctx context.Context, msg Message) error

// ProcessorConfig controls the durable heartbeat worker.
type ProcessorConfig struct {
	Store        *Store
	Interval     time.Duration
	Timeout      time.Duration
	MaxAttempts  int
	RetryDelays  []time.Duration
	Process      ProcessFunc
	Deliver      DeliverFunc
	Logger       *slog.Logger
	MaxCycleWork int
}

// Processor wakes on a heartbeat, drains due inbox/outbox work, and persists
// every state transition. Only one processing cycle can run at a time.
type Processor struct {
	cfg ProcessorConfig

	cycleMu sync.Mutex
	lifeMu  sync.Mutex
	cancel  context.CancelFunc
	wakeCh  chan struct{}
	wg      sync.WaitGroup
}

// NewProcessor validates and fills processor defaults.
func NewProcessor(cfg ProcessorConfig) (*Processor, error) {
	if cfg.Store == nil {
		return nil, errors.New("humanloop: store e obrigatorio")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Minute
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 4
	}
	if len(cfg.RetryDelays) == 0 {
		cfg.RetryDelays = []time.Duration{30 * time.Second, time.Minute, 5 * time.Minute}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxCycleWork <= 0 {
		cfg.MaxCycleWork = 100
	}
	return &Processor{cfg: cfg, wakeCh: make(chan struct{}, 1)}, nil
}

// Start launches the heartbeat and performs an immediate recovery cycle.
func (p *Processor) Start(parent context.Context) {
	p.lifeMu.Lock()
	if p.cancel != nil {
		p.lifeMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancel = cancel
	p.wg.Add(1)
	p.lifeMu.Unlock()

	go p.run(ctx)
}

// Stop cancels the worker and waits for the active cycle to finish.
func (p *Processor) Stop() {
	p.lifeMu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.lifeMu.Unlock()
	if cancel != nil {
		cancel()
		p.wg.Wait()
	}
}

// Wake requests an early heartbeat after new remote work arrives.
func (p *Processor) Wake() {
	select {
	case p.wakeCh <- struct{}{}:
	default:
	}
}

func (p *Processor) run(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()

	p.processAndRecord(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.processAndRecord(ctx)
		case <-p.wakeCh:
			p.processAndRecord(ctx)
		}
	}
}

func (p *Processor) processAndRecord(ctx context.Context) {
	err := p.ProcessNow(ctx)
	if recordErr := p.cfg.Store.RecordHeartbeat(time.Now(), err); recordErr != nil {
		p.cfg.Logger.Error("humanloop: falha ao registrar heartbeat", "error", recordErr)
	}
	if err != nil {
		p.cfg.Logger.Warn("humanloop: heartbeat terminou com erro", "error", err)
	}
}

// ProcessNow synchronously drains all currently due work up to MaxCycleWork.
func (p *Processor) ProcessNow(ctx context.Context) error {
	p.cycleMu.Lock()
	defer p.cycleMu.Unlock()

	var cycleErr error
	for count := 0; count < p.cfg.MaxCycleWork; count++ {
		msg, ok, err := p.cfg.Store.ClaimNextDue(time.Now())
		if err != nil {
			return err
		}
		if !ok {
			return cycleErr
		}
		if err := p.handle(ctx, msg); err != nil {
			cycleErr = errors.Join(cycleErr, err)
		}
	}
	return errors.Join(cycleErr, fmt.Errorf("humanloop: limite de %d itens por ciclo atingido", p.cfg.MaxCycleWork))
}

func (p *Processor) handle(parent context.Context, msg Message) error {
	ctx, cancel := context.WithTimeout(parent, p.cfg.Timeout)
	defer cancel()

	if msg.Direction == DirectionInbound {
		if p.cfg.Process == nil {
			return p.fail(msg, errors.New("processador do agente nao configurado"))
		}
		response, err := p.cfg.Process(ctx, msg)
		if err != nil {
			return p.fail(msg, err)
		}
		if response != "" {
			if _, err := p.cfg.Store.EnqueueOutbound(msg.Channel, msg.UserID, response, msg.ID); err != nil {
				return p.fail(msg, fmt.Errorf("enfileirando resposta: %w", err))
			}
		}
		return p.cfg.Store.Complete(msg.ID, response, time.Now())
	}

	if msg.Direction == DirectionOutbound {
		if p.cfg.Deliver == nil {
			return p.fail(msg, errors.New("entregador remoto nao configurado"))
		}
		if err := p.cfg.Deliver(ctx, msg); err != nil {
			return p.fail(msg, err)
		}
		return p.cfg.Store.Complete(msg.ID, "", time.Now())
	}
	return p.fail(msg, fmt.Errorf("direcao invalida: %s", msg.Direction))
}

func (p *Processor) fail(msg Message, cause error) error {
	delayIndex := msg.Attempts - 1
	if delayIndex < 0 {
		delayIndex = 0
	}
	if delayIndex >= len(p.cfg.RetryDelays) {
		delayIndex = len(p.cfg.RetryDelays) - 1
	}
	next := time.Now().Add(p.cfg.RetryDelays[delayIndex])
	if err := p.cfg.Store.Retry(msg.ID, cause, next, p.cfg.MaxAttempts); err != nil {
		return errors.Join(cause, err)
	}
	return fmt.Errorf("mensagem %s (tentativa %d): %w", msg.ID, msg.Attempts, cause)
}
