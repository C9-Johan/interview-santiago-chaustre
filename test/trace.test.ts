import { describe, it, expect } from 'vitest';
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createTracer, type TraceRecord, type TraceSink } from '../src/telemetry/trace.js';
import { createFileTraceSink } from '../src/adapters/trace/fileTraceSink.js';

describe('createTracer', () => {
  it('buffers steps + context and flushes one record with the outcome on finish', async () => {
    const written: TraceRecord[] = [];
    const sink: TraceSink = { async write(r) { written.push(r); } };

    const trace = createTracer('req-123', sink);
    trace.step('webhook_received', { payload: { a: 1 } });
    trace.context({ postId: 'p1', conversationId: 'c1' });
    trace.step('classified', { primary_code: 'G1' });
    await trace.finish('auto_sent');

    expect(written).toHaveLength(1);
    const rec = written[0]!;
    expect(rec.requestId).toBe('req-123');
    expect(rec.outcome).toBe('auto_sent');
    expect(rec.postId).toBe('p1');
    expect(rec.conversationId).toBe('c1');
    expect(rec.steps.map((s) => s.step)).toEqual(['webhook_received', 'classified']);
    expect(rec.finishedAt).toBeTruthy();
  });
});

describe('createFileTraceSink', () => {
  it('writes a valid JSON trace file per request', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'inquiryiq-traces-'));
    try {
      const sink = createFileTraceSink(dir);
      const trace = createTracer('req-file-1', sink);
      trace.step('parsed', { message: { postId: 'p9' } });
      await trace.finish('ignored');

      const files = await readdir(dir);
      expect(files).toHaveLength(1);
      expect(files[0]).toContain('req-file-1');

      const parsed = JSON.parse(await readFile(join(dir, files[0]!), 'utf8')) as TraceRecord;
      expect(parsed.requestId).toBe('req-file-1');
      expect(parsed.outcome).toBe('ignored');
      expect(parsed.steps[0]!.step).toBe('parsed');
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });
});
