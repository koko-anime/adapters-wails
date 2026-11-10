package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

type fakeSynth struct{}

func (fakeSynth) Synthesize(_ context.Context, req SynthRequest) (SynthResult, error) {
	n := int(24000 * 0.5 / req.Speed)
	pcm := make([]float32, n)
	for i := range pcm {
		pcm[i] = float32(0.2 * math.Sin(2*math.Pi*440*float64(i)/24000))
	}
	return SynthResult{PCM: pcm, SampleRate: 24000}, nil
}
func (fakeSynth) Info(context.Context) (SynthInfo, error) {
	return SynthInfo{Model: "fake", G2P: "fake-g2p"}, nil
}

func newCore(t *testing.T) (*Core, string) {
	t.Helper()
	root := t.TempDir()
	c, err := New(Config{Roots: []string{root}, Synth: fakeSynth{}})
	if err != nil {
		t.Fatal(err)
	}
	return c, root
}

func TestSandbox(t *testing.T) {
	c, root := newCore(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o600)
	p := filepath.ToSlash(filepath.Join(root, "a.ass"))

	if err := c.WriteText(p, "zażółć"); err != nil {
		t.Fatal(err)
	}
	if s, err := c.ReadText(p); err != nil || s != "zażółć" {
		t.Fatalf("ReadText = %q, %v", s, err)
	}
	if ok, _ := c.Exists(p); !ok {
		t.Fatal("Exists = false")
	}
	if _, err := c.ReadText("a.ass"); !errors.Is(err, ErrRelativePath) {
		t.Fatalf("względna: %v", err)
	}
	if _, err := c.ReadText(filepath.Join(outside, "secret.txt")); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("poza piaskownicą: %v", err)
	}
	if _, err := c.ReadText(filepath.ToSlash(root) + "/../" + filepath.Base(outside) + "/secret.txt"); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("..: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err == nil {
		if _, err := c.ReadText(filepath.Join(root, "link", "secret.txt")); !errors.Is(err, ErrOutsideRoots) {
			t.Fatalf("symlink: %v", err)
		}
		if err := c.WriteText(filepath.Join(root, "link", "new.txt"), "x"); !errors.Is(err, ErrOutsideRoots) {
			t.Fatalf("zapis przez symlink: %v", err)
		}
	}
	if ok, err := c.Exists(filepath.Join(outside, "secret.txt")); ok || err != nil {
		t.Fatalf("Exists poza = %v, %v", ok, err)
	}
	if err := AddRoot(c, outside); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadText(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatalf("po AddRoot: %v", err)
	}
}

func TestEmptyRootsDenyAll(t *testing.T) {
	c, _ := New(Config{})
	if c.Capabilities().FS {
		t.Fatal("FS powinno być wyłączone")
	}
	if _, err := c.ReadText("/etc/passwd"); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("%v", err)
	}
}

func TestHTTPFS(t *testing.T) {
	c, root := newCore(t)
	srv := httptest.NewServer(c)
	defer srv.Close()
	p := filepath.ToSlash(filepath.Join(root, "audio.bin"))
	data := bytes.Repeat([]byte{1, 2, 3, 4}, 1000)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/fs?path="+p, bytes.NewReader(data))
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT: %v %v", err, res.Status)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/fs?path="+p, nil)
	req.Header.Set("Range", "bytes=4-7")
	res, _ = http.DefaultClient.Do(req)
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusPartialContent || !bytes.Equal(b, []byte{1, 2, 3, 4}) {
		t.Fatalf("Range: %v %v", res.Status, b)
	}
	res, _ = http.Get(srv.URL + "/fs?path=/etc/passwd")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("poza: %v", res.Status)
	}
	res, _ = http.Get(srv.URL + "/fs?path=" + filepath.ToSlash(filepath.Join(root, "brak")))
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("brak: %v", res.Status)
	}
}

func TestHTTPSynthAndCodecs(t *testing.T) {
	c, _ := newCore(t)
	srv := httptest.NewServer(c)
	defer srv.Close()
	body, _ := json.Marshal(SynthRequest{IPA: []string{"a"}, Voice: "v", Speed: 1})
	res, err := http.Post(srv.URL+"/synth", "application/json", bytes.NewReader(body))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("synth: %v %v", err, res.Status)
	}
	pcm, _ := io.ReadAll(res.Body)
	if len(pcm) != 4*12000 || res.Header.Get(HeaderSampleRate) != "24000" || res.Header.Get(HeaderDuration) != "500" {
		t.Fatalf("synth: %d B, %s, %s", len(pcm), res.Header.Get(HeaderSampleRate), res.Header.Get(HeaderDuration))
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("brak ffmpeg")
	}
	res, err = http.Post(srv.URL+"/encode/mp3?sampleRate=24000&channels=1&bitrate=64", "application/octet-stream", bytes.NewReader(pcm))
	if err != nil || res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("mp3: %v %v %s", err, res.Status, b)
	}
	mp3, _ := io.ReadAll(res.Body)
	if len(mp3) == 0 || len(mp3)%192 != 0 {
		t.Fatalf("mp3: %d B (oczekiwane ramki 192 B)", len(mp3))
	}
	res, _ = http.Post(srv.URL+"/decode?codec=mp3", "audio/mpeg", bytes.NewReader(mp3))
	dec, _ := io.ReadAll(res.Body)
	if sr, _ := strconv.Atoi(res.Header.Get(HeaderSampleRate)); sr != 24000 || len(dec)/4 != (len(mp3)/192-1)*576 /* bez ramki Info */ {
		t.Fatalf("decode: %d Hz, %d próbek", sr, len(dec)/4)
	}
	res, _ = http.Post(srv.URL+"/encode/opus?sampleRate=24000&channels=1&bitrate=32", "application/octet-stream", bytes.NewReader(pcm))
	ogg, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !bytes.HasPrefix(ogg, []byte("OggS")) {
		t.Fatalf("opus: %v", res.Status)
	}
}

func TestUnavailable(t *testing.T) {
	c, _ := New(Config{Roots: []string{t.TempDir()}})
	if _, err := c.G2PVocabulary(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c)
	defer srv.Close()
	res, _ := http.Post(srv.URL+"/synth", "application/json", bytes.NewReader([]byte("{}")))
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatal(res.Status)
	}
}
