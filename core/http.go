package core

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// Nagłówki odpowiedzi binarnych.
const (
	HeaderSampleRate = "X-Koko-Sample-Rate"
	HeaderDuration   = "X-Koko-Duration-Ms"
)

// ServeHTTP obsługuje dane binarne (montowane przez Wails pod `Route`, domyślnie /koko):
//
//	GET|HEAD /fs?path=…          plik (z obsługą Range)
//	PUT      /fs?path=…          zapis pliku (atomowy)
//	POST     /synth              JSON SynthRequest → PCM float32 LE
//	POST     /encode/mp3?…       PCM float32 LE → MP3
//	POST     /encode/opus?…      PCM float32 LE → Ogg Opus
//	POST     /decode?codec=…     plik → PCM float32 LE
func (c *Core) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/fs", "fs":
		c.serveFS(w, r)
	case "/synth", "synth":
		c.serveSynth(w, r)
	case "/encode/mp3", "encode/mp3", "/encode/opus", "encode/opus":
		c.serveEncode(w, r)
	case "/decode", "decode":
		c.serveDecode(w, r)
	default:
		http.NotFound(w, r)
	}
}

func httpError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrOutsideRoots):
		code = http.StatusForbidden
	case errors.Is(err, ErrRelativePath):
		code = http.StatusBadRequest
	case errors.Is(err, ErrUnavailable):
		code = http.StatusNotImplemented
	case os.IsNotExist(err):
		code = http.StatusNotFound
	}
	http.Error(w, err.Error(), code)
}

func (c *Core) serveFS(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		p, err := c.resolve(path, false)
		if err != nil {
			httpError(w, err)
			return
		}
		f, err := os.Open(p)
		if err != nil {
			httpError(w, err)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			http.Error(w, "not a file", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "", st.ModTime(), f)
	case http.MethodPut:
		p, err := c.resolve(path, true)
		if err != nil {
			httpError(w, err)
			return
		}
		tmp, err := os.CreateTemp(filepath.Dir(p), ".koko-*")
		if err != nil {
			httpError(w, err)
			return
		}
		name := tmp.Name()
		_, err = io.Copy(tmp, http.MaxBytesReader(w, r.Body, c.cfg.MaxBodyBytes))
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(name, p)
		}
		if err != nil {
			os.Remove(name)
			httpError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, HEAD, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (c *Core) serveSynth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.cfg.Synth == nil {
		httpError(w, ErrUnavailable)
		return
	}
	var req SynthRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := c.cfg.Synth.Synthesize(r.Context(), req)
	if err != nil {
		httpError(w, err)
		return
	}
	ms := 0
	if res.SampleRate > 0 {
		ms = int(math.Round(float64(len(res.PCM)) * 1000 / float64(res.SampleRate)))
	}
	w.Header().Set(HeaderSampleRate, strconv.Itoa(res.SampleRate))
	w.Header().Set(HeaderDuration, strconv.Itoa(ms))
	writePCM(w, res.PCM)
}

func intParam(r *http.Request, name string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
		return v
	}
	return def
}

func (c *Core) serveEncode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.cfg.Encoder == nil {
		httpError(w, ErrUnavailable)
		return
	}
	pcm, err := readPCM(http.MaxBytesReader(w, r.Body, c.cfg.MaxBodyBytes))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var out []byte
	sr := intParam(r, "sampleRate", 24000)
	ch := intParam(r, "channels", 1)
	if r.URL.Path == "/encode/opus" || r.URL.Path == "encode/opus" {
		out, err = c.cfg.Encoder.Opus(r.Context(), pcm, OpusOptions{SampleRate: sr, Channels: ch, BitrateKbps: intParam(r, "bitrate", 40)})
		w.Header().Set("Content-Type", "audio/ogg")
	} else {
		out, err = c.cfg.Encoder.MP3(r.Context(), pcm, MP3Options{SampleRate: sr, Channels: ch, BitrateKbps: intParam(r, "bitrate", 64), RawFrames: r.URL.Query().Get("raw") == "1"})
		w.Header().Set("Content-Type", "audio/mpeg")
	}
	if err != nil {
		httpError(w, err)
		return
	}
	w.Write(out)
}

func (c *Core) serveDecode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.cfg.Encoder == nil {
		httpError(w, ErrUnavailable)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, c.cfg.MaxBodyBytes))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pcm, sr, err := c.cfg.Encoder.Decode(r.Context(), data, r.URL.Query().Get("codec"))
	if err != nil {
		httpError(w, err)
		return
	}
	w.Header().Set(HeaderSampleRate, strconv.Itoa(sr))
	writePCM(w, pcm)
}

func writePCM(w http.ResponseWriter, pcm []float32) {
	w.Header().Set("Content-Type", "application/octet-stream")
	buf := make([]byte, 4*len(pcm))
	for i, v := range pcm {
		binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(v))
	}
	w.Write(buf)
}

func readPCM(r io.Reader) ([]float32, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(b)%4 != 0 {
		return nil, errors.New("koko: PCM musi mieć wielokrotność 4 bajtów (float32 LE)")
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out, nil
}
