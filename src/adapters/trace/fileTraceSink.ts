import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import type { TraceRecord, TraceSink } from '../../telemetry/trace.js';

/**
 * Persists each request's trace as a JSON file in `dir` (one file per request). This is our
 * stand-in for a database: durable, greppable, and easy to inspect after the fact. In production
 * this would be an append to a traces table / an emit to an OTel collector behind the same TraceSink
 * port — nothing upstream changes.
 *
 * Filename: `<sortable-timestamp>_<requestId>.json` so files sort chronologically.
 */
export function createFileTraceSink(dir: string): TraceSink {
  let dirReady = false;

  return {
    async write(record: TraceRecord): Promise<void> {
      if (!dirReady) {
        await mkdir(dir, { recursive: true });
        dirReady = true;
      }
      const stamp = record.startedAt.replace(/[:.]/g, '-');
      const file = join(dir, `${stamp}_${record.requestId}.json`);
      await writeFile(file, JSON.stringify(record, null, 2), 'utf8');
    },
  };
}
