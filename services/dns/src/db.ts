// D1 rows and queries.

export interface ClinicRow {
  id: string;
  pubkey: string;
  created_at: number;
  created_ip: string;
  hostname_hint: string;
  acme_user: string | null;
  acme_key_hash: string | null;
  listing_opt_in: number;
  last_seen: number | null;
  last_ip: string | null;
  revoked_at: number | null;
}

export interface AddressRow {
  id: string;
  ipv4: string | null;
  ipv6: string | null;
  a_record_id: string | null;
  aaaa_record_id: string | null;
  updated_at: number;
}

export interface TxtRow {
  record_id: string;
  id: string;
  value: string;
  created_at: number;
}

export function getClinic(db: D1Database, id: string): Promise<ClinicRow | null> {
  return db.prepare("SELECT * FROM clinics WHERE id = ?").bind(id).first<ClinicRow>();
}

export function getClinicByAcmeUser(db: D1Database, user: string): Promise<ClinicRow | null> {
  return db.prepare("SELECT * FROM clinics WHERE acme_user = ? AND revoked_at IS NULL").bind(user).first<ClinicRow>();
}

// Returns false if the id is already in use.
export async function insertClinic(db: D1Database, row: ClinicRow): Promise<boolean> {
  const out = await db
    .prepare(
      "INSERT INTO clinics (id, pubkey, created_at, created_ip, hostname_hint, acme_user, acme_key_hash, listing_opt_in, last_seen, last_ip, revoked_at) " +
        "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL) ON CONFLICT (id) DO NOTHING RETURNING id",
    )
    .bind(
      row.id,
      row.pubkey,
      row.created_at,
      row.created_ip,
      row.hostname_hint,
      row.acme_user,
      row.acme_key_hash,
      row.listing_opt_in,
      row.last_seen,
      row.last_ip,
    )
    .first<{ id: string }>();
  return out !== null;
}

export async function touchClinic(db: D1Database, id: string, now: number, ipKey: string): Promise<void> {
  await db.prepare("UPDATE clinics SET last_seen = ?, last_ip = ? WHERE id = ?").bind(now, ipKey, id).run();
}

export async function setListing(db: D1Database, id: string, optIn: boolean): Promise<void> {
  await db.prepare("UPDATE clinics SET listing_opt_in = ? WHERE id = ?").bind(optIn ? 1 : 0, id).run();
}

export async function setCredentials(db: D1Database, id: string, acmeUser: string, acmeHash: string, pubkey: string): Promise<void> {
  await db
    .prepare("UPDATE clinics SET acme_user = ?, acme_key_hash = ?, pubkey = ? WHERE id = ? AND revoked_at IS NULL")
    .bind(acmeUser, acmeHash, pubkey, id)
    .run();
}

export async function revokeClinic(db: D1Database, id: string, now: number): Promise<void> {
  await db
    .prepare("UPDATE clinics SET revoked_at = COALESCE(revoked_at, ?), acme_user = NULL, acme_key_hash = NULL, listing_opt_in = 0 WHERE id = ?")
    .bind(now, id)
    .run();
}

export function getAddress(db: D1Database, id: string): Promise<AddressRow | null> {
  return db.prepare("SELECT * FROM addresses WHERE id = ?").bind(id).first<AddressRow>();
}

export async function putAddress(db: D1Database, row: AddressRow): Promise<void> {
  await db
    .prepare(
      "INSERT INTO addresses (id, ipv4, ipv6, a_record_id, aaaa_record_id, updated_at) VALUES (?, ?, ?, ?, ?, ?) " +
        "ON CONFLICT (id) DO UPDATE SET ipv4 = excluded.ipv4, ipv6 = excluded.ipv6, a_record_id = excluded.a_record_id, " +
        "aaaa_record_id = excluded.aaaa_record_id, updated_at = excluded.updated_at",
    )
    .bind(row.id, row.ipv4, row.ipv6, row.a_record_id, row.aaaa_record_id, row.updated_at)
    .run();
}

export async function deleteAddress(db: D1Database, id: string): Promise<void> {
  await db.prepare("DELETE FROM addresses WHERE id = ?").bind(id).run();
}

// Newest first.
export async function listTxt(db: D1Database, id: string): Promise<TxtRow[]> {
  const out = await db.prepare("SELECT * FROM txt_records WHERE id = ? ORDER BY created_at DESC, rowid DESC").bind(id).all<TxtRow>();
  return out.results;
}

export async function insertTxt(db: D1Database, row: TxtRow): Promise<void> {
  await db
    .prepare("INSERT INTO txt_records (record_id, id, value, created_at) VALUES (?, ?, ?, ?)")
    .bind(row.record_id, row.id, row.value, row.created_at)
    .run();
}

export async function deleteTxt(db: D1Database, recordId: string): Promise<void> {
  await db.prepare("DELETE FROM txt_records WHERE record_id = ?").bind(recordId).run();
}

export async function insertNonce(db: D1Database, nonce: string, expiresAt: number): Promise<void> {
  await db.prepare("INSERT INTO nonces (nonce, expires_at) VALUES (?, ?)").bind(nonce, expiresAt).run();
}

// Deletes the nonce and tells if it was there and not expired. A nonce works one time only.
export async function consumeNonce(db: D1Database, nonce: string, now: number): Promise<boolean> {
  const row = await db
    .prepare("DELETE FROM nonces WHERE nonce = ? RETURNING expires_at")
    .bind(nonce)
    .first<{ expires_at: number }>();
  return row !== null && row.expires_at >= now;
}
