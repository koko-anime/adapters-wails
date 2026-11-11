// Package kokowails to serwis Wails 3 dla @koko-anime/formats: pliki (w piaskownicy),
// G2P, synteza i kodowanie audio dostępne dla frontendu przez plugin
// `@koko-anime/adapters-wails`.
//
//	koko, err := kokowails.New(kokowails.Config{
//		Roots: []string{projectDir},
//		G2P:   myPhonemis, // implementacja kokowails.G2P
//		Synth: myKokoro,   // implementacja kokowails.Synthesizer
//	})
//	app := application.New(application.Options{
//		Services: []application.Service{kokowails.Register(koko, kokowails.DefaultRoute)},
//	})
//
// Wails wystawia frontendowi wszystkie eksportowane metody serwisu, dlatego operacje
// administracyjne (AddRoot, Register) są funkcjami pakietu, a nie metodami.
package kokowails

import (
	"context"

	"github.com/koko-anime/adapters-wails/core"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// DefaultRoute to trasa HTTP serwisu (dane binarne) — musi zgadzać się z opcją `route` pluginu TS.
const DefaultRoute = "/koko"

// Aliasy typów z pakietu core, żeby aplikacja importowała jeden pakiet.
type (
	Config       = core.Config
	G2P          = core.G2P
	Synthesizer  = core.Synthesizer
	Encoder      = core.Encoder
	Segment      = core.Segment
	Lexicon      = core.Lexicon
	LexiconEntry = core.LexiconEntry
	SynthRequest = core.SynthRequest
	SynthResult  = core.SynthResult
	SynthInfo    = core.SynthInfo
	MP3Options   = core.MP3Options
	OpusOptions  = core.OpusOptions
	FFmpeg       = core.FFmpeg
)

// NewFFmpeg zwraca domyślny Encoder oparty na ffmpeg/ffprobe.
var NewFFmpeg = core.NewFFmpeg

// Service to serwis Wails. Metody eksportowane (z osadzonego *core.Core) są wywoływane
// z frontendu jako `github.com/koko-anime/adapters-wails.Service.<Metoda>`,
// a ServeHTTP obsługuje dane binarne pod trasą podaną w Register.
type Service struct {
	*core.Core
}

// New tworzy serwis.
func New(cfg Config) (*Service, error) {
	c, err := core.New(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{Core: c}, nil
}

// Register zwraca serwis do application.Options.Services, z trasą HTTP `route`
// (zwykle DefaultRoute; musi zgadzać się z opcją `route` pluginu TS).
func Register(s *Service, route string) application.Service {
	return application.NewServiceWithOptions(s, application.ServiceOptions{Route: route})
}

// AddRoot dodaje katalog do piaskownicy (np. po wyborze folderu w natywnym dialogu).
func AddRoot(s *Service, dir string) error {
	return core.AddRoot(s.Core, dir)
}

// ServiceName nazywa serwis w logach Wails.
func (s *Service) ServiceName() string { return "koko" }

// ServiceStartup — wymagane przez Wails; nic nie inicjalizuje.
func (s *Service) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	return nil
}
