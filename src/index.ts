/**
 * Plugin środowiska Wails 3 dla `@koko-anime/formats`.
 *
 * Współpracuje z serwisem Go `github.com/seba9989/koko-adapters-wails` (ten sam repozytorium):
 * metody (pliki tekstowe, G2P, informacje o syntezie) idą przez most Wails (`Call.ByName`),
 * a dane binarne (audio, paczki, PCM) — przez trasę HTTP serwisu (domyślnie `/koko`),
 * bo most koduje `[]byte` w base64.
 *
 * @example
 * ```ts
 * // src/koko.config.ts
 * import { defineConfig } from '@koko-anime/formats/config';
 * import { nobleCrypto } from '@koko-anime/formats/crypto-noble';
 * import { wails } from '@koko-anime/adapters-wails';
 *
 * export default defineConfig({ plugins: [nobleCrypto(), await wails()] });
 * ```
 *
 * @module @koko-anime/adapters-wails
 */
import { definePlugin, type CanonicalSegment, type KokoPlugin, type ResolvedLexicon } from '@koko-anime/formats/plugin';

/** Pełna nazwa typu serwisu Go (pakiet + typ) — prefiks nazw metod dla `Call.ByName`. */
export const DEFAULT_SERVICE = 'github.com/seba9989/koko-adapters-wails.Service';

/** Domyślna trasa HTTP serwisu (`kokowails.DefaultRoute`). */
export const DEFAULT_ROUTE = '/koko';

/** Capability dostępne po stronie Go (`Service.Capabilities`). */
export interface WailsCapabilities {
  /** Pliki (co najmniej jeden katalog w piaskownicy). */
  fs: boolean;
  /** G2P (implementacja `kokowails.G2P`). */
  g2p: boolean;
  /** Synteza (implementacja `kokowails.Synthesizer`). */
  synth: boolean;
  /** Kodowanie audio (domyślnie FFmpeg). */
  encoder: boolean;
}

/** Wywołanie metody serwisu Go po pełnej nazwie. */
export type CallByName = (method: string, ...args: unknown[]) => Promise<unknown>;

/** Opcje pluginu. */
export interface WailsOptions {
  /** Nazwa pluginu (domyślnie `wails`). */
  name?: string;
  /** Trasa HTTP serwisu — musi zgadzać się z `kokowails.Register(s, route)`. */
  route?: string;
  /** Pełna nazwa typu serwisu Go (gdy fork modułu ma inną ścieżkę). */
  service?: string;
  /** Własne wywołanie metod (domyślnie `Call.ByName` z `@wailsio/runtime`). */
  call?: CallByName;
  /** Własny `fetch` (domyślnie globalny). */
  fetch?: typeof fetch;
}

/** Nagłówek z częstotliwością próbkowania PCM. */
const HEADER_SAMPLE_RATE = 'X-Koko-Sample-Rate';
/** Nagłówek z długością klipu w ms. */
const HEADER_DURATION = 'X-Koko-Duration-Ms';

let runtimeCall: CallByName | undefined;

/** Domyślne wywołanie: `Call.ByName` z `@wailsio/runtime` (ładowane leniwie). */
async function defaultCall(method: string, ...args: unknown[]): Promise<unknown> {
  if (!runtimeCall) {
    const rt = await import('@wailsio/runtime');
    runtimeCall = (m, ...a) => rt.Call.ByName(m, ...a) as Promise<unknown>;
  }
  return runtimeCall(method, ...args);
}

/**
 * Łączy ścieżki z separatorem `/` (ścieżki Windows są normalizowane: `\` → `/`).
 * Ścieżka bezwzględna w `relative` zastępuje `base`.
 */
