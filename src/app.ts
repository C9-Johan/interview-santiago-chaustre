import express, { type Express } from 'express';
import type { Config } from './config.js';
import { createMemoryStore } from './adapters/store/memoryStore.js';
import { createMockGuesty } from './adapters/guesty/mockGuesty.js';
import { createMockLlm } from './adapters/llm/mockLlm.js';
import { createOpenAiLlm } from './adapters/llm/openaiLlm.js';
import { createProcessMessage } from './pipeline/processMessage.js';
import { createWebhookRouter } from './transport/webhookRouter.js';
import { createAdminRouter } from './transport/adminRouter.js';
import { verifyGuestySignature } from './transport/verifySignature.js';
import { logger } from './adapters/log/logger.js';

/**
 * Composition root: pick adapters, wire ports into the pipeline and routers, build the Express app.
 * This is the ONE place that knows about concrete implementations — everything else talks to ports.
 *
 * Returns the app plus the live adapters so tests/scripts can inspect posted notes and flip the toggle.
 */
export function createApp(config: Config) {
  const store = createMemoryStore(config.autoResponseEnabled);
  const guesty = createMockGuesty();

  // Real OpenAI when a key is present; otherwise the deterministic mock so the slice runs offline.
  const usingRealLlm = Boolean(config.openaiApiKey);
  const llm = usingRealLlm ? createOpenAiLlm({ model: config.openaiModel }) : createMockLlm();
  logger.info({ llm: usingRealLlm ? `openai:${config.openaiModel}` : 'mock' }, 'LLM adapter selected');

  const process = createProcessMessage({ llm, guesty, store });

  const app: Express = express();

  // Webhook router FIRST: it applies its own raw-body middleware per-route and fully handles the
  // request, so the global express.json() below never sees (and never mangles) the signed bytes.
  app.use(
    createWebhookRouter({
      store,
      verify: (rawBody, headers) =>
        verifyGuestySignature(
          { secret: config.guestyWebhookSecret, skip: config.skipSignature },
          rawBody,
          headers,
        ),
      process,
      log: logger,
    }),
  );

  // Everything else (admin/observability) uses parsed JSON.
  app.use(express.json());
  app.use(createAdminRouter({ store, getNotes: () => guesty.getNotes() }));

  return { app, store, guesty };
}
