import 'dotenv/config';
import { loadConfig } from './config.js';
import { createApp } from './app.js';
import { logger } from './adapters/log/logger.js';

const config = loadConfig();
const { app } = createApp(config);

app.listen(config.port, () => {
  logger.info(
    { port: config.port, skipSignature: config.skipSignature },
    `InquiryIQ listening — POST http://localhost:${config.port}/webhooks/guesty/message-received`,
  );
});