export function joinPath(base: string, relative: string): string {
  const rel = relative.replace(/\\/g, '/');
  if (rel.startsWith('/') || /^[A-Za-z]:\//.test(rel)) return normalize(rel);
  return normalize(`${base.replace(/\\/g, '/').replace(/\/+$/, '')}/${rel}`);
}

function normalize(p: string): string {
  const drive = /^[A-Za-z]:/.exec(p)?.[0] ?? '';
  const rest = p.slice(drive.length);
  const abs = rest.startsWith('/');
  const out: string[] = [];
  for (const part of rest.split('/')) {
    if (part === '' || part === '.') continue;
    if (part === '..') {
      if (out.length && out[out.length - 1] !== '..') out.pop();
      else if (!abs) out.push('..');
      continue;
    }
    out.push(part);
  }
  return drive + (abs ? '/' : '') + out.join('/');
}

function bytesOf(pcm: Float32Array): Uint8Array<ArrayBuffer> {
  const copy = new Float32Array(pcm.length);
  copy.set(pcm);
  return new Uint8Array(copy.buffer);
}

async function check(res: Response): Promise<Response> {
  if (!res.ok) throw new Error(`koko-wails ${res.status}: ${(await res.text()).trim()}`);
  return res;
}

/**
 * Tworzy plugin dla znanego zestawu capability (synchronicznie).
 * Zwykle wygodniej użyć {@link wails}, który pyta serwis Go o capability.
 */
export function wailsPlugin(capabilities: WailsCapabilities, opts: WailsOptions = {}): KokoPlugin {
  const service = opts.service ?? DEFAULT_SERVICE;
  const route = (opts.route ?? DEFAULT_ROUTE).replace(/\/+$/, '');
  const call = opts.call ?? defaultCall;
  const http = (input: string, init?: RequestInit) => (opts.fetch ?? fetch)(input, init);
  const m = (name: string, ...args: unknown[]) => call(`${service}.${name}`, ...args);
  const fsUrl = (path: string) => `${route}/fs?path=${encodeURIComponent(path.replace(/\\/g, '/'))}`;

  const plugin: KokoPlugin = { name: opts.name ?? 'wails' };

  if (capabilities.fs) {
    plugin.fs = {
      readText: async (path) => (await m('ReadText', path)) as string,
      readBlob: async (path) => (await check(await http(fsUrl(path)))).blob(),
      writeText: async (path, text) => {
        await m('WriteText', path, text);
      },
      writeBlob: async (path, blob) => {
        await check(await http(fsUrl(path), { method: 'PUT', body: blob }));
      },
      exists: async (path) => (await m('Exists', path)) as boolean,
      resolve: joinPath,
    };
  }

  if (capabilities.g2p) {
    plugin.g2p = {
      toIpa: async (segments: CanonicalSegment[], lexicon: ResolvedLexicon) =>
        ((await m('G2PToIPA', segments, { entries: lexicon.entries })) as string[] | null) ?? [],
      tokenCount: async (ipa) => (await m('G2PTokenCount', ipa)) as number,
      ipaVocabulary: async () => new Set(((await m('G2PVocabulary')) as string[] | null) ?? []),
    };
  }

  if (capabilities.synth) {
    plugin.synth = {
      synthesize: async (req) => {
        const res = await check(
          await http(`${route}/synth`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ipa: req.ipa, voice: req.voice, speed: req.speed, gainDb: req.gainDb }),
            ...(req.signal ? { signal: req.signal } : {}),
          }),
        );
        const pcm = new Float32Array(await res.arrayBuffer());
        const sampleRate = Number(res.headers.get(HEADER_SAMPLE_RATE));
        const durationMs = Number(res.headers.get(HEADER_DURATION)) || Math.round((pcm.length * 1000) / sampleRate);
        return { pcm, sampleRate, durationMs };
      },
      info: async () => {
        const info = (await m('SynthInfo')) as { engine?: string; model: string; modelHash?: string; g2p: string };
        return {
          model: info.model,
          g2p: info.g2p,
          ...(info.engine ? { engine: info.engine } : {}),
          ...(info.modelHash ? { modelHash: info.modelHash } : {}),
        };
      },
    };
  }

  if (capabilities.encoder) {
    const encode = async (path: string, pcm: Float32Array, query: Record<string, string | number>) => {
      const qs = new URLSearchParams(Object.entries(query).map(([k, v]) => [k, String(v)]));
      const res = await check(await http(`${route}${path}?${qs}`, { method: 'POST', body: bytesOf(pcm) }));
      return new Uint8Array(await res.arrayBuffer());
    };
    plugin.encoder = {
      mp3: (pcm, o) => encode('/encode/mp3', pcm, { sampleRate: o.sampleRate, channels: o.channels, bitrate: o.bitrateKbps, raw: o.rawFrames ? 1 : 0 }),
      opus: (pcm, o) => encode('/encode/opus', pcm, { sampleRate: o.sampleRate, channels: o.channels, bitrate: o.bitrateKbps }),
      decode: async (bytes, codec) => {
        const res = await check(await http(`${route}/decode?codec=${codec}`, { method: 'POST', body: bytes }));
        return { pcm: new Float32Array(await res.arrayBuffer()), sampleRate: Number(res.headers.get(HEADER_SAMPLE_RATE)) };
      },
    };
  }

  return definePlugin(plugin);
}

/**
 * Tworzy plugin, pytając serwis Go o dostępne capability (`Service.Capabilities`).
 * Brakujące capability nie trafiają do pluginu, więc biblioteka zgłasza KF002 zamiast błędów wywołań.
 */
export async function wails(opts: WailsOptions = {}): Promise<KokoPlugin> {
  const call = opts.call ?? defaultCall;
  const caps = (await call(`${opts.service ?? DEFAULT_SERVICE}.Capabilities`)) as WailsCapabilities;
  return wailsPlugin(caps, opts);
}
