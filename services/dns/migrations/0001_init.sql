-- care-pi DNS/ACME service: initial schema.
-- Times are Unix seconds (UTC). Network keys are an IPv4 address or an IPv6 /64 prefix.

-- One row per registered clinic. The service never deletes a row, so an id is never used again.
CREATE TABLE clinics (
  id             TEXT PRIMARY KEY,            -- "<slug>-<4 base32>", for example "lumine-k3x9"
  pubkey         TEXT NOT NULL,               -- Ed25519 public key, standard base64 of 32 bytes
  created_at     INTEGER NOT NULL,
  created_ip     TEXT NOT NULL,               -- network key of the registration request
  hostname_hint  TEXT NOT NULL,               -- as sent by the Pi (max. 63 characters)
  acme_user      TEXT UNIQUE,                 -- acme-dns username (UUID); NULL after revocation
  acme_key_hash  TEXT,                        -- hex SHA-256 of the acme-dns password; NULL after revocation
  listing_opt_in INTEGER NOT NULL DEFAULT 0,  -- 1: GET /lookup can show this clinic
  last_seen      INTEGER,                     -- last authenticated request
  last_ip        TEXT,                        -- network key of the last authenticated request
  revoked_at     INTEGER                      -- set by DELETE /v1/clinics/{id} or by an operator
);

CREATE INDEX clinics_lookup_created ON clinics (created_ip) WHERE listing_opt_in = 1 AND revoked_at IS NULL;
CREATE INDEX clinics_lookup_last ON clinics (last_ip) WHERE listing_opt_in = 1 AND revoked_at IS NULL;

-- The LAN addresses of a clinic and the Cloudflare DNS record ids that hold them.
CREATE TABLE addresses (
  id             TEXT PRIMARY KEY REFERENCES clinics (id),
  ipv4           TEXT,
  ipv6           TEXT,
  a_record_id    TEXT,
  aaaa_record_id TEXT,
  updated_at     INTEGER NOT NULL
);

-- The ACME TXT records at _acme-challenge.<id>.<base domain>. The service keeps at most two per clinic
-- (the rolling update of acme-dns) and the scheduled handler deletes records older than one hour.
CREATE TABLE txt_records (
  record_id  TEXT PRIMARY KEY,                -- Cloudflare DNS record id
  id         TEXT NOT NULL REFERENCES clinics (id),
  value      TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE INDEX txt_records_by_clinic ON txt_records (id, created_at);
CREATE INDEX txt_records_by_age ON txt_records (created_at);

-- Single-use registration nonces from GET /v1/nonce.
CREATE TABLE nonces (
  nonce      TEXT PRIMARY KEY,
  expires_at INTEGER NOT NULL
);

-- The replay cache of signed requests: SHA-256 of the clinic id and the canonical string.
CREATE TABLE replay (
  digest     TEXT PRIMARY KEY,
  expires_at INTEGER NOT NULL
);

-- Fixed-window rate-limit counters.
CREATE TABLE rate (
  bucket       TEXT NOT NULL,                 -- for example "register", "address", "acme"
  key          TEXT NOT NULL,                 -- clinic id or network key
  window_start INTEGER NOT NULL,
  count        INTEGER NOT NULL,
  PRIMARY KEY (bucket, key, window_start)
);
