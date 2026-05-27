/**
 * Central env config. Read once at startup; everything else takes values via DI so it stays
 * testable. Defaults make the service run fully offline (mock adapters, signature skipped).
 */

function bool(v: string | undefined, fallback: boolean): boolean {
  if (v === undefined) return fallback;
  return v === 'true' || v === '1';
}

export interface Config {
  port: number;
  guestyWebhookSecret: string | undefined;
  skipSignature: boolean;
  openaiApiKey: string | undefined;
  openaiModel: string;
  autoResponseEnabled: boolean;
  debounceMs: number;
  traceDir: string;
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  return {
    port: Number(env.PORT ?? 3000),
    guestyWebhookSecret: env.GUESTY_WEBHOOK_SECRET || undefined,
    skipSignature: bool(env.SKIP_SIGNATURE, true),
    openaiApiKey: env.OPENAI_API_KEY || undefined,
    openaiModel: env.OPENAI_MODEL || 'gpt-4o-mini',
    autoResponseEnabled: bool(env.AUTO_RESPONSE_ENABLED, true),
    debounceMs: Number(env.DEBOUNCE_MS ?? 0),
    traceDir: env.TRACE_DIR || 'traces',
  };
}
