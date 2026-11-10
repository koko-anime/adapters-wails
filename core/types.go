// Package core zawiera logikę serwisu Koko dla Wails 3 bez zależności od Wails:
// dostęp do plików w piaskownicy, trasy HTTP dla danych binarnych oraz interfejsy
// G2P, syntezy i kodowania audio, które implementuje aplikacja.
package core

import "context"

// Segment to segment kanoniczny wypowiedzi (Koko-Pack §6.2).
type Segment struct {
	// T: "text", "ipa" albo "pause".
	T string `json:"t"`
	// S: tekst albo IPA (dla "text" i "ipa").
	S string `json:"s,omitempty"`
	// G: segment doklejony do poprzedniego bez spacji.
	G bool `json:"g,omitempty"`
	// Ms: długość pauzy w ms (dla "pause").
	Ms int `json:"ms,omitempty"`
}

// LexiconEntry to wpis leksykonu po rozwiązaniu (odcinek + seria).
type LexiconEntry struct {
	Key    string `json:"key"`
	Stem   bool   `json:"stem"`
	Kind   string `json:"kind"` // "ipa" albo "respell"
	Value  string `json:"value"`
	Origin string `json:"origin"` // "episode" albo "series"
}

// Lexicon to leksykon przekazywany do G2P.
type Lexicon struct {
	Entries []LexiconEntry `json:"entries"`
}

// G2P zamienia tekst na fonemy (np. Phonemis).
type G2P interface {
	// ToIPA zwraca IPA dla segmentów (jeden ciąg na wypowiedź lub część).
	ToIPA(ctx context.Context, segments []Segment, lexicon Lexicon) ([]string, error)
	// TokenCount zwraca liczbę tokenów fonemów (limit 510).
	TokenCount(ctx context.Context, ipa string) (int, error)
	// Vocabulary zwraca symbole IPA obecne w słowniku modelu.
	Vocabulary(ctx context.Context) ([]string, error)
}

// SynthRequest to żądanie syntezy jednej wypowiedzi.
type SynthRequest struct {
	IPA    []string `json:"ipa"`
	Voice  string   `json:"voice"`
	Speed  float64  `json:"speed"`
	GainDb float64  `json:"gainDb"` // informacyjnie — głośność stosuje biblioteka
}

// SynthResult to PCM mono float32 bez zastosowanego gain.
type SynthResult struct {
	PCM        []float32
	SampleRate int
}

// SynthInfo to tożsamość silnika syntezy (manifest.synthesis, klucze wypowiedzi).
type SynthInfo struct {
	Engine    string `json:"engine,omitempty"`
	Model     string `json:"model"`
	ModelHash string `json:"modelHash,omitempty"`
	G2P       string `json:"g2p"`
}

// Synthesizer syntezuje mowę (np. plkokoro).
type Synthesizer interface {
	Synthesize(ctx context.Context, req SynthRequest) (SynthResult, error)
	Info(ctx context.Context) (SynthInfo, error)
}

// MP3Options to ustawienia kodowania MP3 (CBR, bez rezerwuaru bitów).
type MP3Options struct {
	SampleRate  int
	Channels    int
	BitrateKbps int
	// RawFrames: same ramki audio, bez ID3 i ramki Xing/Info (podmiana ramek).
	RawFrames bool
}

// OpusOptions to ustawienia kodowania Ogg Opus.
type OpusOptions struct {
	SampleRate  int // częstotliwość wejściowego PCM
	Channels    int
	BitrateKbps int
}

// Encoder koduje i dekoduje audio. Domyślna implementacja: FFmpeg.
type Encoder interface {
	MP3(ctx context.Context, pcm []float32, o MP3Options) ([]byte, error)
	Opus(ctx context.Context, pcm []float32, o OpusOptions) ([]byte, error)
	// Decode zwraca PCM mono bez kompensacji opóźnienia enkodera i jego częstotliwość.
	Decode(ctx context.Context, data []byte, codec string) ([]float32, int, error)
}

// Capabilities mówi frontendowi, które capability są dostępne.
type Capabilities struct {
	FS      bool `json:"fs"`
	G2P     bool `json:"g2p"`
	Synth   bool `json:"synth"`
	Encoder bool `json:"encoder"`
}
