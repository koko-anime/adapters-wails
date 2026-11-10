# koko-adapters-wails

Plugin **Wails 3** dla [`@koko-anime/formats`](https://www.npmjs.com/package/@koko-anime/formats). Składa się z dwóch części:

| Część | Gdzie | Co robi |
| --- | --- | --- |
| Serwis Go `kokowails` | `github.com/seba9989/koko-adapters-wails` | pliki w piaskownicy, hooki G2P/syntezy, kodowanie audio (FFmpeg) |
| Plugin TS `@koko-anime/adapters-wails` | npm | capability `fs`, `g2p`, `synth`, `encoder` dla biblioteki |

Metody (tekst, G2P, info o syntezie) idą przez most Wails (`Call.ByName`). Dane binarne (audio, paczki, PCM) idą przez trasę HTTP serwisu (domyślnie `/koko`), bo most przesyła `[]byte` jako base64.

## Go

```go
import kokowails "github.com/seba9989/koko-adapters-wails"

koko, err := kokowails.New(kokowails.Config{
    Roots: []string{projectDir},   // tylko te katalogi są dostępne dla frontendu
    G2P:   myG2P{},                // np. Phonemis — implementuje kokowails.G2P
    Synth: myKokoro{},             // np. plplkokoro — implementuje kokowails.Synthesizer
    // Encoder: domyślnie FFmpeg z PATH (kokowails.NewFFmpeg("/ścieżka/ffmpeg"))
})
if err != nil { log.Fatal(err) }

app := application.New(application.Options{
    Services: []application.Service{
        kokowails.Register(koko, kokowails.DefaultRoute),
    },
})
```

### Interfejsy do zaimplementowania

```go
type G2P interface {
    ToIPA(ctx context.Context, segments []Segment, lexicon Lexicon) ([]string, error)
    TokenCount(ctx context.Context, ipa string) (int, error)
    Vocabulary(ctx context.Context) ([]string, error)
}

type Synthesizer interface {
    Synthesize(ctx context.Context, req SynthRequest) (SynthResult, error) // PCM mono float32, bez gain
    Info(ctx context.Context) (SynthInfo, error)
}
```

Brak `G2P`, `Synth` lub `Encoder` (np. brak FFmpeg) oznacza, że plugin TS nie zgłosi tej capability. Biblioteka zwróci wtedy diagnostykę `KF002` zamiast błędu wywołania.

### Piaskownica plików

Frontend ma dostęp tylko do katalogów z `Roots`. Ścieżki muszą być bezwzględne, a dowiązania symboliczne są rozwijane przed sprawdzeniem. Pusty `Roots` blokuje cały dostęp. Katalog wybrany przez użytkownika w natywnym oknie dodaj po stronie Go:

```go
dir, _ := app.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).PromptForSingleSelection()
if dir != "" { _ = kokowails.AddRoot(koko, dir) }
```

`AddRoot` i `Register` są funkcjami pakietu, nie metodami. Wails binduje każdą eksportowaną metodę serwisu, a frontend nie może sam rozszerzać piaskownicy.

### Trasy HTTP (pod `route`)

| Metoda | Ścieżka | Opis |
| --- | --- | --- |
| GET/HEAD | `/fs?path=` | odczyt pliku (obsługuje Range) |
| PUT | `/fs?path=` | atomowy zapis (plik tymczasowy + rename) |
| POST | `/synth` | JSON `SynthRequest` → PCM float32 LE (`X-Koko-Sample-Rate`, `X-Koko-Duration-Ms`) |
| POST | `/encode/mp3?sampleRate&channels&bitrate&raw=1` | PCM → MP3 CBR bez rezerwuaru |
| POST | `/encode/opus?sampleRate&channels&bitrate` | PCM → Ogg Opus 48 kHz |
| POST | `/decode?codec=mp3\|opus` | audio → PCM (`X-Koko-Sample-Rate`) |

Kody błędów: 400 ścieżka względna, 403 poza piaskownicą, 404 brak pliku, 501 brak capability.

## TypeScript

```sh
bun add @koko-anime/adapters-wails @wailsio/runtime
```

```ts
// src/koko.config.ts
import { defineConfig } from '@koko-anime/formats/config';
import { nobleCrypto } from '@koko-anime/formats/crypto-noble';
import { wails } from '@koko-anime/adapters-wails';

export default defineConfig({ plugins: [nobleCrypto(), await wails()] });
```

```ts
import { KokoAss } from '@koko-anime/formats/ass';
import { KokoPack } from '@koko-anime/formats/pack';

const ass = await KokoAss.load('/home/seba/anime/ep01.ass'); // + koko-lexicon.toml obok
const pack = KokoPack.create({ subtitles: ass });             // (sprawdź instanceof KokoErrors)
await pack.rebuild();                                         // G2P + synteza + MP3 w Go
```

Opcje `wails(opts)` i `wailsPlugin(capabilities, opts)`:

- `route` — trasa z `Register` (domyślnie `/koko`).
- `service` — pełna nazwa typu Go, jeśli moduł ma inną ścieżkę (domyślnie `github.com/seba9989/koko-adapters-wails.Service`).
- `call`, `fetch` — własne implementacje, np. do testów.
- `name` — nazwa pluginu.

`wails()` pyta serwis o capability (`Service.Capabilities`). Gdy zestaw jest znany z góry, `wailsPlugin()` tworzy plugin synchronicznie. `fs.resolve` działa w TS i normalizuje ścieżki Windows (`\` → `/`).

## Rozwój

```sh
bun install
bun test          # buduje internal/testserver (go build) i testuje plugin end-to-end
go test -race ./core
bun run build
```

`internal/testserver` uruchamia core bez Wails. Emuluje `Call.ByName` (POST `/call`) i używa atrap G2P/syntezy oraz prawdziwego FFmpeg. Gotową binarkę można podać w `KOKO_TESTSERVER`.

Wymagania: Go 1.26 (wymóg Wails v3.0.0), FFmpeg z `libmp3lame` i `libopus`, `@koko-anime/formats` ≥ 0.3.0 z poprawką `OpusEncodeOptions.sampleRate`.
