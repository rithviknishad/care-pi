// The environment defaults of this version, shown read-only next to the env editors.
// The API has no operation for them, so the console keeps this copy.
// Sources: docs/config.md ("env": care-pi defaults that env.backend can change),
// solutions/care-clinic/solution.yaml (env and the plug care_onboarding_fe).
// Keep this list in step with those files.

export interface EnvDefault {
  name: string;
  value: string;
  source: "care-pi" | "care-clinic" | "plug";
}

export const BACKEND_DEFAULTS: EnvDefault[] = [
  { name: "BUCKET_REGION", value: "ap-south-1", source: "care-pi" },
  { name: "FILE_UPLOAD_BUCKET", value: "patient-bucket", source: "care-pi" },
  { name: "FACILITY_S3_BUCKET", value: "facility-bucket", source: "care-pi" },
  { name: "DJANGO_SETTINGS_MODULE", value: "config.settings.production", source: "care-pi" },
  { name: "USE_SMS", value: "False", source: "care-pi" },
  { name: "DJANGO_SECURE_SSL_REDIRECT", value: "False", source: "care-pi" },
  { name: "DJANGO_SECURE_HSTS_INCLUDE_SUBDOMAINS", value: "False", source: "care-pi" },
  { name: "DJANGO_SECURE_HSTS_PRELOAD", value: "False", source: "care-pi" },
  { name: "CURRENT_DOMAIN", value: "https://<hostname>.local", source: "care-pi" },
  { name: "RATE_LIMIT", value: "5/10m", source: "care-clinic" },
];

export const FRONTEND_DEFAULTS: EnvDefault[] = [
  { name: "REACT_ALLOWED_LOCALES", value: "en,hi,ml", source: "care-clinic" },
  { name: "REACT_ALLOWED_ENCOUNTER_CLASSES", value: "amb", source: "care-clinic" },
  { name: "REACT_MFE_REGISTERED_COMPONENTS", value: "UserDashboard", source: "plug" },
];

// The schema rejects these names (config.schema.json, env.*.propertyNames.not.enum).
// The console reads the list from GET /config/schema and uses this copy until then.
export const RESERVED_BACKEND = [
  "ADDITIONAL_PLUGS",
  "BUCKET_ENDPOINT",
  "BUCKET_EXTERNAL_ENDPOINT",
  "BUCKET_KEY",
  "BUCKET_PROVIDER",
  "BUCKET_SECRET",
  "CELERY_BROKER_URL",
  "CELERY_WORKER_CONCURRENCY",
  "CSRF_TRUSTED_ORIGINS",
  "DATABASE_URL",
  "DJANGO_ALLOWED_HOSTS",
  "DJANGO_SECRET_KEY",
  "GUNICORN_WORKERS",
  "JWKS_BASE64",
  "POSTGRES_DB",
  "POSTGRES_HOST",
  "POSTGRES_PASSWORD",
  "POSTGRES_PORT",
  "POSTGRES_USER",
  "REDIS_URL",
];

export const RESERVED_FRONTEND = ["REACT_CARE_API_URL"];
