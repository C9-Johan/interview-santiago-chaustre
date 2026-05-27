import express, { type Request, type Response, type Router } from 'express';
import type { Store } from '../ports/Store.js';
import type { NoteRecord } from '../adapters/guesty/mockGuesty.js';

export interface AdminDeps {
  store: Store;
  /** Read-only view of notes the bot posted to Guesty — lets us SEE output during the demo. */
  getNotes: () => NoteRecord[];
}

/**
 * Operator controls + observability. Factory-injected (DI) like the webhook router. Mounted in
 * app.ts AFTER express.json(), so req.body here is already-parsed JSON.
 */
export function createAdminRouter(deps: AdminDeps): Router {
  const router = express.Router();

  // Liveness probe — no dependencies, just confirms the process is up.
  router.get('/healthz', (_req: Request, res: Response) => {
    res.status(200).json({ status: 'ok' });
  });

  // Operator kill switch — read current state.
  router.get('/admin/auto-response', (_req: Request, res: Response) => {
    res.json({ enabled: deps.store.isAutoResponseEnabled() });
  });

  // Operator kill switch — flip it. Validate the boolean strictly so a typo can't silently
  // disable auto-send (e.g. { enabled: "false" } would otherwise be truthy).
  router.post('/admin/auto-response', (req: Request, res: Response) => {
    const enabled = (req.body as { enabled?: unknown })?.enabled;
    if (typeof enabled !== 'boolean') {
      res.status(400).json({ error: 'body must be { enabled: boolean }' });
      return;
    }
    deps.store.setAutoResponseEnabled(enabled);
    res.json({ enabled: deps.store.isAutoResponseEnabled() });
  });

  // Observability — what the bot actually posted to Guesty.
  router.get('/admin/notes', (_req: Request, res: Response) => {
    res.json({ notes: deps.getNotes() });
  });

  return router;
}
