package whatsappgateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/costa/polypod/internal/humanloop"
	"golang.org/x/sys/unix"
)

const mediaRetention = 7 * 24 * time.Hour

// CleanupMedia removes old native media that no active WhatsApp inbox item
// needs. It only opens the direct media child of the isolated session, never
// follows media-directory or file symlinks, and leaves newer files untouched.
// The returned count may be nonzero when a later file produces an error.
func (n *Native) CleanupMedia(ctx context.Context, g *Gateway, now time.Time) (removed int, cleanupErr error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if n == nil || g == nil || g.store == nil {
		return 0, errors.New("whatsapp: driver e caixa de entrada obrigatorios para limpeza de mídia")
	}
	if now.IsZero() {
		return 0, errors.New("whatsapp: horario obrigatorio para limpeza de mídia")
	}

	// Native media writes hold this lock through durable enqueue. A cleanup
	// snapshot cannot race a replay that rewrites an old hash-named file.
	n.mediaMu.Lock()
	defer n.mediaMu.Unlock()
	if err := n.Preflight(); err != nil {
		return 0, fmt.Errorf("whatsapp: validando sessao antes da limpeza: %w", err)
	}
	sessionDir, err := filepath.EvalSymlinks(n.SessionDir)
	if err != nil {
		return 0, fmt.Errorf("whatsapp: resolvendo sessao: %w", err)
	}
	sessionDir, err = filepath.Abs(sessionDir)
	if err != nil {
		return 0, err
	}
	sessionFD, err := unix.Open(sessionDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("whatsapp: abrindo sessao para limpeza: %w", err)
	}
	defer func() { cleanupErr = errors.Join(cleanupErr, unix.Close(sessionFD)) }()
	if err := privateDirectory(sessionFD); err != nil {
		return 0, err
	}

	mediaDir := filepath.Join(sessionDir, "media")
	mediaFD, err := unix.Openat(sessionFD, "media", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return 0, nil
	}
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return 0, ErrUnsafeMediaPath
	}
	if err != nil {
		return 0, fmt.Errorf("whatsapp: abrindo diretorio de mídia: %w", err)
	}
	media := os.NewFile(uintptr(mediaFD), mediaDir)
	defer func() { cleanupErr = errors.Join(cleanupErr, media.Close()) }()
	if err := privateDirectory(mediaFD); err != nil {
		return 0, err
	}

	_, messages := g.store.Snapshot()
	active := make(map[string]bool)
	for _, msg := range messages {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if msg.Channel != Channel || msg.Direction != humanloop.DirectionInbound ||
			msg.Status == humanloop.StatusCompleted || msg.Status == humanloop.StatusFailed {
			continue
		}
		event, err := Decode(msg)
		if err != nil {
			return 0, fmt.Errorf("whatsapp: decodificando anexo ativo %s: %w", msg.ID, err)
		}
		if event.MediaPath == "" {
			continue
		}
		if !filepath.IsAbs(event.MediaPath) || filepath.Clean(event.MediaPath) != event.MediaPath ||
			filepath.Dir(event.MediaPath) != mediaDir {
			return 0, fmt.Errorf("whatsapp: anexo ativo %s: %w", msg.ID, ErrUnsafeMediaPath)
		}
		active[filepath.Base(event.MediaPath)] = true
	}

	entries, err := media.ReadDir(-1)
	if err != nil {
		return 0, fmt.Errorf("whatsapp: listando mídia: %w", err)
	}
	cutoff := now.Add(-mediaRetention)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if active[entry.Name()] {
			continue
		}
		var info unix.Stat_t
		if err := unix.Fstatat(mediaFD, entry.Name(), &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			return removed, fmt.Errorf("whatsapp: verificando mídia %q: %w", entry.Name(), err)
		}
		if info.Mode&unix.S_IFMT != unix.S_IFREG || !time.Unix(info.Mtim.Sec, info.Mtim.Nsec).Before(cutoff) {
			continue
		}
		if err := unix.Unlinkat(mediaFD, entry.Name(), 0); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			return removed, fmt.Errorf("whatsapp: removendo mídia %q: %w", entry.Name(), err)
		}
		removed++
	}
	return removed, nil
}

func privateDirectory(fd int) error {
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil {
		return fmt.Errorf("whatsapp: verificando diretorio privado: %w", err)
	}
	if info.Mode&unix.S_IFMT != unix.S_IFDIR || info.Uid != uint32(os.Geteuid()) || info.Mode&0o022 != 0 {
		return fmt.Errorf("%w: uid=%d euid=%d modo=%#o", ErrUnsafeMediaPath, info.Uid, os.Geteuid(), info.Mode)
	}
	return nil
}
