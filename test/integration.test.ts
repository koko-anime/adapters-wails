/**
 * Test integracyjny: plugin TS ↔ core Go (internal/testserver, bez Wails).
 * Serwer budowany jest `go build`; można podać gotową binarkę w KOKO_TESTSERVER.
 */
import { afterAll, beforeAll, beforeEach, describe, expect, test } from 'bun:test';
import { mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { defineConfig, resetConfig } from '@koko-anime/formats/config';
import { nobleCrypto } from '@koko-anime/formats/crypto-noble';
import { KokoAss } from '@koko-anime/formats/ass';
import { KokoErrors, KokoPack } from '@koko-anime/formats/pack';
import type { KokoPlugin } from '@koko-anime/formats/plugin';
import { DEFAULT_SERVICE, joinPath, wails, type WailsOptions } from '../src/index.ts';

const ok = <T>(x: T | KokoErrors): T => {
  if (x instanceof KokoErrors) throw new Error(`${x.summary} ${JSON.stringify(x.all.map((d) => [d.code, d.params]))}`);
  return x;
};

const work = mkdtempSync(join(tmpdir(), 'koko-wails-'));
const root = realpathSync(mkdtempSync(join(tmpdir(), 'koko-root-')));
const outside = realpathSync(mkdtempSync(join(tmpdir(), 'koko-out-')));
let server: ChildProcess;
let opts: WailsOptions;
let plugin: KokoPlugin;

beforeAll(async () => {
  let bin = process.env.KOKO_TESTSERVER;
  if (!bin) {
    bin = join(work, 'testserver');
    const b = spawnSync('go', ['build', '-o', bin, './internal/testserver'], { cwd: join(import.meta.dir, '..'), encoding: 'utf8' });
    if (b.status !== 0) throw new Error(`go build: ${b.stderr}`);
  }
  server = spawn(bin, [root], { stdio: ['ignore', 'pipe', 'inherit'] });
  const base = await new Promise<string>((resolve) => server.stdout!.once('data', (d) => resolve(String(d).trim())));
  // Emulacja środowiska WebView: Call.ByName → POST /call, trasy względne → serwer testowy.
  opts = {
    call: async (name, ...args) => {
      const res = await fetch(`${base}/call`, { method: 'POST', body: JSON.stringify({ name, args }) });
      if (!res.ok) throw new Error(await res.text());
      return res.json();
    },
    fetch: ((input: string, init?: RequestInit) => fetch(base + input, init)) as typeof fetch,
  };
  plugin = await wails(opts);
});

afterAll(() => {
  server?.kill();
  for (const d of [work, root, outside]) rmSync(d, { recursive: true, force: true });
});

beforeEach(() => {
  resetConfig();
  defineConfig({ plugins: [nobleCrypto(), plugin] });
});

describe('joinPath', () => {
  test('posix i Windows', () => {
    expect(joinPath('/a/b', 'c/../d.toml')).toBe('/a/b/d.toml');
    expect(joinPath('/a/b', '/x/y')).toBe('/x/y');
    expect(joinPath('C:\\Anime\\S1', '..\\koko-lexicon.toml')).toBe('C:/Anime/koko-lexicon.toml');
    expect(joinPath('C:/a', 'D:\\b')).toBe('D:/b');
  });
});

describe('wails() — capability', () => {
  test('wykrywa wszystkie capability serwisu', () => {
    expect(Object.keys(plugin).filter((k) => k !== 'name').sort()).toEqual(['encoder', 'fs', 'g2p', 'synth']);
  });

  test('pomija capability niedostępne po stronie Go', async () => {
    const p = await wails({
      call: async (name) => {
        expect(name).toBe(`${DEFAULT_SERVICE}.Capabilities`);
        return { fs: true, g2p: false, synth: false, encoder: false };
      },
    });
    expect(p.fs).toBeDefined();
    expect(p.g2p ?? p.synth ?? p.encoder).toBeUndefined();
  });
});

describe('fs', () => {
  test('tekst, blob, exists', async () => {
    const fs = plugin.fs!;
    const path = `${root}/a.txt`;
    expect(await fs.exists(path)).toBe(false);
    await fs.writeText!(path, 'zażółć\n');
    expect(await fs.readText(path)).toBe('zażółć\n');
    expect(await fs.exists(path)).toBe(true);
    const bytes = new Uint8Array([0, 1, 2, 250, 255]);
    await fs.writeBlob!(`${root}/b.bin`, new Blob([bytes]));
    expect(new Uint8Array(await (await fs.readBlob(`${root}/b.bin`)).arrayBuffer())).toEqual(bytes);
  });

  test('piaskownica: ścieżki spoza katalogów są odrzucane', async () => {
    const fs = plugin.fs!;
    writeFileSync(join(outside, 'secret.txt'), 'x');
    expect(await fs.exists(`${outside}/secret.txt`)).toBe(false);
    expect(fs.readText(`${outside}/secret.txt`)).rejects.toThrow();
    expect(fs.readBlob(`${root}/../${outside.split('/').pop()}/secret.txt`)).rejects.toThrow(/403/);
    expect(fs.writeBlob!(`${outside}/w.bin`, new Blob(['x']))).rejects.toThrow(/403/);
  });
});

describe('Koko-ASS + Koko-Pack przez Wails', () => {
  const SRC = readFileSync(join(import.meta.dir, 'fixtures/spec-example.ass'), 'utf8');

  test('KokoAss.load z leksykonem serii', async () => {
    writeFileSync(join(root, 'ep01.ass'), SRC);
    writeFileSync(join(root, 'koko-lexicon.toml'), 'format = "koko-lexicon"\nversion = 1\n\n[entries.Jinshi]\nipa = "dʑinɕi"\n');
    const doc = ok(await KokoAss.load(`${root}/ep01.ass`));
    expect(doc.seriesLexicon).not.toBeNull();
    expect(doc.diagnostics.filter((d) => d.level === 'error')).toEqual([]);
  });

  test('rebuild (G2P + synteza + MP3 z FFmpeg) → toBlob → open → zapis', async () => {
    const p = KokoPack.create({ subtitles: KokoAss.parse(SRC) as KokoAss, title: 'Wails' });
    ok(await p.rebuild());
    expect(p.stale).toBe(false);
    expect(p.timeline.clips.length).toBe(7);
    expect(p.audio!.spliceable).toBe(true);

    const blob = ok(await p.toBlob());
    await plugin.fs!.writeBlob!(`${root}/ep01.kokopack`, blob);
    const back = await plugin.fs!.readBlob(`${root}/ep01.kokopack`);
    const q = ok(await KokoPack.open(back));
    expect(q.timeline.clips.length).toBe(7);
    expect(q.stale).toBe(false);
  });

  test('rebuild z Opus', async () => {
    const p = KokoPack.create({ subtitles: KokoAss.parse(SRC) as KokoAss });
    ok(await p.rebuild(undefined, { codec: 'opus' }));
    expect(p.audio!.params.codec).toBe('opus');
    expect(p.audio!.params.sampleRate).toBe(48000);
    const q = ok(await KokoPack.open(ok(await p.toBlob())));
    expect(q.diagnostics.filter((d) => d.source === 'pack').map((d) => d.code)).toEqual([]);
  });
});
