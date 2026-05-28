import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import {
  CLASSIFY_SYSTEM_PROMPT,
} from '../src/domain/classifyPrompt.js';
import { CLOSER_SYSTEM_PROMPT } from '../src/domain/closer.js';
import {
  DEFAULT_CHECK_AVAILABILITY_DESC,
  DEFAULT_GET_LISTING_DESC,
} from '../src/adapters/llm/openaiLlm.js';
import type { Candidate } from './candidate.js';

/**
 * Write a winning candidate's values into the four whitelisted source locations — and ONLY those.
 * Nothing here can touch `decide.ts`, the taxonomy, the eval oracle, or anything else: each edit
 * replaces one exact, known baseline literal, and throws if that literal isn't found (so a drifted
 * file aborts cleanly rather than corrupting). Returns a `revert()` that restores every file verbatim,
 * which the optimizer calls if the post-write `tsc` gate fails.
 */

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const CLASSIFY_FILE = resolve(ROOT, 'src/domain/classifyPrompt.ts');
const CLOSER_FILE = resolve(ROOT, 'src/domain/closer.ts');
const ADAPTER_FILE = resolve(ROOT, 'src/adapters/llm/openaiLlm.ts');

/** Escape a string for safe insertion inside a backtick template literal. */
function forTemplate(s: string): string {
  return s.replace(/\\/g, '\\\\').replace(/`/g, '\\`').replace(/\$\{/g, '\\${');
}

/** Replace the single expected occurrence of `find` in `content`, or throw if it's not exactly there. */
function replaceOnce(content: string, find: string, replaceWith: string, label: string): string {
  const idx = content.indexOf(find);
  if (idx === -1) throw new Error(`materialize: could not locate ${label} baseline in source`);
  if (content.indexOf(find, idx + find.length) !== -1) {
    throw new Error(`materialize: ${label} baseline appears more than once — refusing to edit`);
  }
  return content.slice(0, idx) + replaceWith + content.slice(idx + find.length);
}

export function materialize(best: Candidate): () => void {
  const targets = [CLASSIFY_FILE, CLOSER_FILE, ADAPTER_FILE];
  const originals = new Map(targets.map((f) => [f, readFileSync(f, 'utf8')]));

  // Prompts are no-interpolation template literals: the raw string appears verbatim between backticks.
  const classify = replaceOnce(
    originals.get(CLASSIFY_FILE)!,
    CLASSIFY_SYSTEM_PROMPT,
    forTemplate(best.classifySystem),
    'CLASSIFY_SYSTEM_PROMPT',
  );

  const closer = replaceOnce(
    originals.get(CLOSER_FILE)!,
    CLOSER_SYSTEM_PROMPT,
    forTemplate(best.closerSystem),
    'CLOSER_SYSTEM_PROMPT',
  );

  // Tool descriptions are single-quoted; swap the whole literal for a JSON-escaped (double-quoted) one.
  // Temperature flips the exported default from `undefined` to the chosen number (skip if unchanged).
  let adapter = originals.get(ADAPTER_FILE)!;
  adapter = replaceOnce(
    adapter,
    `'${DEFAULT_GET_LISTING_DESC}'`,
    JSON.stringify(best.getListingDesc),
    'DEFAULT_GET_LISTING_DESC',
  );
  adapter = replaceOnce(
    adapter,
    `'${DEFAULT_CHECK_AVAILABILITY_DESC}'`,
    JSON.stringify(best.checkAvailabilityDesc),
    'DEFAULT_CHECK_AVAILABILITY_DESC',
  );
  if (typeof best.temperature === 'number') {
    adapter = replaceOnce(
      adapter,
      'export const DEFAULT_TEMPERATURE: number | undefined = undefined;',
      `export const DEFAULT_TEMPERATURE: number | undefined = ${best.temperature};`,
      'DEFAULT_TEMPERATURE',
    );
  }

  // All replacements succeeded — write together so a mid-way throw above leaves the tree untouched.
  writeFileSync(CLASSIFY_FILE, classify);
  writeFileSync(CLOSER_FILE, closer);
  writeFileSync(ADAPTER_FILE, adapter);

  return function revert(): void {
    for (const [file, content] of originals) writeFileSync(file, content);
  };
}
