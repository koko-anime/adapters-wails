package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// FFmpeg to domyślny Encoder: libmp3lame (CBR, bez rezerwuaru bitów) i libopus.
// Pliki pośrednie trafiają do katalogu tymczasowego, bo muxer MP3 aktualizuje
// ramkę Info/LAME (padding) dopiero na końcu, przez seek.
type FFmpeg struct {
	// Bin: ścieżka do ffmpeg.
	Bin string
	// Probe: ścieżka do ffprobe.
	Probe string
}

// NewFFmpeg szuka ffmpeg i ffprobe (bin == "" → PATH).
func NewFFmpeg(bin string) (*FFmpeg, error) {
	if bin == "" {
		bin = "ffmpeg"
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return nil, err
	}
	probe := filepath.Join(filepath.Dir(p), "ffprobe"+filepath.Ext(p))
	if _, err := os.Stat(probe); err != nil {
		if pp, err := exec.LookPath("ffprobe"); err == nil {
			probe = pp
		} else {
			probe = ""
		}
	}
	return &FFmpeg{Bin: p, Probe: probe}, nil
}

func (f *FFmpeg) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, f.Bin, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func pcmBytes(pcm []float32) []byte {
	b := make([]byte, 4*len(pcm))
	for i, v := range pcm {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(v))
	}
	return b
}

func (f *FFmpeg) encode(ctx context.Context, pcm []float32, sr, ch int, ext string, codecArgs ...string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "koko-ffmpeg-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.f32")
	out := filepath.Join(dir, "out."+ext)
	if err := os.WriteFile(in, pcmBytes(pcm), 0o600); err != nil {
		return nil, err
	}
	args := []string{"-f", "f32le", "-ar", strconv.Itoa(sr), "-ac", strconv.Itoa(ch), "-i", in}
	args = append(args, codecArgs...)
	args = append(args, out)
	if err := f.run(ctx, args...); err != nil {
		return nil, err
	}
	return os.ReadFile(out)
}

// MP3 koduje PCM do MP3 CBR bez rezerwuaru bitów (warunek `spliceable`, Koko-Pack §4.1).
func (f *FFmpeg) MP3(ctx context.Context, pcm []float32, o MP3Options) ([]byte, error) {
	xing := "1"
	if o.RawFrames {
		xing = "0"
	}
	if o.Channels == 0 {
		o.Channels = 1
	}
	return f.encode(ctx, pcm, o.SampleRate, o.Channels, "mp3",
		"-c:a", "libmp3lame", "-b:a", strconv.Itoa(o.BitrateKbps)+"k", "-reservoir", "0",
		"-write_xing", xing, "-id3v2_version", "0", "-f", "mp3")
}

// Opus koduje PCM do Ogg Opus (48 kHz).
func (f *FFmpeg) Opus(ctx context.Context, pcm []float32, o OpusOptions) ([]byte, error) {
	if o.Channels == 0 {
		o.Channels = 1
	}
	return f.encode(ctx, pcm, o.SampleRate, o.Channels, "opus",
		"-c:a", "libopus", "-b:a", strconv.Itoa(o.BitrateKbps)+"k", "-ar", "48000", "-f", "ogg")
}

// Decode dekoduje plik do PCM mono w natywnej częstotliwości; dla MP3 bez kompensacji
// opóźnienia enkodera (`-flags2 +skip_manual`), zgodnie z definicją encoderDelay.
func (f *FFmpeg) Decode(ctx context.Context, data []byte, codec string) ([]float32, int, error) {
	dir, err := os.MkdirTemp("", "koko-ffmpeg-")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(dir)
	ext := "mp3"
	if codec == "opus" {
		ext = "ogg"
	}
	in := filepath.Join(dir, "in."+ext)
	out := filepath.Join(dir, "out.f32")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, 0, err
	}
	sr, err := f.sampleRate(ctx, in)
	if err != nil {
		return nil, 0, err
	}
	args := []string{}
	if ext == "mp3" {
		args = append(args, "-flags2", "+skip_manual")
	}
	args = append(args, "-i", in, "-f", "f32le", "-ac", "1", "-ar", strconv.Itoa(sr), out)
	if err := f.run(ctx, args...); err != nil {
		return nil, 0, err
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, 0, err
	}
	pcm := make([]float32, len(b)/4)
	for i := range pcm {
		pcm[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return pcm, sr, nil
}

func (f *FFmpeg) sampleRate(ctx context.Context, path string) (int, error) {
	if f.Probe == "" {
		return 0, fmt.Errorf("ffprobe: brak")
	}
	cmd := exec.CommandContext(ctx, f.Probe, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=sample_rate", "-of", "csv=p=0", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}
