package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/costa/polypod/internal/adapter"
)

// Adapter implements an interactive CLI chat channel.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Name() string { return "cli" }

func (a *Adapter) Start(ctx context.Context, handler adapter.MessageHandler) error {
	fmt.Println("\nPolypod CLI — digite sua mensagem (Ctrl+C para sair)")

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("voce> ")

		// Check context while waiting for input
		inputCh := make(chan string, 1)
		go func() {
			if scanner.Scan() {
				inputCh <- scanner.Text()
			} else {
				close(inputCh)
			}
		}()

		var text string
		select {
		case <-ctx.Done():
			fmt.Println("\nAte logo!")
			return nil
		case t, ok := <-inputCh:
			if !ok {
				fmt.Println("\nAte logo!")
				return nil
			}
			text = t
		}

		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if text == "/sair" || text == "/quit" || text == "/exit" {
			fmt.Println("Ate logo!")
			return nil
		}

		in := adapter.InMessage{
			ID:        fmt.Sprintf("cli-%d", time.Now().UnixNano()),
			Channel:   "cli",
			UserID:    "local",
			UserName:  "local",
			Text:      text,
			Timestamp: time.Now(),
		}

		out, err := handler(ctx, in)
		if err != nil {
			fmt.Printf("erro: %v\n", err)
			continue
		}

		fmt.Printf("\npolypod> %s\n\n", out.Text)
	}
}

func (a *Adapter) Send(ctx context.Context, msg adapter.OutMessage) error {
	fmt.Printf("polypod> %s\n", msg.Text)
	return nil
}
