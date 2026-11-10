package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideRoots oznacza ścieżkę spoza dozwolonych katalogów.
var ErrOutsideRoots = errors.New("koko: ścieżka poza dozwolonymi katalogami")

// ErrRelativePath oznacza ścieżkę względną (wymagane są bezwzględne).
var ErrRelativePath = errors.New("koko: wymagana ścieżka bezwzględna")

func normalizeRoots(roots []string) ([]string, error) {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			return nil, err
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		out = append(out, filepath.Clean(abs))
	}
	return out, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// resolve zamienia ścieżkę z frontendu (separator "/") na bezwzględną ścieżkę w piaskownicy.
// Dowiązania symboliczne są rozwiązywane, więc nie da się nimi wyjść poza katalogi.
func (c *Core) resolve(p string, forWrite bool) (string, error) {
	if p == "" {
		return "", ErrRelativePath
	}
	native := filepath.FromSlash(p)
	if !filepath.IsAbs(native) {
		return "", ErrRelativePath
	}
	clean := filepath.Clean(native)
	real := clean
	if r, err := filepath.EvalSymlinks(clean); err == nil {
		real = r
	} else if forWrite || os.IsNotExist(err) {
		dir, err := filepath.EvalSymlinks(filepath.Dir(clean))
		if err != nil {
			if !forWrite {
				// nieistniejący katalog — sprawdzamy samą ścieżkę
				dir = filepath.Dir(clean)
			} else {
				return "", err
			}
		}
		real = filepath.Join(dir, filepath.Base(clean))
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, root := range c.roots {
		if within(root, real) {
			return real, nil
		}
	}
	return "", ErrOutsideRoots
}
