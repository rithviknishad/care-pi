import { applyD1Migrations } from "cloudflare:test";
import { env } from "cloudflare:workers";

// Setup files can run more than one time. applyD1Migrations() applies only the new migrations.
await applyD1Migrations(env.DB, env.TEST_MIGRATIONS);
