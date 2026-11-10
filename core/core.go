package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Config to konfiguracja serwisu.
type Config struct {
	// Roots to katalogi, do których frontend ma dostęp (np. katalog projektu, biblioteka anime).
	// Pusta lista blokuje wszystkie operacje na plikach.
	Roots []string
	// G2P i Synth implementuje aplikacja (np. przez plkokoro); nil = capability niedostępna.
	G2P   G2P
	Synth Synthesizer
	// Encoder domyślnie: FFmpeg z PATH, jeśli jest dostępny.
	Encoder Encoder
	// MaxBodyBytes ogranicza rozmiar zapisu/wejścia HTTP (domyślnie 2 GiB).
	MaxBodyBytes int64
}

// Core to logika serwisu — metody eksportowane są wywoływane z frontendu.
type Core struct {
	cfg   Config
	mu    sync.RWMutex
	roots []string
}

// ErrUnavailable oznacza brak implementacji capability.
var ErrUnavailable = errors.New("koko: capability niedostępna")

// New tworzy logikę serwisu.
func New(cfg Config) (*Core, error) {
	roots, err := normalizeRoots(cfg.Roots)
	if err != nil {
		return nil, err
	}
	if cfg.Encoder == nil {
		if ff, err := NewFFmpeg(""); err == nil {
			cfg.Encoder = ff
		}
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 2 << 30
	}
	return &Core{cfg: cfg, roots: roots}, nil
}

// AddRoot dodaje katalog do piaskownicy w trakcie działania (np. po wyborze folderu w dialogu).
// Celowo funkcja, nie metoda: Wails wystawia frontendowi wszystkie eksportowane metody serwisu,
// a frontend nie może sam poszerzać piaskownicy.
func AddRoot(c *Core, dir string) error {
	r, err := normalizeRoots([]string{dir})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.roots = append(c.roots, r...)
	c.mu.Unlock()
	return nil
}

// Capabilities zwraca dostępne capability.
func (c *Core) Capabilities() Capabilities {
	c.mu.RLock()
	fs := len(c.roots) > 0
	c.mu.RUnlock()
	return Capabilities{FS: fs, G2P: c.cfg.G2P != nil, Synth: c.cfg.Synth != nil, Encoder: c.cfg.Encoder != nil}
}

// ReadText czyta plik tekstowy (UTF-8).
func (c *Core) ReadText(path string) (string, error) {
	p, err := c.resolve(path, false)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	return string(b), err
}

// WriteText zapisuje plik tekstowy atomowo (plik tymczasowy + rename).
func (c *Core) WriteText(path string, text string) error {
	p, err := c.resolve(path, true)
	if err != nil {
		return err
	}
	return writeAtomic(p, []byte(text))
}

// Exists sprawdza, czy plik istnieje (false także dla ścieżek spoza piaskownicy).
func (c *Core) Exists(path string) (bool, error) {
	p, err := c.resolve(path, false)
	if errors.Is(err, ErrOutsideRoots) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// G2PToIPA zamienia segmenty na IPA.
func (c *Core) G2PToIPA(ctx context.Context, segments []Segment, lexicon Lexicon) ([]string, error) {
	if c.cfg.G2P == nil {
		return nil, ErrUnavailable
	}
	return c.cfg.G2P.ToIPA(ctx, segments, lexicon)
}

// G2PTokenCount zwraca liczbę tokenów fonemów.
func (c *Core) G2PTokenCount(ctx context.Context, ipa string) (int, error) {
	if c.cfg.G2P == nil {
		return 0, ErrUnavailable
	}
	return c.cfg.G2P.TokenCount(ctx, ipa)
}

// G2PVocabulary zwraca symbole IPA słownika modelu.
func (c *Core) G2PVocabulary(ctx context.Context) ([]string, error) {
	if c.cfg.G2P == nil {
		return nil, ErrUnavailable
	}
	return c.cfg.G2P.Vocabulary(ctx)
}

// SynthInfo zwraca tożsamość silnika syntezy.
func (c *Core) SynthInfo(ctx context.Context) (*SynthInfo, error) {
	if c.cfg.Synth == nil {
		return nil, ErrUnavailable
	}
	info, err := c.cfg.Synth.Info(ctx)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

func writeAtomic(p string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), ".koko-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, p)
}
