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
	apply := flag.Bool("apply", false, "recolocar na fila; sem esta opcao apenas verifica")
	flag.Parse()
	if *storePath == "" || *id == "" || *channel == "" || flag.NArg() != 0 {
		return errors.New("uso: polypod-humanloop-replay -store ARQUIVO -id ID -channel CANAL [-apply]")
	}
	store, err := humanloop.OpenStoreForMaintenance(*storePath)
	if err != nil {
		return err
	}
	preview, err := store.PreviewFailedInbound(*id)
	if err != nil {
		return errors.New("entrada indisponivel ou sem falha recuperavel")
	}
	if preview.Channel != *channel {
		return errors.New("canal da entrada diferente do esperado")
	}
	sum := sha256.Sum256([]byte(*id))
	fingerprint := hex.EncodeToString(sum[:6])
	if !*apply {
		fmt.Printf("verificacao: canal=%s tentativas=%d falha=%s replays=%d fingerprint=%s\n",
			preview.Channel, preview.Attempts, preview.FailedAt.Format(time.RFC3339), preview.PriorReplays, fingerprint)
		return nil
	}
	if runtime.GOOS == "linux" {
		state, _ := exec.Command("systemctl", "is-active", "polypod-root.service").Output()
		switch strings.TrimSpace(string(state)) {
		case "inactive", "failed":
			// The inbox has a single offline writer.
		default:
			return errors.New("pare polypod-root.service antes de reprocessar a entrada")
		}
	} else {
		return errors.New("reprocessamento administrativo exige Linux/systemd")
	}
	if err := store.RequeueFailedInbound(*id, time.Now()); err != nil {
		return errors.New("falha ao reprocessar a entrada; caixa preservada")
	}
	fmt.Printf("recolocada na fila: canal=%s fingerprint=%s\n", preview.Channel, fingerprint)
	return nil
}
