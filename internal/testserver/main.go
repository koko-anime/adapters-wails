// Command testserver uruchamia core serwisu Koko bez Wails — dla testów integracyjnych
// części TypeScript. Emuluje most Wails: POST /call {"name":"<pkg>.Service.<Metoda>","args":[...]}
// woła metodę przez refleksję (jak Call.ByName), a trasa /koko obsługuje dane binarne.
//
// Użycie: testserver <katalog-piaskownicy>... — wypisuje adres na stdout.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"

	"github.com/seba9989/koko-adapters-wails/core"
)

const sampleRate = 24000

// fakeG2P: „IPA” = tekst segmentów, 1 token = 2 znaki (jak mockG2p w testach formats).
type fakeG2P struct{}

func (fakeG2P) ToIPA(_ context.Context, segs []core.Segment, _ core.Lexicon) ([]string, error) {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		if s.T == "pause" {
			parts = append(parts, " ")
		} else {
			parts = append(parts, s.S)
		}
	}
	return []string{strings.Join(parts, " ")}, nil
}

func (fakeG2P) TokenCount(_ context.Context, ipa string) (int, error) {
	return (len([]rune(ipa)) + 1) / 2, nil
}

func (fakeG2P) Vocabulary(context.Context) ([]string, error) {
	var v []string
	for _, r := range "abcdefghijklmnopqrstuvwxyzɔɲʑɕ" {
		v = append(v, string(r))
	}
	return v, nil
}

// fakeSynth: ton 55 ms na znak / prędkość.
type fakeSynth struct{}

func (fakeSynth) Synthesize(_ context.Context, req core.SynthRequest) (core.SynthResult, error) {
	text := strings.Join(req.IPA, " ")
	ms := math.Round(float64(len([]rune(text))) * 55 / req.Speed)
	n := int(math.Round(ms * sampleRate / 1000))
	f := 200 + float64(len([]rune(text))%7)*50
	pcm := make([]float32, n)
	for i := range pcm {
		pcm[i] = float32(0.25 * math.Sin(2*math.Pi*f*float64(i)/sampleRate))
	}
	return core.SynthResult{PCM: pcm, SampleRate: sampleRate}, nil
}

func (fakeSynth) Info(context.Context) (core.SynthInfo, error) {
	return core.SynthInfo{Engine: "fake 1.0", Model: "fake-model", G2P: "fake-g2p 1.0"}, nil
}

var ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()

// call emuluje Call.ByName: ostatni człon nazwy to metoda, wiodący context jest wstrzykiwany.
func call(c *core.Core, w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string            `json:"name"`
		Args []json.RawMessage `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	method := reflect.ValueOf(c).MethodByName(req.Name[strings.LastIndex(req.Name, ".")+1:])
	if !method.IsValid() {
		http.Error(w, "unknown method "+req.Name, http.StatusNotFound)
		return
	}
	t := method.Type()
	in := []reflect.Value{}
	first := 0
	if t.NumIn() > 0 && t.In(0) == ctxType {
		in = append(in, reflect.ValueOf(r.Context()))
		first = 1
	}
	if len(req.Args) != t.NumIn()-first {
		http.Error(w, fmt.Sprintf("%s: want %d args, got %d", req.Name, t.NumIn()-first, len(req.Args)), http.StatusBadRequest)
		return
	}
	for i, raw := range req.Args {
		v := reflect.New(t.In(first + i))
		if err := json.Unmarshal(raw, v.Interface()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		in = append(in, v.Elem())
	}
	out := method.Call(in)
	if last := out[len(out)-1]; last.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		if !last.IsNil() {
			http.Error(w, last.Interface().(error).Error(), http.StatusInternalServerError)
			return
		}
		out = out[:len(out)-1]
	}
	var result any
	if len(out) > 0 {
		result = out[0].Interface()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func main() {
	c, err := core.New(core.Config{Roots: os.Args[1:], G2P: fakeG2P{}, Synth: fakeSynth{}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.Handle("/koko/", http.StripPrefix("/koko", c))
	mux.HandleFunc("POST /call", func(w http.ResponseWriter, r *http.Request) { call(c, w, r) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("http://" + ln.Addr().String())
	_ = http.Serve(ln, mux)
}
