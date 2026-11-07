package whatsappgateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/costa/polypod/internal/humanloop"
	"go.mau.fi/whatsmeow"
)

func outgoingPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func outgoingOGG() []byte {
	first := make([]byte, 27+1+19)
	copy(first, "OggS")
	first[26], first[27] = 1, 19
	copy(first[28:], "OpusHead")
	last := make([]byte, 27+1+4)
	copy(last, "OggS")
	last[26], last[27] = 1, 4
	binary.LittleEndian.PutUint64(last[6:14], 48_000)
	return append(first, last...)
}

func TestQueueMediaPersistsAndRetriesStableID(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "inbox.json")
	store, err := humanloop.NewStore(journal, 20)
	if err != nil {
		t.Fatal(err)
	}
	driver := &fakeDriver{fail: true}
	wakes := 0
	const owner = "12345@s.whatsapp.net"
	g, err := New(store, driver, []string{owner}, []string{owner}, func() { wakes++ })
	if err != nil {
		t.Fatal(err)
	}
	data := outgoingPNG(t)
	queued, err := g.QueueMedia(owner, "foto", "foto.png", "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	if wakes != 1 || len(driver.sent) != 0 || queued.Media == nil {
		t.Fatalf("queue = %+v, wakes=%d, sent=%d", queued, wakes, len(driver.sent))
	}
	store, err = humanloop.NewStore(journal, 20)
	if err != nil {
		t.Fatal(err)
	}
	g, err = New(store, driver, []string{owner}, []string{owner}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, messages := store.Snapshot()
	if len(messages) != 1 || messages[0].ID != queued.ID {
		t.Fatalf("reloaded queue = %+v", messages)
	}
	if err := g.Deliver(context.Background(), messages[0]); err == nil {
		t.Fatal("fake offline send should fail")
	}
	driver.fail = false
	if err := g.Deliver(context.Background(), messages[0]); err != nil {
		t.Fatal(err)
	}
	if len(driver.sent) != 2 || driver.sent[0].ID != driver.sent[1].ID || driver.sent[0].Media == nil ||
		!bytes.Equal(driver.sent[0].Media.Data, data) || driver.sent[1].Media.Kind != "image" {
		t.Fatalf("retry changed media or ID: %+v", driver.sent)
	}
	if _, err := g.QueueMedia("other@s.whatsapp.net", "", "voice.ogg", "audio/ogg", outgoingOGG()); err == nil {
		t.Fatal("unauthorized media destination accepted")
	}
}

func TestQueueLocalFileCopiesBeforeSourceDeletion(t *testing.T) {
	store, err := humanloop.NewStore(filepath.Join(t.TempDir(), "inbox.json"), 20)
	if err != nil {
		t.Fatal(err)
	}
	const owner = "12345@s.whatsapp.net"
	g, err := New(store, &fakeDriver{}, []string{owner}, []string{owner}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n%%EOF"), 0600); err != nil {
		t.Fatal(err)
	}
	queued, err := g.QueueLocalFile(owner, path, "relatorio")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadOutboundMedia(queued)
	if err != nil || !bytes.Equal(got, []byte("%PDF-1.7\n%%EOF")) {
		t.Fatalf("copy unavailable after source removal: %q, %v", got, err)
	}
	if _, err := g.QueueMedia(owner, "caption", "voice.ogg", "audio/ogg", outgoingOGG()); err == nil {
		t.Fatal("audio caption should be rejected")
	}
	if _, err := g.QueueMedia(owner, "", "../voice.ogg", "audio/ogg", outgoingOGG()); err == nil {
		t.Fatal("unsafe filename should be rejected")
	}
	if _, err := g.QueueMedia(owner, "", "voice.pdf", "audio/ogg", outgoingOGG()); err == nil {
		t.Fatal("filename MIME mismatch should be rejected")
	}
	link := filepath.Join(t.TempDir(), "report-link.pdf")
	if err := os.Symlink(filepath.Join(t.TempDir(), "target.pdf"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := g.QueueLocalFile(owner, link, ""); err == nil {
		t.Fatal("symlink source should be rejected")
	}
}

type fakeUploader struct {
	kind whatsmeow.MediaType
	data []byte
	err  error
}

func (u *fakeUploader) Upload(_ context.Context, data []byte, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	u.kind = kind
	u.data = append([]byte(nil), data...)
	return whatsmeow.UploadResponse{URL: "https://example.invalid/media", DirectPath: "/m", FileLength: uint64(len(data)),
		MediaKey: []byte{1}, FileSHA256: []byte{2}, FileEncSHA256: []byte{3}}, u.err
}

func TestBuildOutboundMediaMessagesWithoutNetwork(t *testing.T) {
	for _, tc := range []struct {
		name, mime, filename, caption, kind string
		data                                []byte
		want                                whatsmeow.MediaType
	}{
		{"voice", "audio/ogg; codecs=opus", "voice.ogg", "", "audio", outgoingOGG(), whatsmeow.MediaAudio},
		{"image", "image/png", "image.png", "photo", "image", outgoingPNG(t), whatsmeow.MediaImage},
		{"document", "application/pdf", "file.pdf", "report", "document", []byte("%PDF-1.7\n%%EOF"), whatsmeow.MediaDocument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uploader := &fakeUploader{}
			message, err := buildOutboundMessage(context.Background(), uploader, Outbound{
				Text: tc.caption, Media: &OutboundMedia{Kind: tc.kind, MIME: tc.mime, Filename: tc.filename, Data: tc.data},
			})
			if err != nil {
				t.Fatal(err)
			}
			if uploader.kind != tc.want || !bytes.Equal(uploader.data, tc.data) {
				t.Fatalf("upload = %s %d bytes", uploader.kind, len(uploader.data))
			}
			switch tc.kind {
			case "audio":
				if message.GetAudioMessage() == nil || !message.GetAudioMessage().GetPTT() ||
					message.GetAudioMessage().GetMimetype() != tc.mime || message.GetAudioMessage().GetSeconds() != 1 {
					t.Fatalf("audio protobuf = %+v", message.GetAudioMessage())
				}
			case "image":
				if message.GetImageMessage() == nil || message.GetImageMessage().GetCaption() != tc.caption {
					t.Fatalf("image protobuf = %+v", message.GetImageMessage())
				}
			case "document":
				if message.GetDocumentMessage() == nil || message.GetDocumentMessage().GetFileName() != tc.filename {
					t.Fatalf("document protobuf = %+v", message.GetDocumentMessage())
				}
			}
		})
	}
}
