// Command polypod-humanloop-replay requeues one failed inbound message while
// the Polypod service is stopped. Dry-run is the default.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/costa/polypod/internal/humanloop"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	storePath := flag.String("store", "", "caminho do humanloop.json")
	id := flag.String("id", "", "ID interno da entrada com falha")
	channel := flag.String("channel", "", "canal esperado, por exemplo whatsapp")
	action := flag.String("action", "replay", "replay ou resolve")
	reason := flag.String("reason", "", "motivo obrigatorio para encerrar uma falha")
	apply := flag.Bool("apply", false, "aplicar a acao; sem esta opcao apenas verifica")
	flag.Parse()
	if *storePath == "" || *id == "" || *channel == "" || flag.NArg() != 0 {
		return errors.New("uso: polypod-humanloop-replay -store ARQUIVO -id ID -channel CANAL [-action replay|resolve] [-reason MOTIVO] [-apply]")
	}
	if *action != "replay" && *action != "resolve" {
		return errors.New("acao invalida: use replay ou resolve")
	}
	if *action == "resolve" && strings.TrimSpace(*reason) == "" {
		return errors.New("motivo obrigatorio para resolver falha")
	}
	store, err := humanloop.OpenStoreForMaintenance(*storePath)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(*id))
	fingerprint := hex.EncodeToString(sum[:6])
	if *action == "resolve" {
		preview, err := store.PreviewFailedResolution(*id)
		if err != nil {
			return errors.New("item indisponivel ou sem falha terminal")
		}
		if preview.Channel != *channel {
			return errors.New("canal do item diferente do esperado")
		}
		if !*apply {
			fmt.Printf("verificacao: acao=resolve direcao=%s canal=%s tentativas=%d falha=%s entregue=%s fingerprint=%s\n",
				preview.Direction, preview.Channel, preview.Attempts, preview.FailedAt.Format(time.RFC3339),
				strings.Join(preview.Delivered, ","), fingerprint)
			return nil
		}
		if err := requireServiceStopped(); err != nil {
			return err
		}
		if err := store.ResolveFailed(*id, "offline-operator", *reason, time.Now()); err != nil {
			return errors.New("falha ao resolver item; caixa preservada")
		}
		fmt.Printf("falha encerrada sem replay: direcao=%s canal=%s fingerprint=%s\n",
			preview.Direction, preview.Channel, fingerprint)
		return nil
	}
	preview, err := store.PreviewFailedInbound(*id)
	if err != nil {
		return errors.New("entrada indisponivel ou sem falha recuperavel")
	}
	if preview.Channel != *channel {
		return errors.New("canal da entrada diferente do esperado")
	}
	if !*apply {
		fmt.Printf("verificacao: canal=%s tentativas=%d falha=%s replays=%d fingerprint=%s\n",
			preview.Channel, preview.Attempts, preview.FailedAt.Format(time.RFC3339), preview.PriorReplays, fingerprint)
		return nil
	}
	if err := requireServiceStopped(); err != nil {
		return err
	}
	if err := store.RequeueFailedInbound(*id, time.Now()); err != nil {
		return errors.New("falha ao reprocessar a entrada; caixa preservada")
	}
	fmt.Printf("recolocada na fila: canal=%s fingerprint=%s\n", preview.Channel, fingerprint)
	return nil
}

func requireServiceStopped() error {
	if runtime.GOOS == "linux" {
		state, _ := exec.Command("systemctl", "is-active", "polypod-root.service").Output()
		switch strings.TrimSpace(string(state)) {
		case "inactive", "failed":
			// The inbox has a single offline writer.
		default:
			return errors.New("pare polypod-root.service antes de alterar a caixa")
		}
	} else {
		return errors.New("manutencao administrativa exige Linux/systemd")
	}
	return nil
}
